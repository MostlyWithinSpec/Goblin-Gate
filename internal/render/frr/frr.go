// Package frr renders FRR's configuration for dynamic routing: BGP (bgpd)
// and OSPFv2 (ospfd), with zebra installing the learned routes into the main
// routing table. Nothing is rendered while both are off, and the applier then
// stops FRR.
//
// The box's policy routing looks up the main table first for everything but
// the default route (render.RulePrioMain, suppress_prefixlength 0), so learned
// routes to networks are used at once, while a learned default route never
// takes internet traffic away from the box's own gateways.
package frr

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render"
)

// Paths and the unit, shared with the applier and the status API.
const (
	Dir         = "/etc/frr/"
	ConfPath    = Dir + "frr.conf"
	DaemonsPath = Dir + "daemons"
	Unit        = "frr.service"
)

// Renderer implements commit.Renderer.
type Renderer struct{}

// Name returns "frr".
func (Renderer) Name() string { return "frr" }

// Render returns frr.conf and daemons while BGP or OSPF is on.
func (Renderer) Render(cfg *config.Config) (map[string][]byte, error) {
	r := cfg.Routing
	if !r.BGP.Enabled && !r.OSPF.Enabled {
		return map[string][]byte{}, nil
	}
	conf, err := Conf(cfg)
	if err != nil {
		return nil, err
	}
	return map[string][]byte{ConfPath: []byte(conf), DaemonsPath: []byte(Daemons(cfg))}, nil
}

// Daemons is FRR's daemons file: zebra always (watchfrr starts it), bgpd and
// ospfd as configured, every daemon listening on localhost only for vty.
func Daemons(cfg *config.Config) string {
	yn := func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	}
	var b strings.Builder
	b.WriteString("# Managed by Goblin Gate: changes are overwritten on the next commit.\n")
	fmt.Fprintf(&b, "bgpd=%s\nospfd=%s\n", yn(cfg.Routing.BGP.Enabled), yn(cfg.Routing.OSPF.Enabled))
	for _, d := range []string{"ospf6d", "ripd", "ripngd", "isisd", "pimd", "pim6d", "ldpd", "nhrpd", "eigrpd", "babeld", "sharpd", "pbrd", "bfdd", "fabricd", "vrrpd", "pathd"} {
		fmt.Fprintf(&b, "%s=no\n", d)
	}
	b.WriteString(`vtysh_enable=yes
zebra_options="  -A 127.0.0.1 -s 90000000"
bgpd_options="   -A 127.0.0.1"
ospfd_options="  -A 127.0.0.1"
staticd_options="-A 127.0.0.1"
`)
	return b.String()
}

// Conf renders frr.conf.
func Conf(cfg *config.Config) (string, error) {
	r := cfg.Routing
	rid := cfg.RoutingRouterID()
	if !rid.IsValid() {
		return "", fmt.Errorf("frr: no router ID")
	}
	host := cfg.System.Hostname
	if host == "" {
		host = "goblin-gate"
	}
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	w("! Managed by Goblin Gate: changes are overwritten on the next commit.")
	w("frr defaults traditional")
	w("hostname %s", host)
	w("log syslog informational")
	w("service integrated-vtysh-config")
	w("!")
	if r.BGP.Enabled {
		bgp(w, cfg, rid)
	}
	if r.OSPF.Enabled {
		if err := ospf(w, cfg, rid); err != nil {
			return "", err
		}
	}
	w("end")
	return b.String(), nil
}

// neighborTag names a neighbour's prefix lists and route maps.
func neighborTag(i int) string { return fmt.Sprintf("gg-n%d", i+1) }

