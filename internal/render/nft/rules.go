package nft

import (
	"fmt"
	"slices"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render"
)

// inputRules opens the box itself: admin access per interface (allow_access),
// DHCP client replies on dhcp-mode interfaces, the DHCP server where one is
// configured, and DNS on internal interfaces (Unbound, task 011).
func (r *ruleset) inputRules() {
	adminPort := r.cfg.System.AdminPort
	if adminPort == 0 {
		adminPort = DefaultPort
	}
	dhcpSrv := map[string]bool{}
	for _, d := range r.cfg.DHCP {
		dhcpSrv[d.Interface] = true
	}

	type ifc struct {
		name, mode string
		wan        bool
		enabled    bool
		access     []string
		v6         string // ipv6 mode, "" = none
	}
	v6mode := func(x *config.IPv6) string {
		if x == nil || x.Mode == "none" {
			return ""
		}
		return x.Mode
	}
	var all []ifc
	wan := map[string]bool{}
	for _, i := range r.cfg.Interfaces {
		wan[i.Name] = i.Role == "wan"
		all = append(all, ifc{i.Name, i.Mode, i.Role == "wan", i.Enabled, i.AllowAccess, v6mode(i.IPv6)})
	}
	// VLANs have no role of their own; a VLAN on a wan parent is wan.
	for _, v := range r.cfg.VLANs {
		all = append(all, ifc{v.Name, v.Mode, wan[v.Parent], v.Enabled, v.AllowAccess, v6mode(v.IPv6)})
	}
	sortBy(all, func(i ifc) string { return i.name })

	// IPv6 (task 045): neighbor discovery, router advertisements and path
	// MTU discovery must work on every IPv6 link; ICMPv6 errors for tracked
	// connections are already accepted as related.
	if r.cfg.IPv6Enabled() {
		r.input = append(r.input, "icmpv6 type { nd-neighbor-solicit, nd-neighbor-advert, nd-router-solicit, nd-router-advert, packet-too-big, destination-unreachable, time-exceeded, parameter-problem } accept comment "+quote("ipv6: neighbor discovery and errors"))
	}

	for _, i := range all {
		dev, ok := r.devs[i.name]
		if !ok || !i.enabled {
			continue
		}
		in := "iifname " + quote(dev)
		for _, a := range i.access {
			switch a {
			case "ping":
				r.input = append(r.input, fmt.Sprintf("%s icmp type echo-request accept comment %s", in, quote(i.name+": ping")))
				if i.v6 != "" {
					r.input = append(r.input, fmt.Sprintf("%s icmpv6 type echo-request accept comment %s", in, quote(i.name+": ping6")))
				}
			case "https":
				r.input = append(r.input, fmt.Sprintf("%s tcp dport %d accept comment %s", in, adminPort, quote(i.name+": https admin")))
			case "ssh":
				r.input = append(r.input, fmt.Sprintf("%s tcp dport %d accept comment %s", in, sshPort, quote(i.name+": ssh admin")))
			}
		}
		if i.mode == "dhcp" {
			r.input = append(r.input, fmt.Sprintf("%s udp sport 67 udp dport 68 accept comment %s", in, quote(i.name+": dhcp client")))
		}
		if i.v6 == "dhcp6" {
			r.input = append(r.input, fmt.Sprintf("%s ip6 saddr fe80::/10 udp sport 547 udp dport 546 accept comment %s", in, quote(i.name+": dhcpv6 client")))
		}
		if dhcpSrv[i.name] {
			r.input = append(r.input, fmt.Sprintf("%s udp dport 67 accept comment %s", in, quote(i.name+": dhcp server")))
		}
		if !i.wan {
			r.input = append(r.input, fmt.Sprintf("%s meta l4proto { tcp, udp } th dport 53 accept comment %s", in, quote(i.name+": dns")))
		}
	}
}

// portForwards renders DNAT rules and the forward accepts that let the
// translated connections through.
func (r *ruleset) portForwards() error {
	for _, pf := range r.cfg.NAT.PortForwards {
		if err := r.portForward(pf); err != nil {
			return fmt.Errorf("port forward %q: %w", pf.Name, err)
		}
	}
	return nil
}

