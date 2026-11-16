package config

import (
	"fmt"
	"net/netip"
)

// InternalCA is a private certificate authority for the LAN (Caddy's PKI
// app, run by the reverse proxy's service): a root and an intermediate kept
// in the proxy's state directory. Reverse proxy hosts may take their
// certificates from it (issuer "internal"), and with ACME on, machines on the
// LAN side get certificates from it themselves over ACME.
type InternalCA struct {
	Enabled bool `json:"enabled"`
	// Name appears in the root and intermediate certificates' names
	// (default "Goblin Gate Local CA").
	Name string `json:"name,omitempty"`
	// ACME serves an ACME directory on the box's LAN-side addresses, port
	// ACMEPort: https://<box>:9443/acme/local/directory.
	ACME bool `json:"acme,omitempty"`
	// ACMEAllow limits who may use the ACME directory: address objects (host
	// or subnet) or CIDRs. Empty = the LAN-side networks.
	ACMEAllow []string `json:"acme_allow,omitempty"`
	// LifetimeHours is how long the reverse proxy's internal certificates are
	// valid (1-120, default 48). ACME clients get 12 hours (Caddy 2.6 sets it).
	LifetimeHours int `json:"lifetime_hours,omitempty"`
}

// Internal CA defaults and limits.
const (
	DefaultInternalCAName    = "Goblin Gate Local CA"
	DefaultCertLifetimeHours = 48
	MaxCertLifetimeHours     = 120 // leaves stay well inside the 7-day intermediate
	ACMEPort                 = 9443
	ProxyIssuerInternal      = "internal"
	ProxyIssuerLetsEncrypt   = "letsencrypt"
	maxInternalCANameLength  = 64
)

// InternalProxyAddress is where the box's DNS points the names of reverse
// proxy hosts with issuer "internal": the box redirects LAN-side connections
// to it (TCP 80, 443) to the proxy, since its own LAN addresses keep 443 for
// the admin page. 198.18.0.0/15 is reserved for network tests, so no real
// host has it.
var InternalProxyAddress = netip.MustParseAddr("198.18.0.53")

// CAName resolves the default name.
func (c InternalCA) CAName() string {
	if c.Name == "" {
		return DefaultInternalCAName
	}
	return c.Name
}

// Lifetime resolves the default certificate lifetime in hours.
func (c InternalCA) Lifetime() int {
	if c.LifetimeHours == 0 {
		return DefaultCertLifetimeHours
	}
	return c.LifetimeHours
}

// IsInternal reports whether a proxy host takes its certificate from the
// internal CA.
func (h ProxyHost) IsInternal() bool { return h.Issuer == ProxyIssuerInternal }

func (v *validator) checkInternalCA() {
	ca := v.cfg.InternalCA
	if len(ca.Name) > maxInternalCANameLength {
		v.add("internal_ca.name", "at most %d characters", maxInternalCANameLength)
	}
	for _, r := range ca.Name {
		if r < 0x20 || r == 0x7f || r == '"' || r == 0x5c {
			v.add("internal_ca.name", "no control characters, quotes or backslashes")
			break
		}
	}
	if ca.LifetimeHours < 0 || ca.LifetimeHours > MaxCertLifetimeHours {
		v.add("internal_ca.lifetime_hours", "1-%d hours (0 = %d)", MaxCertLifetimeHours, DefaultCertLifetimeHours)
	}
	for i, a := range ca.ACMEAllow {
		if _, err := v.cfg.ProxyAllowPrefix(a); err != nil {
			v.add(fmt.Sprintf("internal_ca.acme_allow[%d]", i), "%v", err)
		}
	}
	if len(ca.ACMEAllow) > 0 && !ca.ACME {
		v.add("internal_ca.acme_allow", "only with acme on")
	}
	if !ca.Enabled && ca.ACME {
		v.add("internal_ca.acme", "needs the internal CA enabled")
	}
	if ca.ACME && !v.hasLANSide() {
		v.add("internal_ca.acme", "needs an enabled LAN-side interface with a static IPv4 address to listen on")
	}
	for _, n := range v.cfg.netifPrefixes() {
		if n.prefix.Masked().Contains(InternalProxyAddress) {
			v.add("internal_ca", "%s uses %s, which holds %s, the address the box keeps for reverse proxy hosts with internal certificates", n.name, n.prefix.Masked(), InternalProxyAddress)
		}
	}
}

type netifPrefix struct {
	name   string
	prefix netip.Prefix
}

// netifPrefixes lists the static IPv4 prefixes of interfaces and VLANs.
func (c *Config) netifPrefixes() []netifPrefix {
	var out []netifPrefix
	for _, in := range c.Interfaces {
		if in.Address != nil && in.Address.Addr().Is4() {
			out = append(out, netifPrefix{in.Name, *in.Address})
		}
	}
	for _, vl := range c.VLANs {
		if vl.Address != nil && vl.Address.Addr().Is4() {
			out = append(out, netifPrefix{vl.Name, *vl.Address})
		}
	}
	return out
}

// hasLANSide: an enabled non-WAN interface or VLAN with a static IPv4
// address.
func (v *validator) hasLANSide() bool {
	wan := map[string]bool{}
	for _, in := range v.cfg.Interfaces {
		wan[in.Name] = in.Role == "wan"
		if in.Role != "wan" && in.Enabled && in.Address != nil && in.Address.Addr().Is4() {
			return true
		}
	}
	for _, vl := range v.cfg.VLANs {
		if !wan[vl.Parent] && vl.Enabled && vl.Address != nil && vl.Address.Addr().Is4() {
			return true
		}
	}
	return false
}

// LANSidePrefixes are the static prefixes (IPv4 and IPv6) of enabled
// interfaces and VLANs that are not WAN: the networks behind the box.
func (c *Config) LANSidePrefixes() []netip.Prefix {
	var out []netip.Prefix
	wan := map[string]bool{}
	add := func(p *netip.Prefix) {
		if p != nil {
			out = append(out, p.Masked())
		}
	}
	for _, in := range c.Interfaces {
		wan[in.Name] = in.Role == "wan"
		if in.Role == "wan" || !in.Enabled {
			continue
		}
		add(in.Address)
		if in.IPv6 != nil {
			add(in.IPv6.Address)
		}
	}
	for _, vl := range c.VLANs {
		if wan[vl.Parent] || !vl.Enabled {
			continue
		}
		add(vl.Address)
		if vl.IPv6 != nil {
			add(vl.IPv6.Address)
		}
	}
	return out
}

// LANSideAddrs are the box's own static IPv4 addresses on those networks.
func (c *Config) LANSideAddrs() []netip.Addr {
	var out []netip.Addr
	wan := map[string]bool{}
	for _, in := range c.Interfaces {
		wan[in.Name] = in.Role == "wan"
		if in.Role != "wan" && in.Enabled && in.Address != nil && in.Address.Addr().Is4() {
			out = append(out, in.Address.Addr())
		}
	}
	for _, vl := range c.VLANs {
		if !wan[vl.Parent] && vl.Enabled && vl.Address != nil && vl.Address.Addr().Is4() {
			out = append(out, vl.Address.Addr())
		}
	}
	return out
}
