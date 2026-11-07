package nft

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/render/caddy"
)

// reverseProxy hands TCP 80 and 443 to the reverse proxy (internal/render/caddy)
// while it runs: on the WAN, and with lan_access on LAN-side interfaces for
// connections to a box address that is not one of its LAN-side addresses (a
// LAN client that resolved a public name to the WAN address). The admin page
// keeps 443 on LAN addresses. Caddy's ports only take redirected connections.
// IPv4: the NAT table is ip only.
func (r *ruleset) reverseProxy() {
	if !caddy.Active(r.cfg) {
		return
	}
	redirect := func(match, what string) {
		r.proxyPre = append(r.proxyPre,
			fmt.Sprintf("%s tcp dport 80 redirect to :%d comment %s", match, caddy.HTTPPort, quote("reverse proxy: http"+what)),
			fmt.Sprintf("%s tcp dport 443 redirect to :%d comment %s", match, caddy.HTTPSPort, quote("reverse proxy: https"+what)))
	}
	var in []string
	if wan := r.wanMatch(); wan != "" {
		redirect("iifname "+wan, "")
		in = append(in, r.wanDevs()...)
	}
	if r.cfg.ReverseProxy.LANAccessOn() {
		lan, own := r.lanSide()
		if len(lan) > 0 {
			m := "iifname " + devSet(lan) + " fib daddr type local"
			if len(own) > 0 {
				m += " ip daddr != " + addrSet(own)
			}
			redirect(m, " from the lan")
			in = append(in, lan...)
		}
	}
	if len(in) == 0 {
		return
	}
	slices.Sort(in)
	in = slices.Compact(in)
	r.input = append(r.input, fmt.Sprintf("iifname %s tcp dport { %d, %d } ct status dnat accept comment %s",
		devSet(in), caddy.HTTPPort, caddy.HTTPSPort, quote("reverse proxy")))
}

// wanDevs lists the devices of enabled WAN interfaces and VLANs on them.
func (r *ruleset) wanDevs() []string {
	wan := map[string]bool{}
	var devs []string
	for _, i := range r.cfg.Interfaces {
		wan[i.Name] = i.Role == "wan"
		if d, ok := r.devs[i.Name]; ok && i.Role == "wan" && i.Enabled {
			devs = append(devs, d)
		}
	}
	for _, v := range r.cfg.VLANs {
		if d, ok := r.devs[v.Name]; ok && wan[v.Parent] && v.Enabled {
			devs = append(devs, d)
		}
	}
	slices.Sort(devs)
	return devs
}

// lanSide lists the devices of enabled LAN-side interfaces and VLANs, and
// the box's static IPv4 addresses on them.
func (r *ruleset) lanSide() (devs []string, own []netip.Addr) {
	wan := map[string]bool{}
	for _, i := range r.cfg.Interfaces {
		wan[i.Name] = i.Role == "wan"
		if d, ok := r.devs[i.Name]; ok && i.Role != "wan" && i.Enabled {
			devs = append(devs, d)
			if i.Mode == "static" && i.Address != nil && i.Address.Addr().Is4() {
				own = append(own, i.Address.Addr())
			}
		}
	}
	for _, v := range r.cfg.VLANs {
		if d, ok := r.devs[v.Name]; ok && !wan[v.Parent] && v.Enabled {
			devs = append(devs, d)
			if v.Mode == "static" && v.Address != nil && v.Address.Addr().Is4() {
				own = append(own, v.Address.Addr())
			}
		}
	}
	slices.Sort(devs)
	slices.SortFunc(own, func(a, b netip.Addr) int { return a.Compare(b) })
	return devs, slices.Compact(own)
}

func addrSet(addrs []netip.Addr) string {
	if len(addrs) == 1 {
		return addrs[0].String()
	}
	s := make([]string, len(addrs))
	for i, a := range addrs {
		s[i] = a.String()
	}
	return "{ " + strings.Join(s, ", ") + " }"
}