func (r *ruleset) portForward(pf config.PortForward) error {
	dev, ok := r.devs[pf.InIntf]
	if !ok {
		return fmt.Errorf("unknown interface %q", pf.InIntf)
	}
	proto := strings.ToLower(pf.Protocol)
	if proto != "tcp" && proto != "udp" {
		return fmt.Errorf("unsupported protocol %q", pf.Protocol)
	}
	to, err := parseIPv4(pf.ToAddr)
	if err != nil {
		return fmt.Errorf("to_addr: %w", err)
	}
	ext, err := portSpan(pf.ExtPort)
	if err != nil {
		return fmt.Errorf("ext_port: %w", err)
	}
	// Internal port: unset or equal to ext keeps the port; a single port maps
	// everything to it. Remapping one range onto another is not supported.
	target := to.String()
	dst := ext
	if strings.TrimSpace(pf.ToPort) != "" {
		in, err := portSpan(pf.ToPort)
		if err != nil {
			return fmt.Errorf("to_port: %w", err)
		}
		switch {
		case in == ext:
		case in.lo == in.hi:
			target += ":" + formatPortSpan(in)
			dst = in
		default:
			return fmt.Errorf("to_port %q: a port range must equal ext_port", pf.ToPort)
		}
	}
	counter := r.names.get("portfwd_", pf.Name)
	r.counters = append(r.counters, counterDecl{counter, "port forward " + pf.Name})
	comment := quote("port forward " + pf.Name)
	r.pre = append(r.pre, fmt.Sprintf("iifname %s %s dport %s dnat to %s comment %s",
		quote(dev), proto, formatPortSpan(ext), target, comment))
	r.forward = append(r.forward, fmt.Sprintf("iifname %s ip daddr %s %s dport %s ct status dnat counter name %s accept comment %s",
		quote(dev), to, proto, formatPortSpan(dst), quote(counter), comment))
	return nil
}

func portSpan(s string) (span, error) {
	spans, err := parsePorts(s)
	if err != nil {
		return span{}, err
	}
	if len(spans) != 1 || strings.Contains(s, ",") {
		return span{}, fmt.Errorf("%q: want one port or one range", s)
	}
	return spans[0], nil
}

// policies renders one forward rule per enabled policy, in policy order.
func (r *ruleset) policies() error {
	ids := map[uint32]bool{}
	for _, p := range r.cfg.Policies {
		if ids[p.ID] {
			return fmt.Errorf("policy %d: duplicate id", p.ID)
		}
		ids[p.ID] = true
		counter := fmt.Sprintf("policy_%d", p.ID)
		r.counters = append(r.counters, counterDecl{counter, p.Name})
		if !p.Enabled {
			r.owner[len(r.forward)] = p.ID
			r.forward = append(r.forward, fmt.Sprintf("# policy %d %s is disabled", p.ID, quote(p.Name)))
			continue
		}
		rules, err := r.policy(p, counter)
		if err != nil {
			return fmt.Errorf("policy %d: %w", p.ID, err)
		}
		for i := range rules {
			r.owner[len(r.forward)+i] = p.ID
		}
		r.forward = append(r.forward, rules...)
	}
	return nil
}

