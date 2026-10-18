package pfsense

import (
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// filterRules converts the firewall rules to policies in pfSense's order:
// floating rules first, then each interface's list (pfSense matches on the
// interface a packet arrives on, so per-interface lists never compete).
// Rules created for port forwards are covered by the forwards themselves.
func (im *importer) filterRules() {
	var floating, perIface []pfRule
	nat := map[string]bool{}
	for _, r := range im.pf.Filter {
		if strings.HasPrefix(r.AssociatedRuleID, "nat_") {
			nat[r.Tracker] = true
			continue
		}
		if r.Floating == "yes" {
			floating = append(floating, r)
		} else {
			perIface = append(perIface, r)
		}
	}
	if n := len(nat); n > 0 {
		im.add("rules", "port forward rules", Converted, "%d rule(s) pfSense created for port forwards: the forwards let their traffic in", n)
	}
	used := map[string]bool{}
	id := uint32(0)
	for _, list := range [][]pfRule{floating, perIface} {
		for _, r := range list {
			id++
			im.rule(r, id, used)
		}
	}
}

func (im *importer) rule(r pfRule, id uint32, used map[string]bool) {
	label := r.Descr
	if label == "" {
		label = fmt.Sprintf("%s rule %d", r.Interface, id)
	}
	skip := func(why string, args ...any) { im.add("rules", label, Skipped, why, args...) }

	var src []string
	for _, key := range strings.Split(r.Interface, ",") {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		name, ok := im.ifName(key)
		if !ok {
			skip("interface %q (VPN, interface group or unassigned) is not converted", key)
			return
		}
		src = append(src, name)
	}
	if len(src) == 0 {
		src = []string{"all"}
	}
	action := "accept"
	switch r.Type {
	case "", "pass":
	case "block":
		action = "deny"
	case "reject":
		action = "deny"
	default:
		skip("rule type %q is not supported", r.Type)
		return
	}
	s, d := im.ruleAddr(r.Source), im.ruleAddr(r.Destination)
	switch {
	case s.why != "":
		skip("source: %s", s.why)
		return
	case d.why != "":
		skip("destination: %s", d.why)
		return
	case d.self:
		skip("traffic to the firewall itself: Goblin Gate opens its own services per interface (Interfaces → allowed access: ping, https, ssh), DNS and DHCP automatically")
		return
	case s.self:
		skip("traffic from the firewall itself is always allowed here")
		return
	}
	if s.feed != "" || d.feed != "" {
		feed := s.feed + d.feed
		if action == "deny" {
			for i := range im.cfg.Feeds {
				if im.cfg.Feeds[i].Name == feed {
					im.cfg.Feeds[i].BlockWAN = true
				}
			}
			im.add("rules", label, Approximated, "blocking a URL alias → threat feed %q now drops its addresses on every WAN interface", feed)
		} else {
			skip("allowing traffic by a URL alias (feed %q) is not supported", feed)
		}
		return
	}
	if r.Source.Port != "" {
		skip("source ports are not supported in policies")
		return
	}
	svcs, why := im.services(r.Protocol, r.Destination.Port)
	if why != "" {
		skip("%s", why)
		return
	}
	name := unique(used, slug(r.Descr, fmt.Sprintf("%s-rule-%d", strings.Join(src, "-"), id)))
	p := config.Policy{ID: id, Name: name, Enabled: r.Disabled == nil, SrcIntf: src, DstIntf: []string{"all"},
		SrcAddr: s.refs, DstAddr: d.refs, Service: svcs, Action: action}
	if r.Log != nil {
		p.Log = "all"
	}
	var notes []string
	if r.Sched != "" {
		if im.schedOK[r.Sched] {
			p.Schedule = slug(r.Sched, "schedule")
		} else {
			notes = append(notes, "its schedule "+r.Sched+" could not be converted, so it applies all the time")
		}
	}
	if r.Type == "reject" {
		notes = append(notes, "reject became deny (dropped without a reply)")
	}
	if r.Floating == "yes" {
		notes = append(notes, "floating rule: matched on the interfaces it lists, as traffic arriving there")
		if r.Direction == "out" {
			notes = append(notes, "direction \"out\" is matched as incoming traffic instead")
		}
	}
	if r.Gateway != "" {
		if ours, ok := im.gatewayNames[r.Gateway]; ok && action == "accept" {
			p.Gateway = ours
		} else {
			notes = append(notes, "its gateway "+r.Gateway+" (a VPN or an unconverted gateway) is ignored: the traffic takes the default route")
		}
	}
	if r.IPProtocol == "inet6" && len(s.refs) == 1 && s.refs[0] == "all" && len(d.refs) == 1 && d.refs[0] == "all" {
		notes = append(notes, "it was IPv6 only; without addresses it now matches IPv4 too")
	}
	if r.ICMPType != "" && r.ICMPType != "any" {
		notes = append(notes, "ICMP type "+r.ICMPType+" became any ICMP")
	}
	im.cfg.Policies = append(im.cfg.Policies, p)
	if len(notes) > 0 {
		im.add("rules", label, Approximated, "policy %d %q: %s", id, name, strings.Join(notes, "; "))
	} else {
		im.add("rules", label, Converted, "policy %d %q", id, name)
	}
}

// outboundNAT sets nat on accept policies that leave through a WAN: pfSense
// automatic/hybrid outbound NAT masquerades everything leaving a WAN;
// Goblin Gate does it per policy (nat: true), for traffic leaving a WAN.
func (im *importer) outboundNAT() {
	mode := im.pf.NAT.Outbound.Mode
	if mode == "" {
		mode = "automatic"
	}
	if mode == "disabled" {
		im.add("nat", "outbound NAT", Converted, "disabled: no policy masquerades")
		return
	}
	n := 0
	for i, p := range im.cfg.Policies {
		if p.Action != "accept" {
			continue
		}
		fromWAN := true
		for _, s := range p.SrcIntf {
			fromWAN = fromWAN && im.isWAN(s)
		}
		if !fromWAN {
			im.cfg.Policies[i].NAT = true
			n++
		}
	}
	detail := fmt.Sprintf("%s mode: %d policy(ies) from internal interfaces masquerade traffic that leaves through a WAN", mode, n)
	switch mode {
	case "automatic", "hybrid":
		im.add("nat", "outbound NAT", Converted, "%s", detail)
	default:
		im.add("nat", "outbound NAT", Approximated, "%s; manual outbound rules are not converted one by one", detail)
	}
	static := 0
	for _, r := range im.pf.NAT.Outbound.Rules {
		if r.StaticNATPort != nil && r.Disabled == nil {
			static++
		}
	}
	if static > 0 {
		im.add("nat", "static port", Converted, "%d static-port rule(s) need no setting: Linux keeps the source port unless two connections clash", static)
	}
}

// portForwards converts NAT port forwards. A forward of every port is
// flagged: it exposes the whole host.
func (im *importer) portForwards() {
	used := map[string]bool{}
	for i, r := range im.pf.NAT.Rules {
		label := r.Descr
		if label == "" {
			label = fmt.Sprintf("port forward %d", i+1)
		}
		skip := func(why string, args ...any) { im.add("nat", label, Skipped, why, args...) }
		if r.Disabled != nil {
			skip("disabled in pfSense")
			continue
		}
		if r.NoRDR != nil {
			skip("a \"no redirect\" rule is not needed")
			continue
		}
		in, ok := im.ifName(r.Interface)
		if !ok {
			skip("interface %q is not converted", r.Interface)
			continue
		}
		target := r.Target
		if al, ok := im.aliases[target]; ok && al.Type == "host" && len(strings.Fields(al.Address)) == 1 {
			target = strings.Fields(al.Address)[0]
		}
		to, err := netip.ParseAddr(target)
		if err != nil || !to.Is4() {
			skip("target %q is not a single IPv4 address", r.Target)
			continue
		}
		ext := strings.ReplaceAll(r.Destination.Port, ":", "-")
		if al, ok := im.aliases[r.Destination.Port]; ok {
			if f := strings.Fields(al.Address); al.Type == "port" && len(f) == 1 {
				ext = strings.ReplaceAll(f[0], ":", "-")
			} else {
				skip("port alias %q with several entries: add one forward per port", r.Destination.Port)
				continue
			}
		}
		lo, hi, ok := portSpan(ext)
		if !ok {
			skip("external port %q not understood", r.Destination.Port)
			continue
		}
		toPort := ""
		if local, err := strconv.Atoi(r.LocalPort); err == nil && local != lo {
			if lo != hi {
				skip("moving the port range %s to start at %d is not supported: forward it unchanged or port by port", ext, local)
				continue
			}
			toPort = strconv.Itoa(local)
		}
		var protos []string
		switch r.Protocol {
		case "tcp", "udp":
			protos = []string{r.Protocol}
		case "tcp/udp":
			protos = []string{"tcp", "udp"}
		default:
			skip("protocol %q is not supported for port forwards", r.Protocol)
			continue
		}
		// A source the gate cannot express skips the forward: importing it
		// without the limit would open it to everyone.
		src := im.ruleAddr(r.Source)
		switch {
		case src.why != "":
			skip("source: %s", src.why)
			continue
		case src.self || src.feed != "":
			skip("source %q is not supported for port forwards", r.Source.Address+r.Source.Network)
			continue
		}
		var source []string
		if !slices.Contains(src.refs, "all") {
			source = src.refs
		}
		base := slug(r.Descr, fmt.Sprintf("forward-%d", i+1))
		for _, p := range protos {
			name := base
			if len(protos) > 1 {
				name = base + "-" + p
			}
			im.cfg.NAT.PortForwards = append(im.cfg.NAT.PortForwards, config.PortForward{
				Name: unique(used, name), InIntf: in, ExtPort: ext, Protocol: p, ToAddr: to.String(), ToPort: toPort, Source: source})
		}
		detail := fmt.Sprintf("%s %s → %s", strings.Join(protos, "+"), ext, to)
		if toPort != "" {
			detail += ":" + toPort
		}
		if source != nil {
			detail += ", only from " + strings.Join(source, ", ")
		}
		switch {
		case lo == 1 && hi == 65535 && source == nil:
			im.risk("nat", label, Converted, "%s: EVERY port of the WAN goes to %s; that host is fully exposed to the internet. Consider forwarding only the ports it needs, or UPnP", detail, to)
		default:
			im.add("nat", label, Converted, "%s", detail)
		}
	}
}

// portSpan parses "80" or "1000-2000".
func portSpan(s string) (int, int, bool) {
	a, b, isRange := strings.Cut(s, "-")
	lo, err := strconv.Atoi(strings.TrimSpace(a))
	if err != nil || lo < 1 || lo > 65535 {
		return 0, 0, false
	}
	hi := lo
	if isRange {
		if hi, err = strconv.Atoi(strings.TrimSpace(b)); err != nil || hi < lo || hi > 65535 {
			return 0, 0, false
		}
	}
	return lo, hi, true
}
