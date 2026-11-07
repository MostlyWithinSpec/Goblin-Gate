package config

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

// maxProxyDomains caps the hostnames of one published service.
const maxProxyDomains = 20

func (v *validator) checkReverseProxy() {
	rp := v.cfg.ReverseProxy
	if len(rp.Hosts) > MaxProxyHosts {
		v.add("reverse_proxy.hosts", "at most %d hosts", MaxProxyHosts)
	}
	if rp.Email != "" && (strings.ContainsAny(rp.Email, " \t\r\n\"\\<>") || !strings.Contains(rp.Email, "@") || len(rp.Email) > 254) {
		v.add("reverse_proxy.email", "invalid email address %q", rp.Email)
	}

	own := v.ownAddresses()
	names := map[string]bool{}
	domains := map[string]string{} // domain -> host name
	for i, h := range rp.Hosts {
		p := fmt.Sprintf("reverse_proxy.hosts[%d]", i)
		if !validName(h.Name) {
			v.add(p+".name", "invalid name %q: must be 1-63 characters of [A-Za-z0-9_.-]", h.Name)
		} else if names[h.Name] {
			v.add(p+".name", "duplicate host name %q", h.Name)
		}
		names[h.Name] = true

		if len(h.Domains) == 0 {
			v.add(p+".domains", "at least one domain")
		}
		if len(h.Domains) > maxProxyDomains {
			v.add(p+".domains", "at most %d domains", maxProxyDomains)
		}
		for j, d := range h.Domains {
			dp := fmt.Sprintf("%s.domains[%d]", p, j)
			switch {
			case d != strings.ToLower(d):
				v.add(dp, "domain %q must be lower case", d)
			case strings.HasPrefix(d, "*."):
				v.add(dp, "domain %q: wildcards need a DNS challenge, which is not supported; list each name", d)
			case !validHostname(d) || !strings.Contains(d, "."):
				v.add(dp, "invalid domain %q: want a public name like media.example.com", d)
			case isIPLiteral(d):
				v.add(dp, "domain %q: Let's Encrypt certificates are for names, not addresses", d)
			default:
				if other, dup := domains[d]; dup {
					v.add(dp, "domain %q is already used by host %q", d, other)
				}
				domains[d] = h.Name
			}
		}

		up, err := ParseProxyUpstream(h.Upstream)
		if err != nil {
			v.add(p+".upstream", "upstream %q: %v", h.Upstream, err)
		} else if why := selfUpstream(up.Host, own); why != "" {
			// would publish the box's own admin page or services
			v.add(p+".upstream", "upstream %q is %s: the proxy only publishes services on other machines", h.Upstream, why)
		}
		if h.InsecureSkipVerify && err == nil && !up.TLS {
			v.add(p+".insecure_skip_verify", "only for an https upstream")
		}
		for j, a := range h.Allow {
			if _, err := v.cfg.ProxyAllowPrefix(a); err != nil {
				v.add(fmt.Sprintf("%s.allow[%d]", p, j), "%v", err)
			}
		}
	}

	if !rp.Enabled {
		return
	}
	hasWAN := false
	for _, in := range v.cfg.Interfaces {
		if in.Role == "wan" && in.Enabled {
			hasWAN = true
		}
	}
	if !hasWAN {
		v.add("reverse_proxy.enabled", "the reverse proxy needs an enabled WAN interface")
	}
	// The proxy takes TCP 80 and 443 on the WAN: nothing else may want them.
	adminPort := v.cfg.System.AdminPort
	if adminPort == 0 {
		adminPort = 443
	}
	if adminPort == 80 || adminPort == 443 {
		for _, n := range v.wanSideInterfaces() {
			if n.https {
				v.add("reverse_proxy.enabled", "%s allows the admin page (https) on port %d, which the reverse proxy takes on the WAN: remove https from its allow access or move the admin port", n.name, adminPort)
			}
		}
	}
	for i, pf := range v.cfg.NAT.PortForwards {
		if !strings.EqualFold(pf.Protocol, "tcp") {
			continue
		}
		if role, ok := v.netifRole(pf.InIntf); !ok || role != "wan" {
			continue
		}
		lo, hi, ok := portOrRange(pf.ExtPort)
		if !ok {
			continue // reported by checkPortForwards
		}
		for _, port := range []int{80, 443} {
			if lo <= port && port <= hi {
				v.add(fmt.Sprintf("nat.port_forwards[%d].ext_port", i), "port forward %q uses TCP %d on the WAN, which the reverse proxy takes: publish the service as a reverse proxy host instead, or turn the proxy off", pf.Name, port)
			}
		}
	}
}

type wanSide struct {
	name  string
	https bool
}

// wanSideInterfaces lists enabled WAN interfaces and VLANs on a WAN parent,
// and whether they allow the admin page.
func (v *validator) wanSideInterfaces() []wanSide {
	var out []wanSide
	wan := map[string]bool{}
	for _, in := range v.cfg.Interfaces {
		wan[in.Name] = in.Role == "wan"
		if in.Role == "wan" && in.Enabled {
			out = append(out, wanSide{in.Name, slices.Contains(in.AllowAccess, "https")})
		}
	}
	for _, vl := range v.cfg.VLANs {
		if wan[vl.Parent] && vl.Enabled {
			out = append(out, wanSide{vl.Name, slices.Contains(vl.AllowAccess, "https")})
		}
	}
	return out
}

// ownAddresses are the box's static interface addresses.
func (v *validator) ownAddresses() map[netip.Addr]string {
	own := map[netip.Addr]string{}
	for _, in := range v.cfg.Interfaces {
		if in.Address != nil {
			own[in.Address.Addr().Unmap()] = in.Name
		}
		if in.IPv6 != nil && in.IPv6.Address != nil {
			own[in.IPv6.Address.Addr()] = in.Name
		}
	}
	for _, vl := range v.cfg.VLANs {
		if vl.Address != nil {
			own[vl.Address.Addr().Unmap()] = vl.Name
		}
		if vl.IPv6 != nil && vl.IPv6.Address != nil {
			own[vl.IPv6.Address.Addr()] = vl.Name
		}
	}
	return own
}

// selfUpstream says why host is the box itself, or "" when it is not.
func selfUpstream(host string, own map[netip.Addr]string) string {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return "the box itself"
	}
	a, err := netip.ParseAddr(h)
	if err != nil {
		return ""
	}
	a = a.Unmap()
	switch {
	case a.IsLoopback(), a.IsUnspecified():
		return "the box itself"
	case own[a] != "":
		return "the box's own address on " + own[a]
	}
	return ""
}

func isIPLiteral(s string) bool {
	_, err := netip.ParseAddr(s)
	return err == nil
}
