package nft

import (
	"fmt"
	"slices"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render"
)

// Policy routing marks (task 049, plan in internal/render/routing.go).
//
// New IPv4 connections from the LAN side walk the policies in order in
// chain route_policies (prerouting, before the routing decision; the
// forward chain is too late): the first matching policy decides - its
// gateway's mark in the ct mark, or no mark (the default route). New
// connections arriving on a WAN get that WAN's gateway, so replies leave
// where the connection came in. Every packet from the LAN side then carries
// its connection's route mark as packet mark, which the ip rules route on.
// Packets from a WAN never carry it: replies to the LAN use the main table.

// routeRules records a policy's route-chain rules (IPv4 only).
func (r *ruleset) routeRules(p config.Policy, iif string, src, dst addrRef, svc svcRef, times []string, fams []string) error {
	if !r.route.Enabled || !p.Enabled {
		return nil
	}
	verdict := "return"
	if p.Action == "accept" && p.Gateway != "" {
		t, ok := r.route.Target(p.Gateway)
		if !ok {
			return fmt.Errorf("unknown gateway %q", p.Gateway)
		}
		verdict = fmt.Sprintf("ct mark set ct mark and 0x%08x or 0x%08x return", ^render.RouteMarkMask, t.Mark)
	}
	for _, fam := range fams {
		if fam == "6" {
			continue // IPv6 always takes the default route
		}
		family := ""
		if fam == "" {
			family = "meta nfproto ipv4"
		}
		for _, m := range svc.matches(fam) {
			for _, tm := range times {
				r.routePol = append(r.routePol, joinNonEmpty(iif, family, src.match(fam), dst.match(fam), m, tm,
					verdict, "comment "+quote(p.Name)))
			}
		}
	}
	return nil
}

// routePrerouting returns the prerouting chain's rules.
func (r *ruleset) routePrerouting() []string {
	if !r.route.Enabled {
		return nil
	}
	ext := r.externalDevs()
	notExt := ""
	if len(ext) > 0 {
		notExt = "iifname != " + devSet(ext)
	}
	out := []string{joinNonEmpty("ct state new meta nfproto ipv4", notExt, "jump route_policies")}
	for _, g := range r.route.Gateways {
		t, _ := r.route.Target(g.Name)
		out = append(out, fmt.Sprintf("ct state new iifname %s ct mark set ct mark and 0x%08x or 0x%08x comment %s",
			quote(g.Device), ^render.RouteMarkMask, t.Mark, quote("replies leave through "+g.Name)))
	}
	out = append(out, joinNonEmpty(notExt, fmt.Sprintf("meta mark set ct mark and 0x%08x", render.RouteMarkMask)))
	return out
}

// externalDevs are the WAN devices plus the tunnels of WireGuard clients
// that are gateways: the outside, where traffic is masqueraded and route
// marks are never applied.
func (r *ruleset) externalDevs() []string {
	var out []string
	wan := map[string]bool{}
	for _, i := range r.cfg.Interfaces {
		wan[i.Name] = i.Role == "wan"
		if i.Role == "wan" && i.Enabled {
			out = append(out, r.devs[i.Name])
		}
	}
	for _, v := range r.cfg.VLANs {
		if wan[v.Parent] && v.Enabled {
			out = append(out, r.devs[v.Name])
		}
	}
	for _, g := range r.route.Gateways {
		if g.Tunnel {
			out = append(out, g.Device)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func devSet(devs []string) string {
	if len(devs) == 1 {
		return quote(devs[0])
	}
	q := make([]string, len(devs))
	for i, d := range devs {
		q[i] = quote(d)
	}
	return "{ " + strings.Join(q, ", ") + " }"
}
