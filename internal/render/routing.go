package render

import (
	"fmt"
	"sort"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// Multi-WAN routing plan (task 049), shared by the networkd and nft
// renderers and goblind's gateway monitor so they agree on numbers.
//
//	table 100+i  gateway i's own route (networkd): DHCP/static default via
//	             the router, or the WireGuard tunnel device; the monitor
//	             pings through it (rule "to <monitor ip>")
//	table 200+k  routing target k (every gateway and group): goblind writes
//	             the default route of the target's live gateways, a
//	             blackhole for a kill-switch tunnel that is down, or the
//	             default route when nothing is up
//	table 250    the default route (default_gateway, or every gateway in
//	             list order as failover), written by goblind
//
// ip rules (systemd-networkd RoutingPolicyRule):
//
//	500        lookup main suppress_prefixlength 0 (LAN and connected
//	           routes first, even for marked traffic)
//	900+i      to <monitor ip of gateway i> lookup 100+i
//	1000+k     fwmark <mark k>/RouteMarkMask lookup 200+k
//	32000      lookup 250
//
// Marks: nft sets RouteMarkMask bits of the ct mark to target k+1 for new
// connections (policy order, first match; inbound connections from a WAN
// get that WAN's gateway, so replies leave where they came in) and copies
// them to the packet mark of every packet arriving from the LAN side.
const (
	GatewayTableBase = 100
	TargetTableBase  = 200
	DefaultTable     = 250
	RouteMarkMask    = uint32(0x00ff0000)
	RouteMarkShift   = 16

	RulePrioMain    = 500
	RulePrioMonitor = 900
	RulePrioMark    = 1000
	RulePrioDefault = 32000
)

// GatewayPlan is one gateway's place in the plan.
type GatewayPlan struct {
	config.Gateway
	Device     string // kernel device of its interface or tunnel
	Tunnel     bool   // a WireGuard client
	KillSwitch bool   // tunnel with kill switch
	Table      int    // 100+i
	MonitorIP  string // "" = not monitored
}

// TargetPlan is a routing target: a gateway or a group.
type TargetPlan struct {
	Name    string
	Group   bool
	Members []config.GroupMember // a gateway target: itself, tier 1
	Table   int                  // 200+k
	Mark    uint32               // (k+1) << RouteMarkShift
	// KillSwitch: drop instead of falling back to the default route when
	// no member is up (a single kill-switch tunnel, or a group of only
	// kill-switch tunnels).
	KillSwitch bool
}

// RoutingPlan is the whole multi-WAN layout; Enabled is false without
// gateways (the single-WAN default route stays in the main table).
type RoutingPlan struct {
	Enabled  bool
	Gateways []GatewayPlan
	Targets  []TargetPlan
	Default  TargetPlan // the default route: default_gateway or all gateways
}

// Routing computes the plan.
func Routing(cfg *config.Config) (RoutingPlan, error) {
	var p RoutingPlan
	if len(cfg.Gateways) == 0 {
		return p, nil
	}
	p.Enabled = true
	devs := Devices(cfg)
	clients := map[string]config.WireGuardClient{}
	for _, w := range cfg.VPN.WireGuardClients {
		clients[w.Name] = w
	}
	monitor := cfg.MonitorIPs()
	byName := map[string]GatewayPlan{}
	for i, g := range cfg.Gateways {
		gp := GatewayPlan{Gateway: g, Table: GatewayTableBase + i, MonitorIP: monitor[g.Name]}
		if w, ok := clients[g.Interface]; ok {
			gp.Device, gp.Tunnel, gp.KillSwitch = w.Name, true, w.KillSwitch
		} else if d, ok := devs[g.Interface]; ok {
			gp.Device = d
		} else {
			return p, fmt.Errorf("gateway %q: unknown interface %q", g.Name, g.Interface)
		}
		p.Gateways = append(p.Gateways, gp)
		byName[g.Name] = gp
	}
	addTarget := func(t TargetPlan) {
		k := len(p.Targets)
		t.Table, t.Mark = TargetTableBase+k, uint32(k+1)<<RouteMarkShift
		p.Targets = append(p.Targets, t)
	}
	for _, g := range p.Gateways {
		addTarget(TargetPlan{Name: g.Name, Members: []config.GroupMember{{Gateway: g.Name, Tier: 1, Weight: 1}}, KillSwitch: g.KillSwitch})
	}
	for _, gg := range cfg.GatewayGroups {
		ks := len(gg.Members) > 0
		for _, m := range gg.Members {
			ks = ks && byName[m.Gateway].KillSwitch
		}
		addTarget(TargetPlan{Name: gg.Name, Group: true, Members: normalize(gg.Members), KillSwitch: ks})
	}
	if len(p.Targets) > 45 {
		return p, fmt.Errorf("too many gateways and groups (%d, at most 45)", len(p.Targets))
	}
	p.Default = TargetPlan{Name: "default", Table: DefaultTable}
	if cfg.DefaultGateway != "" {
		t, ok := p.Target(cfg.DefaultGateway)
		if !ok {
			return p, fmt.Errorf("default_gateway: unknown gateway or group %q", cfg.DefaultGateway)
		}
		p.Default.Members, p.Default.KillSwitch = t.Members, false
	} else {
		// every gateway, failing over in list order (tunnels only when
		// they are the only gateways: a VPN is opt-in per policy)
		tier := 1
		for _, g := range p.Gateways {
			if !g.Tunnel {
				p.Default.Members = append(p.Default.Members, config.GroupMember{Gateway: g.Name, Tier: tier, Weight: 1})
				tier++
			}
		}
		if len(p.Default.Members) == 0 {
			for i, g := range p.Gateways {
				p.Default.Members = append(p.Default.Members, config.GroupMember{Gateway: g.Name, Tier: i + 1, Weight: 1})
			}
		}
	}
	return p, nil
}

// Target returns the routing target of a gateway or group name.
func (p RoutingPlan) Target(name string) (TargetPlan, bool) {
	for _, t := range p.Targets {
		if t.Name == name {
			return t, true
		}
	}
	return TargetPlan{}, false
}

// Gateway returns a gateway's plan.
func (p RoutingPlan) Gateway(name string) (GatewayPlan, bool) {
	for _, g := range p.Gateways {
		if g.Name == name {
			return g, true
		}
	}
	return GatewayPlan{}, false
}

// normalize fills default tiers/weights and sorts by tier (stable).
func normalize(ms []config.GroupMember) []config.GroupMember {
	out := append([]config.GroupMember(nil), ms...)
	for i := range out {
		if out[i].Tier == 0 {
			out[i].Tier = 1
		}
		if out[i].Weight == 0 {
			out[i].Weight = 1
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Tier < out[j].Tier })
	return out
}