func (r *ruleset) policy(p config.Policy, counter string) ([]string, error) {
	// A schedule limits every rule of the policy to its windows (task 057):
	// outside them the policy does not match and traffic falls through.
	times := []string{""}
	if p.Schedule != "" {
		var err error
		if times, err = scheduleMatches(r.cfg, p.Schedule); err != nil {
			return nil, err
		}
	}
	var verdict string
	switch p.Action {
	case "accept":
		verdict = "accept"
	case "deny":
		verdict = "drop"
	default:
		return nil, fmt.Errorf("unsupported action %q", p.Action)
	}
	iif, err := r.devMatch("iifname", p.SrcIntf)
	if err != nil {
		return nil, fmt.Errorf("src_intf: %w", err)
	}
	oif, err := r.devMatch("oifname", p.DstIntf)
	if err != nil {
		return nil, fmt.Errorf("dst_intf: %w", err)
	}
	src, err := r.addrMatch("saddr", p.SrcAddr, fmt.Sprintf("policy_%d_src", p.ID))
	if err != nil {
		return nil, fmt.Errorf("src_addr: %w", err)
	}
	dst, err := r.addrMatch("daddr", p.DstAddr, fmt.Sprintf("policy_%d_dst", p.ID))
	if err != nil {
		return nil, fmt.Errorf("dst_addr: %w", err)
	}
	svc, err := r.svcMatch(p.Service, fmt.Sprintf("policy_%d_svc", p.ID))
	if err != nil {
		return nil, fmt.Errorf("service: %w", err)
	}
	for _, f := range p.ThreatFeeds {
		if !r.feedExists(f) {
			return nil, fmt.Errorf("unknown threat feed %q", f)
		}
	}

	var log, marks string
	if p.Log == "all" || p.Log == "deny" && p.Action == "deny" {
		log = fmt.Sprintf("log group %d prefix %s", LogGroup, quote(fmt.Sprintf(LogPrefix, p.ID)))
	}
	if p.Action == "accept" {
		if p.NAT {
			marks = joinNonEmpty(marks, fmt.Sprintf("meta mark set meta mark or 0x%08x", MarkMasquerade))
		}
		if u, ok := r.ips[p.IPSProfile]; ok {
			// remember the profile on the connection and hand this first
			// packet to its Suricata; ipsQueues queues the rest
			marks = joinNonEmpty(marks, fmt.Sprintf("ct mark set ct mark and 0x%08x or 0x%08x",
				^(CtMarkIPSProfile|CtMarkIPS), u.ProfileMark|CtMarkIPS))
			verdict = queueVerdict(u)
		}
	}

	// One set of rules per address family the policy can match (task 045):
	// without address objects one family-less set; otherwise IPv4 and/or
	// IPv6, each with its own sets. All share the policy counter.
	fams := addrFamilies(src, dst)
	if len(fams) == 0 {
		return []string{fmt.Sprintf("# policy %d %s: its source and destination addresses share no address family", p.ID, quote(p.Name))}, nil
	}
	if err := r.routeRules(p, iif, src, dst, svc, times, fams); err != nil {
		return nil, err
	}
	var feedDrops, rules []string
	for _, fam := range fams {
		s, d, l4 := src.match(fam), dst.match(fam), svc.matches(fam)
		// Threat feeds: traffic this policy would accept is dropped first
		// when its source or destination is in one of the policy's IP feeds
		// (IPv4 sets). Domain and url feeds are handled by the DNS and web
		// filters; disabled feeds have no set and are skipped.
		if p.Action == "accept" && fam != "6" {
			for _, f := range p.ThreatFeeds {
				set, ok := r.feedSet[f]
				if !ok {
					continue
				}
				drop := fmt.Sprintf("counter name %s drop comment %s", quote(r.feedHit[f]), quote(p.Name+": feed "+f))
				for _, m := range l4 {
					for _, t := range times {
						for _, dir := range []string{"saddr", "daddr"} {
							feedDrops = append(feedDrops, joinNonEmpty(iif, oif, s, d, m, t, "ip "+dir+" @"+set, drop))
						}
					}
				}
			}
		}
		// A service list mixing port-less protocols (icmp, tcp on all
		// ports) with port pairs needs one rule per kind, and a schedule one
		// per time window.
		for _, m := range l4 {
			for _, t := range times {
				rules = append(rules, joinNonEmpty(iif, oif, s, d, m, t,
					"counter name "+quote(counter), log, marks, verdict, "comment "+quote(p.Name)))
			}
		}
	}
	return append(feedDrops, rules...), nil
}

// addrRef is a policy's source or destination address match per family.
type addrRef struct {
	all    bool   // no address match
	m4, m6 string // "ip saddr @set" / "ip6 saddr @set6"; "" = cannot match that family
}

func (a addrRef) match(fam string) string {
	switch fam {
	case "4":
		return a.m4
	case "6":
		return a.m6
	}
	return ""
}

