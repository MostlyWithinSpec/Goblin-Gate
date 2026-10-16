package netif

import (
	"fmt"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render"
)

// Multi-WAN (task 049, plan in internal/render/routing.go): a WAN link with
// a gateway puts its default route into the gateway's own table instead of
// the main table (DHCP: RouteTable=, static: a [Route] with Table=);
// goblind's monitor writes the default route itself. Static routes go on
// the gateway's link. The ip rules hang off the loopback, which never goes
// down (networkd drops a link's rules with the link).

// RulesPath is the .network file carrying the routing policy rules.
const RulesPath = Dir + "/05-goblin-routing.network"

// linkRouting is what the plan asks of one link.
type linkRouting struct {
	table   int
	gateway string // static router address; "" = learned by DHCP
	routes  []string
}

// attachRouting gives the links their gateway tables and static routes.
func attachRouting(cfg *config.Config, plan render.RoutingPlan, links map[string]*link) error {
	if !plan.Enabled {
		return nil
	}
	for _, g := range plan.Gateways {
		if g.Tunnel {
			continue // the wireguard renderer handles tunnels
		}
		l, ok := links[g.Interface]
		if !ok {
			return fmt.Errorf("gateway %q: unknown interface %q", g.Name, g.Interface)
		}
		if l.route != nil {
			return fmt.Errorf("gateway %q: interface %q already has a gateway", g.Name, g.Interface)
		}
		r := &linkRouting{table: g.Table}
		if g.Address != "dhcp" {
			r.gateway = g.Address
		}
		l.route = r
	}
	for _, rt := range cfg.Routes {
		g, ok := plan.Gateway(rt.Gateway)
		if !ok || g.Tunnel {
			continue
		}
		l := links[g.Interface]
		via := "_dhcp4"
		if g.Address != "dhcp" {
			via = g.Address
		}
		l.route.routes = append(l.route.routes, fmt.Sprintf("\n[Route]\nDestination=%s\nGateway=%s\n", rt.Destination.Masked(), via))
	}
	return nil
}

// routeSections returns the routing sections of a link's .network file.
func (r *linkRouting) sections(dhcp bool) string {
	var b strings.Builder
	if dhcp {
		fmt.Fprintf(&b, "\n[DHCPv4]\nRouteTable=%d\n", r.table)
	} else if r.gateway != "" {
		fmt.Fprintf(&b, "\n[Route]\nGateway=%s\nTable=%d\n", r.gateway, r.table)
	}
	for _, s := range r.routes {
		b.WriteString(s)
	}
	return b.String()
}

// rulesFile renders the ip rules of the plan on the loopback.
func rulesFile(plan render.RoutingPlan) []byte {
	var b strings.Builder
	b.WriteString(header)
	b.WriteString("# Multi-WAN routing rules (see internal/render/routing.go)\n[Match]\nName=lo\n\n[Network]\nLinkLocalAddressing=no\nIPv6AcceptRA=no\n")
	fmt.Fprintf(&b, "\n# LAN and connected routes first, even for marked traffic\n[RoutingPolicyRule]\nTable=main\nSuppressPrefixLength=0\nPriority=%d\n", render.RulePrioMain)
	for i, g := range plan.Gateways {
		if g.MonitorIP == "" {
			continue
		}
		fmt.Fprintf(&b, "\n# monitor of gateway %s\n[RoutingPolicyRule]\nTo=%s/32\nTable=%d\nPriority=%d\n", g.Name, g.MonitorIP, g.Table, render.RulePrioMonitor+i)
	}
	for k, t := range plan.Targets {
		fmt.Fprintf(&b, "\n# policies routed to %s\n[RoutingPolicyRule]\nFirewallMark=0x%08x/0x%08x\nTable=%d\nPriority=%d\n", t.Name, t.Mark, render.RouteMarkMask, t.Table, render.RulePrioMark+k)
	}
	fmt.Fprintf(&b, "\n# everything else: the default route (goblind)\n[RoutingPolicyRule]\nTable=%d\nPriority=%d\n", render.DefaultTable, render.RulePrioDefault)
	return []byte(b.String())
}
