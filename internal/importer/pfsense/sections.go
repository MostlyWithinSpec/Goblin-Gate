package pfsense

import (
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// schedules converts weekday schedules. pfSense counts days 1 = Monday ...
// 7 = Sunday and writes hours as "8:00-17:00"; an end of 23:59 means the
// end of the day. Ranges on calendar dates are skipped.
func (im *importer) schedules() {
	days := map[string]string{"1": "mon", "2": "tue", "3": "wed", "4": "thu", "5": "fri", "6": "sat", "7": "sun"}
	for _, s := range im.pf.Schedules {
		sc := config.Schedule{Name: slug(s.Name, "schedule"), Comment: s.Descr}
		var notes []string
		for _, tr := range s.TimeRanges {
			if tr.Position == "" {
				notes = append(notes, "a range on calendar dates ("+tr.Month+"/"+tr.Day+") was left out")
				continue
			}
			var ds []string
			seen := map[string]bool{}
			for _, p := range strings.Split(tr.Position, ",") {
				if d, ok := days[strings.TrimSpace(p)]; ok && !seen[d] {
					ds = append(ds, d)
					seen[d] = true
				}
			}
			start, end, ok := strings.Cut(tr.Hour, "-")
			st, err1 := clock(start)
			en, err2 := clock(end)
			if !ok || err1 != nil || err2 != nil || len(ds) == 0 {
				notes = append(notes, "range "+tr.Position+" "+tr.Hour+" not understood")
				continue
			}
			if en == "23:59" {
				en = "24:00"
			}
			if st == en {
				notes = append(notes, "an empty range "+tr.Hour+" was left out")
				continue
			}
			sc.Ranges = append(sc.Ranges, config.ScheduleRange{Days: ds, Start: st, End: en})
		}
		if len(sc.Ranges) == 0 {
			im.add("schedules", s.Name, Skipped, "nothing convertible: %s", strings.Join(notes, "; "))
			continue
		}
		im.cfg.Schedules = append(im.cfg.Schedules, sc)
		im.schedOK[s.Name] = true
		if len(notes) > 0 {
			im.add("schedules", s.Name, Approximated, "%d range(s); %s", len(sc.Ranges), strings.Join(notes, "; "))
		} else {
			im.add("schedules", s.Name, Converted, "%d range(s), times in the system time zone", len(sc.Ranges))
		}
	}
}

// clock normalises "8:00" to "08:00".
func clock(s string) (string, error) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if !ok || err1 != nil || err2 != nil || hh < 0 || hh > 24 || mm < 0 || mm > 59 {
		return "", fmt.Errorf("bad time %q", s)
	}
	return fmt.Sprintf("%02d:%02d", hh, mm), nil
}