// addrFamilies lists the families to render a policy for: "" (any) when
// neither side names addresses, else "4" and/or "6" where both sides can match.
func addrFamilies(src, dst addrRef) []string {
	if src.all && dst.all {
		return []string{""}
	}
	var out []string
	if (src.all || src.m4 != "") && (dst.all || dst.m4 != "") {
		out = append(out, "4")
	}
	if (src.all || src.m6 != "") && (dst.all || dst.m6 != "") {
		out = append(out, "6")
	}
	return out
}

// addrMatch renders an address match. One object uses its own sets;
// several get per-policy sets holding their union (polSet, polSet+"6").
func (r *ruleset) addrMatch(dir string, refs []string, polSet string) (addrRef, error) {
	var objs []*addrObj
	for _, ref := range refs {
		if isAll(ref) {
			return addrRef{all: true}, nil
		}
		o, ok := r.objs.addrs[ref]
		if !ok {
			return addrRef{}, fmt.Errorf("unknown address %q", ref)
		}
		objs = append(objs, o)
	}
	if len(objs) == 0 {
		return addrRef{all: true}, nil
	}
	set, set6 := objs[0].set, objs[0].set6
	if len(objs) > 1 {
		u := &addrObj{}
		for _, o := range objs {
			u.spans = append(u.spans, o.spans...)
			u.spans6 = append(u.spans6, o.spans6...)
			u.fqdns = append(u.fqdns, o.fqdns...)
		}
		comment := "union of " + strings.Join(refs, " ")
		set, set6 = "", ""
		if u.has4() || len(u.spans6) == 0 {
			set = r.names.get("", polSet)
			r.sets = append(r.sets, addrSetDecl(set, u.spans, comment))
		}
		if u.has6() {
			set6 = r.names.get("", polSet+"6")
			r.sets = append(r.sets, addrSetDecl6(set6, u.spans6, comment))
		}
	}
	var a addrRef
	if set != "" {
		a.m4 = "ip " + dir + " @" + set
	}
	if set6 != "" {
		a.m6 = "ip6 " + dir + " @" + set6
	}
	return a, nil
}

// svcRef is a policy's resolved service list.
type svcRef struct {
	any    bool
	pairs  string   // "meta l4proto . th dport @set", "" = no port pairs
	protos []string // protocols matched on all ports
}

// matches returns the layer-4 match(es) for one address family: [""] for
// any, otherwise one match for port pairs and/or one for port-less
// protocols. "icmp" means ICMPv4 and/or ICMPv6 by family.
func (s svcRef) matches(fam string) []string {
	if s.any {
		return []string{""}
	}
	var out []string
	if s.pairs != "" {
		out = append(out, s.pairs)
	}
	var protos []string
	for _, p := range s.protos {
		if p == "icmp" {
			if fam != "6" {
				protos = append(protos, "icmp")
			}
			if fam != "4" {
				protos = append(protos, "ipv6-icmp")
			}
			continue
		}
		protos = append(protos, p)
	}
	if len(protos) == 1 {
		out = append(out, "meta l4proto "+protos[0])
	} else if len(protos) > 1 {
		out = append(out, "meta l4proto { "+strings.Join(protos, ", ")+" }")
	}
	return out
}

// svcMatch resolves a service list, declaring a per-policy set when several
// services carry port pairs.
func (r *ruleset) svcMatch(refs []string, polSet string) (svcRef, error) {
	var objs []*svcObj
	for _, ref := range refs {
		if isAll(ref) {
			return svcRef{any: true}, nil
		}
		o, ok := r.objs.svcs[ref]
		if !ok {
			return svcRef{}, fmt.Errorf("unknown service %q", ref)
		}
		objs = append(objs, o)
	}
	if len(objs) == 0 {
		return svcRef{any: true}, nil
	}
	u := unionSvcs(objs)
	if u.any {
		return svcRef{any: true}, nil
	}
	var out svcRef
	if len(u.pairs) > 0 {
		set := ""
		if len(objs) == 1 {
			set = objs[0].set
		}
		if set == "" {
			set = r.names.get("", polSet)
			r.sets = append(r.sets, svcSetDecl(set, u.pairs, "union of "+strings.Join(refs, " ")))
		}
		out.pairs = "meta l4proto . th dport @" + set
	}
	out.protos = sortedKeys(u.protoOnly)
	return out, nil
}

