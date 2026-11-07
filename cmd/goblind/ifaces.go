package main

import (
	"net"
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render"
)

// tailscaleNets are the addresses Tailscale hands out; the box's own
// tailscale0 address is a single host, so its peers are matched by these.
var tailscaleNets = []netip.Prefix{netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("fd7a:115c:a1e0::/48")}

// liveNets returns the networks a device has now, IPv4 and IPv6, without
// link-local ones (every interface has fe80::/64).
func liveNets(dev string) []netip.Prefix {
	ifc, err := net.InterfaceByName(dev)
	if err != nil {
		return nil
	}
	addrs, _ := ifc.Addrs()
	var out []netip.Prefix
	for _, a := range addrs {
		p, err := netip.ParsePrefix(a.String())
		if err != nil || p.Addr().IsLinkLocalUnicast() || p.Addr().IsLoopback() {
			continue
		}
		out = append(out, p)
	}
	return out
}

// ifaceNet is the network of an interface, VLAN or VPN.
type ifaceNet struct {
	name string
	net  netip.Prefix
}

// ifaceNets is what traffic is matched against: the networks, longest prefix
// first, and the WAN interfaces (incl. VLANs on one).
type ifaceNets struct {
	nets []ifaceNet
	wans []string
}

// interfaceNets lists the networks of the enabled interfaces, VLANs and VPNs:
// their static IPv4 and IPv6 addresses plus the addresses their devices have
// now (live; nil = none), which covers a WAN by DHCP and delegated IPv6
// prefixes.
func interfaceNets(cfg *config.Config, live func(dev string) []netip.Prefix) ifaceNets {
	var out ifaceNets
	devs := render.Devices(cfg)
	add := func(name string, p *netip.Prefix) {
		if p != nil {
			out.nets = append(out.nets, ifaceNet{name, p.Masked()})
		}
	}
	addAll := func(name string, ipv4 *netip.Prefix, ipv6 *config.IPv6) {
		add(name, ipv4)
		if ipv6 != nil {
			add(name, ipv6.Address)
		}
		if live != nil {
			for _, lp := range live(devs[name]) {
				add(name, &lp)
			}
		}
	}
	wan := map[string]bool{}
	for _, i := range cfg.Interfaces {
		if !i.Enabled {
			continue
		}
		addAll(i.Name, i.Address, i.IPv6)
		if i.Role == "wan" {
			wan[i.Name] = true
			out.wans = append(out.wans, i.Name)
		}
	}
	for _, v := range cfg.VLANs {
		if !v.Enabled {
			continue
		}
		addAll(v.Name, v.Address, v.IPv6)
		if wan[v.Parent] {
			out.wans = append(out.wans, v.Name)
		}
	}
	// VPN clients: the WireGuard servers' tunnel networks and Tailscale's
	for _, w := range cfg.VPN.WireGuard {
		if w.Enabled && w.Address.IsValid() {
			add(w.Name, &w.Address)
		}
	}
	if cfg.VPN.Tailscale.Enabled {
		for _, p := range tailscaleNets {
			add(config.TailscaleInterface, &p)
		}
	}
	sort.SliceStable(out.nets, func(i, j int) bool { return out.nets[i].net.Bits() > out.nets[j].net.Bits() })
	return out
}

// holder names the network in nets that holds ip ("" = none).
func (n ifaceNets) holder(ip string) (string, bool) {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return "", false
	}
	a = a.Unmap()
	for _, in := range n.nets {
		if in.net.Contains(a) {
			return in.name, true
		}
	}
	return "", true
}

// lookupInterface names the interface traffic from src to dst came in on: the
// one whose network holds src, or for traffic from outside, the WAN it
// arrived at (the WAN holding dst, else the only WAN). "" = unknown.
func lookupInterface(n ifaceNets, src, dst string) string {
	name, ok := n.holder(src)
	if name != "" || !ok {
		return name
	}
	if d, _ := n.holder(dst); d != "" {
		for _, w := range n.wans {
			if w == d {
				return w
			}
		}
	}
	if len(n.wans) == 1 {
		return n.wans[0]
	}
	return ""
}

// interfaceForAddr names the interface or VLAN traffic from src to dst came
// in on ("" = unknown). Traffic events from the IPS's flow records come
// without an interface; their addresses tell which network they began on.
// The networks are re-read from the running config every 10 s.
func interfaceForAddr(store *config.Store) func(src, dst string) string {
	var (
		mu     sync.Mutex
		nets   ifaceNets
		loaded time.Time
	)
	return func(src, dst string) string {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(loaded) > 10*time.Second {
			if cfg, err := store.Running(); err == nil {
				nets = interfaceNets(cfg, liveNets)
			}
			loaded = time.Now()
		}
		return lookupInterface(nets, src, dst)
	}
}