// dhcp converts the DHCPv4 servers (ranges, static mappings, DNS, gateway,
// lease time). DHCPv6 is replaced by SLAAC (router advertisements).
func (im *importer) dhcp() {
	for _, d := range im.pf.DHCPD.Items {
		key := d.XMLName.Local
		if d.Enable == nil {
			continue
		}
		name, ok := im.ifName(key)
		if !ok {
			im.add("dhcp", key, Skipped, "interface %q is not converted", key)
			continue
		}
		from, err1 := netip.ParseAddr(d.Range.From)
		to, err2 := netip.ParseAddr(d.Range.To)
		if err1 != nil || err2 != nil {
			im.add("dhcp", key, Skipped, "no valid address range")
			continue
		}
		s := config.DHCPServer{Interface: name, RangeStart: from, RangeEnd: to}
		if n, err := strconv.Atoi(d.DefaultLeaseTime); err == nil && n > 0 {
			s.LeaseSeconds = n
		}
		for _, v := range d.DNSServers {
			if a, err := netip.ParseAddr(v); err == nil {
				s.DNS = append(s.DNS, a)
			}
		}
		if g, err := netip.ParseAddr(d.Gateway); err == nil {
			s.Gateway = &g
		}
		var notes []string
		for _, m := range d.StaticMaps {
			ip, err := netip.ParseAddr(m.IPAddr)
			if err != nil || m.MAC == "" {
				notes = append(notes, "static mapping "+m.MAC+" without an IP address (pfSense only registers its name) was left out")
				continue
			}
			host := m.Hostname
			if host == "" {
				host = slug(m.Descr, "")
			}
			s.StaticLeases = append(s.StaticLeases, config.DHCPStaticLease{MAC: strings.ToLower(m.MAC), IP: ip, Hostname: host})
		}
		if d.Domain != "" && d.Domain != im.cfg.System.Domain {
			notes = append(notes, "its own domain "+d.Domain+" is not supported (one LAN domain: DNS → local domain)")
		}
		im.cfg.DHCP = append(im.cfg.DHCP, s)
		detail := fmt.Sprintf("%s-%s, %d static mapping(s)", from, to, len(s.StaticLeases))
		if len(notes) > 0 {
			im.add("dhcp", key, Approximated, "%s; %s", detail, strings.Join(notes, "; "))
		} else {
			im.add("dhcp", key, Converted, "%s", detail)
		}
	}
	for _, d := range im.pf.DHCPDv6.Items {
		if d.Enable != nil || d.RAMode != "" {
			im.add("dhcp", "DHCPv6 "+d.XMLName.Local, Approximated, "DHCPv6 server / router advertisements (%s) → SLAAC with router advertisements on IPv6 interfaces; there is no DHCPv6 server yet", d.RAMode)
		}
	}
}

// dns converts the Unbound resolver: forwarding, DNSSEC, host and domain
// overrides.
func (im *importer) dns() {
	u, dns := im.pf.Unbound, &im.cfg.DNS
	if im.pf.DNSMasq != nil && im.pf.DNSMasq.Enable != nil {
		im.add("dns", "DNS forwarder (dnsmasq)", Approximated, "Goblin Gate uses Unbound; its host overrides are not read from dnsmasq")
	}
	if u.Enable == nil {
		im.add("dns", "resolver", Approximated, "the DNS resolver was off in pfSense; Goblin Gate always resolves for its LAN")
	}
	dns.DNSSEC = u.DNSSEC != nil
	dns.Forwarders = nil // pfSense decides: forwarding to its DNS servers, or resolving itself
	if len(im.forwardTo) > 0 {
		dns.Forwarders = im.forwardTo
		im.add("dns", "forwarding", Converted, "forwarding to %d server(s) from the resolver's forwarding entries", len(dns.Forwarders))
	} else if u.Forwarding != nil {
		for _, a := range im.cfg.System.DNSServers {
			if a.Is4() {
				dns.Forwarders = append(dns.Forwarders, a)
			}
		}
		im.add("dns", "forwarding", Converted, "forwarding to %d server(s) from System → DNS servers", len(dns.Forwarders))
	} else {
		im.add("dns", "resolving", Converted, "resolving itself (no forwarders)")
	}
	if im.cfg.System.Domain != "" {
		dns.LocalDomain = im.cfg.System.Domain
	}
	for _, h := range u.Hosts {
		ip, err := netip.ParseAddr(h.IP)
		fqdn := strings.Trim(strings.ToLower(h.Host+"."+h.Domain), ".")
		if err != nil || fqdn == "" {
			im.add("dns", fqdn, Skipped, "host override without a single valid address (%s)", h.IP)
			continue
		}
		if dns.LocalRecords == nil {
			dns.LocalRecords = map[string]string{}
		}
		dns.LocalRecords[fqdn] = ip.String()
		for _, a := range h.Aliases.Items {
			if alias := strings.Trim(strings.ToLower(a.Host+"."+a.Domain), "."); alias != "" {
				dns.LocalRecords[alias] = ip.String()
			}
		}
		im.add("dns", fqdn, Converted, "host override → %s", ip)
	}
	byDomain := map[string][]netip.Addr{}
	for _, o := range u.DomainOverrides {
		ip := strings.SplitN(o.IP, "@", 2)[0] // "10.0.0.53@5353": ports are not supported
		a, err := netip.ParseAddr(ip)
		if err != nil {
			im.add("dns", o.Domain, Skipped, "domain override server %q not understood", o.IP)
			continue
		}
		if strings.Contains(o.IP, "@") {
			im.add("dns", o.Domain, Approximated, "the server's port in %q is ignored (port 53 is used)", o.IP)
		}
		byDomain[strings.ToLower(o.Domain)] = append(byDomain[strings.ToLower(o.Domain)], a)
	}
	for _, d := range sortedKeys(byDomain) {
		dns.DomainForwards = append(dns.DomainForwards, config.DomainForward{Domain: d, Servers: byDomain[d]})
		im.add("dns", d, Converted, "domain override → %d server(s)", len(byDomain[d]))
	}
}

