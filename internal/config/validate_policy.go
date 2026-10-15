package config

import (
	"fmt"
	"net/netip"
)

func (v *validator) checkPolicies() {
	c := v.cfg
	intfRef := toSet(nameList(c.Interfaces, func(x Interface) string { return x.Name }),
		nameList(c.VLANs, func(x VLAN) string { return x.Name }))
	if c.VPN.Tailscale.Enabled {
		intfRef[TailscaleInterface] = true
	}
	for _, w := range c.VPN.WireGuard {
		if w.Enabled {
			intfRef[w.Name] = true
		}
	}
	addrRef := toSet(nameList(c.Addresses, func(x Address) string { return x.Name }),
		nameList(c.AddressGroups, func(x AddressGroup) string { return x.Name }))
	svcRef := toSet(nameList(c.Services, func(x Service) string { return x.Name }),
		nameList(c.ServiceGroups, func(x ServiceGroup) string { return x.Name }))
	feeds := toSet(nameList(c.Feeds, func(x Feed) string { return x.Name }))
	ips := toSet(nameList(c.Profiles.IPS, func(x IPSProfile) string { return x.Name }))
	wf := toSet(nameList(c.Profiles.WebFilter, func(x WebFilterProfile) string { return x.Name }))
	dnf := toSet(nameList(c.Profiles.DNSFilter, func(x DNSFilterProfile) string { return x.Name }))
	app := toSet(nameList(c.Profiles.AppCtrl, func(x AppCtrlProfile) string { return x.Name }))

	idSeen := make(map[uint32]int, len(c.Policies))
	for i, pol := range c.Policies {
		p := fmt.Sprintf("policies[%d]", i)
		if j, dup := idSeen[pol.ID]; dup {
			v.add(p+".id", "duplicate policy id %d (first used at policies[%d])", pol.ID, j)
		} else {
			idSeen[pol.ID] = i
		}
		if !oneOf(pol.Action, "accept", "deny") {
			v.add(p+".action", "unknown action %q: want accept or deny", pol.Action)
		}
		if !oneOf(pol.Log, "", "off", "all", "deny") {
			v.add(p+".log", "unknown log mode %q: want off, all or deny", pol.Log)
		}
		// Wildcards ("all"/"any") are accepted everywhere; Default() uses "all".
		refs := func(field string, list []string, valid map[string]bool) {
			for j, r := range list {
				if !isWildcard(r) && !valid[r] {
					v.add(fmt.Sprintf("%s.%s[%d]", p, field, j), "unknown %s reference %q", field, r)
				}
			}
		}
		refs("src_intf", pol.SrcIntf, intfRef)
		refs("dst_intf", pol.DstIntf, intfRef)
		refs("src_addr", pol.SrcAddr, addrRef)
		refs("dst_addr", pol.DstAddr, addrRef)
		refs("service", pol.Service, svcRef)
		one := func(field, ref string, valid map[string]bool) {
			if ref != "" && !valid[ref] {
				v.add(p+"."+field, "unknown %s reference %q", field, ref)
			}
		}
		one("ips_profile", pol.IPSProfile, ips)
		one("webfilter_profile", pol.WebFilterProfile, wf)
		one("dnsfilter_profile", pol.DNSFilterProfile, dnf)
		one("appctrl_profile", pol.AppCtrlProfile, app)
		for j, f := range pol.ThreatFeeds {
			if !feeds[f] {
				v.add(fmt.Sprintf("%s.threat_feeds[%d]", p, j), "unknown threat_feeds reference %q", f)
			}
		}
	}
}

// checkPortForwards: wan-role interface (a VLAN inherits its parent's role),
// one external port or range, an IPv4 target, and an optional internal port
// that is either a single port or exactly the external range.
func (v *validator) checkPortForwards() {
	for i, pf := range v.cfg.NAT.PortForwards {
		p := fmt.Sprintf("nat.port_forwards[%d]", i)
		if role, ok := v.netifRole(pf.InIntf); !ok {
			v.add(p+".in_intf", "unknown interface %q", pf.InIntf)
		} else if role != "wan" {
			v.add(p+".in_intf", "interface %q has role %q, want wan", pf.InIntf, role)
		}
		if pf.Protocol != "tcp" && pf.Protocol != "udp" {
			v.add(p+".protocol", "unknown protocol %q: want tcp or udp", pf.Protocol)
		}
		extLo, extHi, extOK := portOrRange(pf.ExtPort)
		if !extOK {
			v.add(p+".ext_port", "invalid port %q: want 1-65535 or a \"low-high\" range", pf.ExtPort)
		}
		if ip, err := netip.ParseAddr(pf.ToAddr); err != nil || !ip.Unmap().Is4() {
			v.add(p+".to_addr", "invalid IPv4 address %q", pf.ToAddr)
		}
		if pf.ToPort == "" {
			continue // keep the external port
		}
		lo, hi, ok := portOrRange(pf.ToPort)
		switch {
		case !ok:
			v.add(p+".to_port", "invalid port %q: want 1-65535 or a \"low-high\" range", pf.ToPort)
		case lo != hi && extOK && (lo != extLo || hi != extHi):
			v.add(p+".to_port", "port range %q must equal ext_port %q (or use a single port)", pf.ToPort, pf.ExtPort)
		}
	}
}