func bgp(w func(string, ...any), cfg *config.Config, rid netip.Addr) {
	b := cfg.Routing.BGP
	var enabled []int
	for i, n := range b.Neighbors {
		if n.Enabled {
			enabled = append(enabled, i)
		}
	}
	// filters: a prefix list per family and direction, used by a route map
	// that every session gets (FRR refuses eBGP routes without a policy)
	for _, i := range enabled {
		n := b.Neighbors[i]
		for _, dir := range []struct {
			name  string
			rules []config.PrefixRule
		}{{"in", n.Import}, {"out", n.Export}} {
			tag := neighborTag(i) + "-" + dir.name
			if len(dir.rules) == 0 {
				w("route-map %s permit 10", tag)
				w("exit")
				w("!")
				continue
			}
			for _, fam := range []struct {
				ip string
				v4 bool
			}{{"ip", true}, {"ipv6", false}} {
				seq := 0
				for _, rule := range dir.rules {
					if rule.Prefix.Addr().Is4() != fam.v4 {
						continue
					}
					seq += 5
					le := ""
					if rule.MaxLength > rule.Prefix.Bits() {
						le = fmt.Sprintf(" le %d", rule.MaxLength)
					}
					w("%s prefix-list %s seq %d %s %s%s", fam.ip, tag, seq, rule.Action, rule.Prefix, le)
				}
				if seq == 0 {
					// no rule for this family: an empty list denies all of it
					w("%s prefix-list %s seq 5 deny any", fam.ip, tag)
				}
			}
			w("route-map %s permit 10", tag)
			w(" match ip address prefix-list %s", tag)
			w("exit")
			w("route-map %s permit 20", tag)
			w(" match ipv6 address prefix-list %s", tag)
			w("exit")
			w("!")
		}
	}
	w("router bgp %d", b.AS)
	w(" bgp router-id %s", rid)
	w(" bgp log-neighbor-changes")
	w(" no bgp default ipv4-unicast")
	for _, i := range enabled {
		n := b.Neighbors[i]
		w(" neighbor %s remote-as %d", n.Address, n.RemoteAS)
		if n.Description != "" {
			w(" neighbor %s description %s", n.Address, n.Description)
		}
		if n.Password != "" {
			w(" neighbor %s password %s", n.Address, n.Password)
		}
		if n.Multihop > 0 {
			w(" neighbor %s ebgp-multihop %d", n.Address, n.Multihop)
		}
	}
	announce := append(cfg.AnnouncePrefixes(), siteAnnounce(cfg)...)
	for _, fam := range []struct {
		af string
		v4 bool
	}{{"ipv4 unicast", true}, {"ipv6 unicast", false}} {
		var nets []netip.Prefix
		for _, p := range announce {
			if p.Addr().Is4() == fam.v4 {
				nets = append(nets, p)
			}
		}
		var nbrs []int
		for _, i := range enabled {
			if b.Neighbors[i].Address.Is4() == fam.v4 {
				nbrs = append(nbrs, i)
			}
		}
		if len(nbrs) == 0 && len(nets) == 0 {
			continue
		}
		w(" !")
		w(" address-family %s", fam.af)
		for _, p := range nets {
			w("  network %s", p)
		}
		if b.RedistributeConnected {
			w("  redistribute connected")
		}
		if b.RedistributeStatic {
			w("  redistribute static")
			w("  redistribute kernel")
		}
		for _, i := range nbrs {
			n := b.Neighbors[i]
			w("  neighbor %s activate", n.Address)
			w("  neighbor %s route-map %s-in in", n.Address, neighborTag(i))
			w("  neighbor %s route-map %s-out out", n.Address, neighborTag(i))
		}
		w(" exit-address-family")
	}
	w("exit")
	w("!")
}

func ospf(w func(string, ...any), cfg *config.Config, rid netip.Addr) error {
	o := cfg.Routing.OSPF
	devs := render.Devices(cfg)
	type ifc struct {
		dev, area string
		cost      int
		passive   bool
	}
	var ifcs []ifc
	for _, a := range o.Areas {
		id, ok := config.OSPFAreaID(a.ID)
		if !ok {
			return fmt.Errorf("frr: OSPF area %q", a.ID)
		}
		for _, in := range a.Interfaces {
			dev, ok := devs[in.Name]
			if !ok {
				return fmt.Errorf("frr: OSPF interface %q", in.Name)
			}
			ifcs = append(ifcs, ifc{dev, id, in.Cost, in.Passive})
		}
	}
	slices.SortFunc(ifcs, func(a, b ifc) int { return strings.Compare(a.dev, b.dev) })
	for _, in := range ifcs {
		w("interface %s", in.dev)
		w(" ip ospf area %s", in.area)
		if in.cost > 0 {
			w(" ip ospf cost %d", in.cost)
		}
		if in.passive {
			w(" ip ospf passive")
		}
		w("exit")
		w("!")
	}
	w("router ospf")
	w(" ospf router-id %s", rid)
	w(" log-adjacency-changes")
	if o.RedistributeConnected {
		w(" redistribute connected")
	}
	sites := siteAnnounce(cfg)
	if o.RedistributeStatic {
		w(" redistribute static")
		w(" redistribute kernel")
	} else if len(sites) > 0 {
		// site routes are networkd's (kernel) routes: only those
		w(" redistribute kernel route-map gg-sites")
	}
	w("exit")
	w("!")
	if !o.RedistributeStatic && len(sites) > 0 {
		for i, p := range sites {
			w("ip prefix-list gg-sites seq %d permit %s", (i+1)*5, p)
		}
		w("route-map gg-sites permit 10")
		w(" match ip address prefix-list gg-sites")
		w("exit")
		w("!")
	}
	return nil
}

// siteAnnounce is what the site-to-site VPN adds to the routing protocols
// when vpn.sites.announce is on: the remote sites' IPv4 networks.
func siteAnnounce(cfg *config.Config) []netip.Prefix {
	if !cfg.VPN.Sites.Announce {
		return nil
	}
	var out []netip.Prefix
	for p := range cfg.RemoteSiteNetworks() {
		if p.Addr().Is4() {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b netip.Prefix) int { return strings.Compare(a.String(), b.String()) })
	return out
}