// gateways converts pfSense gateways. With one IPv4 gateway nothing more is
// needed (the WAN's gateway is part of its addressing). With several, or
// when gateway groups, static routes or rules with a gateway exist, they
// become multi-WAN gateways (task 049): groups keep their tiers, routes and
// policy routing follow.
func (im *importer) gateways() {
	type gw struct {
		pfName, name, iface, addr, monitor string
		monitorOff                         bool
	}
	var v4 []gw
	used := map[string]bool{}
	for _, g := range im.pf.Gateways.Items {
		if g.Disabled != nil {
			im.add("gateways", g.Name, Skipped, "disabled in pfSense: not imported")
			continue
		}
		if g.IPProtocol == "inet6" {
			im.add("gateways", g.Name, Converted, "IPv6 gateway: IPv6 follows router advertisements and the default route")
			continue
		}
		iface, ok := im.ifName(g.Interface)
		if !ok {
			im.add("gateways", g.Name, Skipped, "its interface %q (a VPN or unassigned) is not converted", g.Interface)
			continue
		}
		addr := g.Gateway
		if addr == "dynamic" || addr == "" {
			addr = "dhcp"
		}
		v4 = append(v4, gw{pfName: g.Name, name: unique(used, slug(g.Name, "gw")), iface: iface, addr: addr, monitor: g.Monitor, monitorOff: g.MonitorOff != nil})
	}
	ruleGW := false
	for _, r := range im.pf.Filter {
		ruleGW = ruleGW || r.Gateway != ""
	}
	var routes []pfRoute
	for _, r := range im.pf.Routes {
		if r.Disabled != nil {
			im.add("routes", r.Network, Skipped, "disabled in pfSense: not imported")
			continue
		}
		routes = append(routes, r)
	}
	multi := len(v4) > 1 || len(im.pf.Gateways.Groups) > 0 || len(routes) > 0 || ruleGW
	if !multi {
		for _, g := range v4 {
			im.add("gateways", g.pfName, Converted, "%s on %s (single WAN: part of the interface)", g.addr, g.iface)
		}
		return
	}
	names := map[string]string{} // pfSense gateway/group name -> ours
	perIface := map[string]bool{}
	for _, g := range v4 {
		if perIface[g.iface] {
			im.add("gateways", g.pfName, Skipped, "a second gateway on %s is not supported", g.iface)
			continue
		}
		perIface[g.iface] = true
		cg := config.Gateway{Name: g.name, Interface: g.iface, Address: g.addr}
		if g.monitorOff {
			cg.Monitor.Disabled = true
		} else if a, err := netip.ParseAddr(g.monitor); err == nil && a.Is4() {
			cg.Monitor.IP = a.String()
		}
		im.cfg.Gateways = append(im.cfg.Gateways, cg)
		names[g.pfName] = g.name
		im.add("gateways", g.pfName, Converted, "multi-WAN gateway %q: %s on %s", g.name, g.addr, g.iface)
	}
	for _, gg := range im.pf.Gateways.Groups {
		grp := config.GatewayGroup{Name: unique(used, slug(gg.Name, "group"))}
		var notes []string
		for _, it := range gg.Items {
			f := strings.Split(it, "|")
			ours, ok := names[f[0]]
			if !ok {
				notes = append(notes, f[0]+" left out")
				continue
			}
			tier := 1
			if len(f) > 1 {
				if n, err := strconv.Atoi(f[1]); err == nil && n >= 1 && n <= 5 {
					tier = n
				}
			}
			grp.Members = append(grp.Members, config.GroupMember{Gateway: ours, Tier: tier})
		}
		if len(grp.Members) == 0 {
			im.add("gateways", gg.Name, Skipped, "no member gateway was converted")
			continue
		}
		im.cfg.GatewayGroups = append(im.cfg.GatewayGroups, grp)
		names[gg.Name] = grp.Name
		if len(notes) > 0 {
			im.add("gateways", gg.Name, Approximated, "gateway group %q with %d member(s); %s", grp.Name, len(grp.Members), strings.Join(notes, "; "))
		} else {
			im.add("gateways", gg.Name, Converted, "gateway group %q with %d member(s)", grp.Name, len(grp.Members))
		}
	}
	if d := im.pf.Gateways.DefaultGW4; d != "" {
		if ours, ok := names[d]; ok {
			im.cfg.DefaultGateway = ours
		}
	}
	for _, r := range routes {
		dst, err := netip.ParsePrefix(r.Network)
		ours, ok := names[r.Gateway]
		_, isGroup := im.cfg.GatewayTarget(ours)
		if err != nil || !dst.Addr().Is4() || !ok || isGroup {
			im.add("routes", r.Network, Skipped, "static route via %s: needs an IPv4 subnet and a converted gateway (not a group)", r.Gateway)
			continue
		}
		im.cfg.Routes = append(im.cfg.Routes, config.Route{Destination: dst.Masked(), Gateway: ours, Comment: r.Descr})
		im.add("routes", r.Network, Converted, "via %s", ours)
	}
	im.gatewayNames = names
}