func (r *ruleset) feedExists(name string) bool {
	return slices.ContainsFunc(r.cfg.Feeds, func(f config.Feed) bool { return f.Name == name })
}

// dnsFilter forces DNS from interfaces with a DNS or web filter profile
// through the box (render.DNSFilterAssignments / WebFilterAssignments):
// port 53 to any address is redirected to the box - to goblind's web filter
// proxy (WebFilterProxyPort) where a web filter applies, else to Unbound -
// and DNS-over-TLS (853) is rejected so clients fall back to plain DNS.
// DNS-over-HTTPS cannot be told apart by port; blocking its well-known
// hosts is the filters' job (UT1 category "doh"). Web-filtered interfaces
// also reach the block page (tcp 80) and the proxy port.
func (r *ruleset) dnsFilter() {
	type filtered struct {
		dev, iface, dns, web string
	}
	byIface := map[string]*filtered{}
	var order []string
	get := func(a render.ProfileAssignment) *filtered {
		f, ok := byIface[a.Interface]
		if !ok {
			f = &filtered{dev: a.Device, iface: a.Interface}
			byIface[a.Interface] = f
			order = append(order, a.Interface)
		}
		return f
	}
	for _, a := range render.DNSFilterAssignments(r.cfg) {
		get(a).dns = a.Profile
	}
	for _, a := range render.WebFilterAssignments(r.cfg) {
		get(a).web = a.Profile
	}
	// application control is answered by the same proxy
	for _, a := range render.AppCtrlAssignments(r.cfg) {
		f := get(a)
		if f.web == "" {
			f.web = "app control " + a.Profile
		}
	}
	slices.Sort(order)
	for _, name := range order {
		f := byIface[name]
		if f.dev == "" {
			continue
		}
		in := "iifname " + quote(f.dev)
		label := "dns filter " + f.dns
		target := ":53"
		if f.web != "" {
			label = "web filter " + f.web
			if strings.HasPrefix(f.web, "app control ") {
				label = f.web
			}
			target = fmt.Sprintf(":%d", WebFilterProxyPort)
			r.input = append(r.input,
				fmt.Sprintf("%s meta l4proto { tcp, udp } th dport %d accept comment %s", in, WebFilterProxyPort, quote(f.iface+": web filter dns")),
				fmt.Sprintf("%s tcp dport 80 accept comment %s", in, quote(f.iface+": block page")))
		}
		r.dnsRedir = append(r.dnsRedir, fmt.Sprintf("%s meta l4proto { tcp, udp } th dport 53 redirect to %s comment %s",
			in, target, quote(label+": "+f.iface)))
		if r.cfg.IPv6Enabled() {
			// The filter knows its clients by IPv4 subnet: DNS over IPv6
			// (to the box or past it) is refused, clients fall back to IPv4.
			v6dns := fmt.Sprintf("%s meta nfproto ipv6 meta l4proto { tcp, udp } th dport 53 reject comment %s", in, quote(label+": dns over ipv4 only"))
			r.input = append(r.input, v6dns)
			r.dnsFwd = append(r.dnsFwd, v6dns)
		}
		r.dnsFwd = append(r.dnsFwd, fmt.Sprintf("%s tcp dport 853 reject with tcp reset comment %s",
			in, quote(label+": no DNS-over-TLS")))
	}
}

// ipsQueues queues every packet (both directions) of connections that an
// IPS policy accepted to that profile's Suricata. bypass accepts packets
// while no Suricata listens (fail-open); fanout spreads flows over queues.
func (r *ruleset) ipsQueues() {
	r.ips = map[string]render.IPSUse{}
	for _, u := range render.IPSProfilesInUse(r.cfg) {
		r.ips[u.Profile.Name] = u
		r.ipsQueue = append(r.ipsQueue, fmt.Sprintf("ct mark & 0x%08x == 0x%08x %s comment %s",
			CtMarkIPSProfile, u.ProfileMark, queueVerdict(u), quote("ips profile "+u.Profile.Name)))
	}
}

func queueVerdict(u render.IPSUse) string {
	return fmt.Sprintf("queue flags bypass,fanout to %d-%d", u.QueueFirst, u.QueueLast)
}

