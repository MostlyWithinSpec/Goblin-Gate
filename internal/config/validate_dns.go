package config

import (
	"fmt"
	"net/netip"
	"strings"
)

// checkDNS validates the resolver settings: local records, the local domain
// and split-DNS forwards (task 055).
func (v *validator) checkDNS() {
	d := v.cfg.DNS
	for name, ip := range d.LocalRecords {
		p := "dns.local_records." + name
		if !validHostname(strings.TrimSuffix(name, ".")) {
			v.add(p, "invalid name %q", name)
		}
		if a, err := netip.ParseAddr(ip); err != nil || !a.Is4() {
			v.add(p, "invalid IPv4 address %q", ip)
		}
	}
	if d.LocalDomain != "" && !validHostname(d.LocalDomain) {
		v.add("dns.local_domain", "invalid domain %q", d.LocalDomain)
	}
	seen := map[string]bool{}
	for i, f := range d.DomainForwards {
		p := fmt.Sprintf("dns.domain_forwards[%d]", i)
		dom := strings.ToLower(strings.TrimSuffix(f.Domain, "."))
		switch {
		case !validHostname(dom):
			v.add(p+".domain", "invalid domain %q (use dns.forwarders for all other names)", f.Domain)
		case seen[dom]:
			v.add(p+".domain", "domain %q is forwarded twice", f.Domain)
		case dom == strings.ToLower(v.cfg.EffectiveLocalDomain()):
			v.add(p+".domain", "%q is the local domain, answered by the box itself", f.Domain)
		}
		seen[dom] = true
		if len(f.Servers) == 0 {
			v.add(p+".servers", "at least one DNS server is required")
		}
		for j, s := range f.Servers {
			if !s.Is4() {
				v.add(fmt.Sprintf("%s.servers[%d]", p, j), "IPv6 server %s is not supported yet", s)
			}
		}
	}
}