// upnp converts the miniupnpd package settings.
func (im *importer) upnp() {
	c := im.pf.Packages.MiniUPnPd.Config
	if c == nil {
		return
	}
	if c.Enable == nil {
		im.add("upnp", "UPnP", Converted, "off in pfSense: left off")
		return
	}
	u := config.UPnP{Enabled: true}
	var notes []string
	for _, key := range strings.Split(c.IfaceArray, ",") {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if name, ok := im.ifName(key); ok && !im.isWAN(name) {
			u.Interfaces = append(u.Interfaces, name)
		} else {
			notes = append(notes, "interface "+key+" left out")
		}
	}
	if c.EnableNATPMP == nil {
		off := false
		u.NATPMP = &off
	}
	u.STUN = c.EnableSTUN != nil
	for _, row := range c.Rows {
		f := strings.Fields(row.PermUser)
		if len(f) != 4 {
			continue
		}
		if f[0] != "allow" {
			notes = append(notes, "deny entry \""+row.PermUser+"\" left out (everything not allowed is denied)")
			continue
		}
		if f[1] != f[3] {
			notes = append(notes, "\""+row.PermUser+"\": external ports follow the internal ones ("+f[3]+")")
		}
		u.Allow = append(u.Allow, config.UPnPAllow{Address: f[2], Ports: f[3]})
	}
	if len(u.Interfaces) == 0 {
		im.add("upnp", "UPnP", Skipped, "no LAN-side interface left to serve")
		return
	}
	im.cfg.UPnP = u
	detail := fmt.Sprintf("on %s, %d permission rule(s)", strings.Join(u.Interfaces, ", "), len(u.Allow))
	if len(notes) > 0 {
		im.add("upnp", "UPnP", Approximated, "%s; %s", detail, strings.Join(notes, "; "))
	} else {
		im.add("upnp", "UPnP", Converted, "%s", detail)
	}
}