// tailnetRange is Tailscale's CGNAT address range.
const tailnetRange = "100.64.0.0/10"

// tailscale opens the box for the tailnet (task 041): WAN udp 41641 for
// direct (peer-to-peer) connections, the tailscale interface's
// allow_access, and drops tailnet source addresses arriving on LAN-side
// interfaces (spoofing). Tailscale's own rule drops them on every other
// interface, but CGNAT ISPs (e.g. Starlink) put their WAN gateways in
// 100.64.0.0/10, so WAN interfaces are left out. Traffic between the
// tailnet and the LAN is decided by policies on interface "tailscale".
func (r *ruleset) tailscale() {
	t := r.cfg.VPN.Tailscale
	if !t.Enabled {
		return
	}
	dev := quote(config.TailscaleDevice)
	if wan := r.wanMatch(); wan != "" {
		r.input = append(r.input, fmt.Sprintf("iifname %s udp dport %d accept comment %s", wan, tailscaleDirectPort, quote("tailscale: direct connections")))
	}
	adminPort := r.cfg.System.AdminPort
	if adminPort == 0 {
		adminPort = DefaultPort
	}
	for _, a := range t.AllowAccess {
		switch a {
		case "ping":
			r.input = append(r.input, fmt.Sprintf("iifname %s icmp type echo-request accept comment %s", dev, quote("tailscale: ping")))
		case "https":
			r.input = append(r.input, fmt.Sprintf("iifname %s tcp dport %d accept comment %s", dev, adminPort, quote("tailscale: https admin")))
		case "ssh":
			r.input = append(r.input, fmt.Sprintf("iifname %s tcp dport %d accept comment %s", dev, sshPort, quote("tailscale: ssh admin")))
		}
	}
	skip := []string{dev}
	for _, i := range r.cfg.Interfaces {
		if i.Role == "wan" {
			if d, ok := r.devs[i.Name]; ok {
				skip = append(skip, quote(d))
			}
		}
	}
	wanParent := map[string]bool{}
	for _, i := range r.cfg.Interfaces {
		wanParent[i.Name] = i.Role == "wan"
	}
	for _, v := range r.cfg.VLANs {
		if wanParent[v.Parent] {
			if d, ok := r.devs[v.Name]; ok {
				skip = append(skip, quote(d))
			}
		}
	}
	r.spoof = append(r.spoof, fmt.Sprintf("iifname != { %s } ip saddr %s drop comment %s",
		strings.Join(skip, ", "), tailnetRange, quote("tailscale: no tailnet addresses from local networks")))
}

// wireguard opens each enabled WireGuard server's port on WAN and lets its
// clients use the box's DNS plus the interface's allow_access (task 036).
// Traffic between the tunnel and other networks is decided by policies on
// the interface (its name).
func (r *ruleset) wireguard() {
	wan := r.wanMatch()
	adminPort := r.cfg.System.AdminPort
	if adminPort == 0 {
		adminPort = DefaultPort
	}
	for _, w := range r.cfg.VPN.WireGuard {
		if !w.Enabled {
			continue
		}
		port := w.ListenPort
		if port == 0 {
			port = config.DefaultWireGuardPort
		}
		if wan != "" {
			r.input = append(r.input, fmt.Sprintf("iifname %s udp dport %d accept comment %s", wan, port, quote("wireguard "+w.Name)))
		}
		in := "iifname " + quote(w.Name)
		r.input = append(r.input, fmt.Sprintf("%s meta l4proto { tcp, udp } th dport 53 accept comment %s", in, quote(w.Name+": dns")))
		for _, a := range w.AllowAccess {
			switch a {
			case "ping":
				r.input = append(r.input, fmt.Sprintf("%s icmp type echo-request accept comment %s", in, quote(w.Name+": ping")))
			case "https":
				r.input = append(r.input, fmt.Sprintf("%s tcp dport %d accept comment %s", in, adminPort, quote(w.Name+": https admin")))
			case "ssh":
				r.input = append(r.input, fmt.Sprintf("%s tcp dport %d accept comment %s", in, sshPort, quote(w.Name+": ssh admin")))
			}
		}
	}
}
