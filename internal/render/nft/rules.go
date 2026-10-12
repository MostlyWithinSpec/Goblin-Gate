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
	}
	var all []ifc
	wan := map[string]bool{}
	for _, i := range r.cfg.Interfaces {
		wan[i.Name] = i.Role == "wan"
		all = append(all, ifc{i.Name, i.Mode, i.Role == "wan", i.Enabled, i.AllowAccess})
	}
	// VLANs have no role of their own; a VLAN on a wan parent is wan.
	for _, v := range r.cfg.VLANs {
		all = append(all, ifc{v.Name, v.Mode, wan[v.Parent], v.Enabled, v.AllowAccess})
	}
	sortBy(all, func(i ifc) string { return i.name })

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
			case "https":
				r.input = append(r.input, fmt.Sprintf("%s tcp dport %d accept comment %s", in, adminPort, quote(i.name+": https admin")))
			case "ssh":
				r.input = append(r.input, fmt.Sprintf("%s tcp dport %d accept comment %s", in, sshPort, quote(i.name+": ssh admin")))
			}
		}
		if i.mode == "dhcp" {
			r.input = append(r.input, fmt.Sprintf("%s udp sport 67 udp dport 68 accept comment %s", in, quote(i.name+": dhcp client")))
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
			r.forward = append(r.forward, fmt.Sprintf("# policy %d %s is disabled", p.ID, quote(p.Name)))
			continue
		}
		rules, err := r.policy(p, counter)
		if err != nil {
			return fmt.Errorf("policy %d: %w", p.ID, err)
		}
		r.forward = append(r.forward, rules...)
	}
	return nil
}

func (r *ruleset) policy(p config.Policy, counter string) ([]string, error) {
	if p.Schedule != "" {
		return nil, fmt.Errorf("schedules are not supported yet")
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
	src, err := r.addrMatch("ip saddr", p.SrcAddr, fmt.Sprintf("policy_%d_src", p.ID))
	if err != nil {
		return nil, fmt.Errorf("src_addr: %w", err)
	}
	dst, err := r.addrMatch("ip daddr", p.DstAddr, fmt.Sprintf("policy_%d_dst", p.ID))
	if err != nil {
		return nil, fmt.Errorf("dst_addr: %w", err)
	}
	l4, err := r.svcMatch(p.Service, fmt.Sprintf("policy_%d_svc", p.ID))
	if err != nil {
		return nil, fmt.Errorf("service: %w", err)
	}
	for _, f := range p.ThreatFeeds {
		if !r.feedExists(f) {
			return nil, fmt.Errorf("unknown threat feed %q", f)
		}
	}
	// Threat feeds: traffic this policy would accept is dropped first when
	// its source or destination is in one of the policy's IP feeds. Domain
	// and url feeds are handled by the DNS and web filters; disabled feeds
	// have no set and are skipped.
	var feedDrops []string
	if p.Action == "accept" {
		for _, f := range p.ThreatFeeds {
			set, ok := r.feedSet[f]
			if !ok {
				continue
			}
			drop := fmt.Sprintf("counter name %s drop comment %s", quote(r.feedHit[f]), quote(p.Name+": feed "+f))
			for _, m := range l4 {
				for _, dir := range []string{"saddr", "daddr"} {
					feedDrops = append(feedDrops, joinNonEmpty(iif, oif, src, dst, m, "ip "+dir+" @"+set, drop))
				}
			}
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

	// A service list mixing port-less protocols (icmp, tcp on all ports) with
	// port pairs needs one rule per kind; both share the policy counter.
	rules := feedDrops
	for _, m := range l4 {
		rules = append(rules, joinNonEmpty(iif, oif, src, dst, m,
			"counter name "+quote(counter), log, marks, verdict, "comment "+quote(p.Name)))
	}
	return rules, nil
}

// addrMatch renders an address match. One object uses its own set; several
// get a per-policy set holding their union.
func (r *ruleset) addrMatch(key string, refs []string, polSet string) (string, error) {
	var objs []*addrObj
	for _, ref := range refs {
		if isAll(ref) {
			return "", nil
		}
		o, ok := r.objs.addrs[ref]
		if !ok {
			return "", fmt.Errorf("unknown address %q", ref)
		}
		objs = append(objs, o)
	}
	switch len(objs) {
	case 0:
		return "", nil
	case 1:
		return key + " @" + objs[0].set, nil
	}
	var spans []span
	for _, o := range objs {
		spans = append(spans, o.spans...)
	}
	name := r.names.get("", polSet)
	r.sets = append(r.sets, addrSetDecl(name, spans, "union of "+strings.Join(refs, " ")))
	return key + " @" + name, nil
}

// svcMatch returns the layer-4 match(es) for a service list: [""] for any,
// otherwise one match for port pairs and/or one for port-less protocols.
func (r *ruleset) svcMatch(refs []string, polSet string) ([]string, error) {
	var objs []*svcObj
	for _, ref := range refs {
		if isAll(ref) {
			return []string{""}, nil
		}
		o, ok := r.objs.svcs[ref]
		if !ok {
			return nil, fmt.Errorf("unknown service %q", ref)
		}
		objs = append(objs, o)
	}
	if len(objs) == 0 {
		return []string{""}, nil
	}
	u := unionSvcs(objs)
	if u.any {
		return []string{""}, nil
	}
	var out []string
	if len(u.pairs) > 0 {
		set := ""
		if len(objs) == 1 {
			set = objs[0].set
		}
		if set == "" {
			set = r.names.get("", polSet)
			r.sets = append(r.sets, svcSetDecl(set, u.pairs, "union of "+strings.Join(refs, " ")))
		}
		out = append(out, "meta l4proto . th dport @"+set)
	}
	if protos := sortedKeys(u.protoOnly); len(protos) == 1 {
		out = append(out, "meta l4proto "+protos[0])
	} else if len(protos) > 1 {
		out = append(out, "meta l4proto { "+strings.Join(protos, ", ")+" }")
	}
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