// ids turns Suricata/Snort instances into one IPS profile on the policies
// leaving the monitored internal interfaces (or all internal ones when only
// the WAN was monitored).
func (im *importer) ids() {
	type inst struct {
		engine string
		r      pfIDSRule
	}
	var all []inst
	for _, r := range im.pf.Packages.Suricata {
		all = append(all, inst{"Suricata", r})
	}
	for _, r := range im.pf.Packages.Snort {
		all = append(all, inst{"Snort", r})
	}
	monitored := map[string]bool{}
	block, any := false, false
	for _, x := range all {
		if x.r.Enable != "on" {
			im.add("ips", x.engine+" "+x.r.Interface, Converted, "disabled in pfSense: not imported")
			continue
		}
		any = true
		if name, ok := im.ifName(x.r.Interface); ok {
			monitored[name] = true
		}
		if x.r.IPSMode == "ips_mode_inline" || x.r.BlockOffenders == "on" || x.r.BlockOffenders7 == "on" {
			block = true
		}
	}
	if !any {
		return
	}
	mode := "detect"
	if block {
		mode = "prevent"
	}
	prof := config.IPSProfile{Name: "imported", Comment: "from pfSense", Mode: mode}
	im.cfg.Profiles.IPS = append(im.cfg.Profiles.IPS, prof)
	wanOnly := true
	for name := range monitored {
		wanOnly = wanOnly && im.isWAN(name)
	}
	n := 0
	for i, p := range im.cfg.Policies {
		if p.Action != "accept" {
			continue
		}
		for _, s := range p.SrcIntf {
			if monitored[s] || wanOnly && !im.isWAN(s) {
				im.cfg.Policies[i].IPSProfile = prof.Name
				n++
				break
			}
		}
	}
	im.add("ips", "IDS/IPS", Approximated, "IPS profile \"imported\" (%s, Emerging Threats Open rules) on %d allow policy(ies); pfSense's rule selection, suppressions and blocking of offenders are not imported", mode, n)
}

// pfBlocker converts pfBlockerNG lists: IP lists → IP threat feeds (on
// allow policies for outbound lists, on the WAN for inbound ones), DNSBL →
// domain feeds in a DNS filter profile.
func (im *importer) pfBlocker() {
	var ipFeeds []string
	for _, l := range im.pf.Packages.PfBlockerIPv4 {
		if l.Action == "Disabled" || strings.HasPrefix(l.Action, "Permit") || l.Action == "Match_Both" {
			im.add("pfblockerng", l.AliasName, Skipped, "action %s is not converted", l.Action)
			continue
		}
		for _, row := range l.Rows {
			if row.State != "Enabled" || row.URL == "" || !strings.HasPrefix(row.URL, "http") {
				continue
			}
			name := unique(im.names, slug(l.AliasName+"-"+row.Header, "list")+"-ip")
			f := config.Feed{Name: name, Type: "ip", URL: row.URL, RefreshMinutes: 1440, Enabled: true,
				BlockWAN: l.Action == "Deny_Inbound" || l.Action == "Deny_Both"}
			im.cfg.Feeds = append(im.cfg.Feeds, f)
			if l.Action != "Deny_Inbound" {
				ipFeeds = append(ipFeeds, name)
			}
			im.add("pfblockerng", row.Header, Approximated, "IP list → threat feed %q (%s)", name, l.Action)
		}
	}
	if len(ipFeeds) > 0 {
		n := 0
		for i, p := range im.cfg.Policies {
			if p.Action == "accept" && p.NAT {
				im.cfg.Policies[i].ThreatFeeds = append(im.cfg.Policies[i].ThreatFeeds, ipFeeds...)
				n++
			}
		}
		im.add("pfblockerng", "outbound IP lists", Approximated, "%d feed(s) block destinations on %d internet policy(ies)", len(ipFeeds), n)
	}
	var domainFeeds []string
	for _, l := range im.pf.Packages.PfBlockerDNSBL {
		if l.Action == "Disabled" {
			continue
		}
		for _, row := range l.Rows {
			if row.State != "Enabled" || row.URL == "" || !strings.HasPrefix(row.URL, "http") {
				continue
			}
			name := unique(im.names, slug(l.AliasName+"-"+row.Header, "list")+"-dns")
			im.cfg.Feeds = append(im.cfg.Feeds, config.Feed{Name: name, Type: "domain", URL: row.URL, RefreshMinutes: 1440, Enabled: true})
			domainFeeds = append(domainFeeds, name)
			im.add("pfblockerng", row.Header, Approximated, "DNSBL list → domain feed %q", name)
		}
	}
	if len(domainFeeds) > 0 {
		prof := config.DNSFilterProfile{Name: "pfblockerng", Comment: "DNSBL lists from pfSense", BlockFeeds: domainFeeds}
		im.cfg.Profiles.DNSFilter = append(im.cfg.Profiles.DNSFilter, prof)
		n := 0
		for i, p := range im.cfg.Policies {
			if p.Action == "accept" && p.NAT && p.DNSFilterProfile == "" {
				im.cfg.Policies[i].DNSFilterProfile = prof.Name
				n++
			}
		}
		im.add("pfblockerng", "DNSBL", Approximated, "DNS filter profile \"pfblockerng\" with %d list(s) on %d internet policy(ies)", len(domainFeeds), n)
	}
}

// unsupported lists what is not converted, with what to use instead.
func (im *importer) unsupported() {
	pf := im.pf
	for _, s := range pf.OpenVPN.Servers {
		im.add("vpn", "OpenVPN server "+s.label(), Skipped, "OpenVPN is not supported: use WireGuard (VPN → WireGuard) or Tailscale for remote access")
	}
	for _, c := range pf.OpenVPN.Clients {
		im.add("vpn", "OpenVPN client "+c.label(), Skipped, "OpenVPN clients are not supported: if the provider offers WireGuard, add it as a WireGuard client (VPN providers) and route traffic to it with a policy gateway")
	}
	for _, w := range pf.Packages.WireGuard {
		im.add("vpn", "WireGuard "+w.label(), Skipped, "recreate it under VPN → WireGuard (keys are never imported)")
	}
	if pf.Packages.Tailscale != nil {
		im.add("vpn", "Tailscale", Skipped, "turn Tailscale on under VPN → Tailscale and log in again")
	}
	if n := len(pf.CAs) + len(pf.Certs); n > 0 {
		im.add("system", "certificates", Skipped, "%d CA(s)/certificate(s) and their private keys are never imported", n)
	}
	for _, d := range pf.DynDNS {
		im.add("system", "dynamic DNS "+d.label(), Skipped, "dynamic DNS is not supported yet")
	}
	for _, v := range pf.VirtualIPs {
		im.add("interfaces", "virtual IP "+v.label(), Skipped, "virtual IPs (CARP/IP alias) are not supported")
	}
	for _, g := range pf.IfGroups {
		im.add("interfaces", "interface group "+g.label(), Skipped, "interface groups are not supported: list the interfaces in each policy")
	}
	if len(pf.ShaperQueue) > 0 {
		im.add("system", "traffic shaper", Skipped, "traffic shaping is not supported yet")
	}
	known := map[string]bool{"miniupnpd": true, "pfBlockerNG": true, "pfBlockerNG-devel": true, "suricata": true, "snort": true,
		"WireGuard": true, "Tailscale": true}
	var other []string
	for _, p := range pf.Packages.Packages {
		if !known[p.Name] {
			other = append(other, p.Name)
		}
	}
	sort.Strings(other)
	for _, p := range other {
		im.add("packages", p, Skipped, "package %s has no counterpart that the importer knows", p)
	}
}
