package config

import (
	"fmt"
	"net/netip"
)

// UPnP lets LAN clients open ports themselves with UPnP IGD, NAT-PMP and
// PCP (task 059), like pfSense's miniupnpd package. goblind runs miniupnpd
// with its mappings in chains of the Goblin ruleset.
type UPnP struct {
	Enabled bool `json:"enabled"`
	// Interfaces are the LAN-side interfaces/VLANs it serves (each needs a
	// static IPv4 address). The external interface is the first enabled WAN.
	Interfaces []string `json:"interfaces,omitempty"`
	// NATPMP also answers NAT-PMP and PCP (nil = true).
	NATPMP *bool `json:"nat_pmp,omitempty"`
	// SecureMode only lets a client map ports to its own address (nil = true).
	SecureMode *bool `json:"secure_mode,omitempty"`
	// Allow lists who may map which internal ports; empty = every client on
	// the interfaces, ports 1024-65535. Everything else is denied.
	Allow []UPnPAllow `json:"allow,omitempty"`
	// MaxLease caps the lifetime (seconds) of NAT-PMP/PCP mappings; 0 = 86400.
	MaxLease int `json:"max_lease,omitempty"`
	// STUN finds the public address with a STUN server when the WAN address
	// is private (the box sits behind another router). miniupnpd refuses to
	// map ports on a private WAN address otherwise; mappings then only work
	// if the outer router forwards everything to the box (DMZ host).
	STUN bool `json:"stun,omitempty"`
}

// UPnPAllow permits mappings for clients in Address (an address object of
// type host or subnet, or a CIDR/IPv4 address) to internal Ports ("1024-65535"
// or "3074"); the external port range is the same.
type UPnPAllow struct {
	Address string `json:"address"`
	Ports   string `json:"ports"`
}

// DefaultUPnPMaxLease is the NAT-PMP/PCP lifetime cap when max_lease is 0.
const DefaultUPnPMaxLease = 86400

// NATPMPOn and SecureOn resolve the defaults (true).
func (u UPnP) NATPMPOn() bool { return u.NATPMP == nil || *u.NATPMP }
func (u UPnP) SecureOn() bool { return u.SecureMode == nil || *u.SecureMode }

// UPnPAllowPrefix resolves an allow entry's address to an IPv4 prefix: an
// address object (host or subnet) or a literal address/CIDR.
func (c *Config) UPnPAllowPrefix(addr string) (netip.Prefix, error) {
	for _, a := range c.Addresses {
		if a.Name != addr {
			continue
		}
		switch a.Type {
		case "host":
			ip, err := netip.ParseAddr(a.Value)
			if err != nil || !ip.Unmap().Is4() {
				return netip.Prefix{}, fmt.Errorf("address %q is not an IPv4 host", addr)
			}
			return netip.PrefixFrom(ip.Unmap(), 32), nil
		case "subnet":
			p, err := netip.ParsePrefix(a.Value)
			if err != nil || !p.Addr().Unmap().Is4() {
				return netip.Prefix{}, fmt.Errorf("address %q is not an IPv4 subnet", addr)
			}
			return p.Masked(), nil
		}
		return netip.Prefix{}, fmt.Errorf("address %q is a %s: use a host or subnet", addr, a.Type)
	}
	if p, err := netip.ParsePrefix(addr); err == nil && p.Addr().Is4() {
		return p.Masked(), nil
	}
	if ip, err := netip.ParseAddr(addr); err == nil && ip.Is4() {
		return netip.PrefixFrom(ip, 32), nil
	}
	return netip.Prefix{}, fmt.Errorf("%q is not an address object, IPv4 address or CIDR", addr)
}

func (v *validator) checkUPnP() {
	u := v.cfg.UPnP
	if !u.Enabled && len(u.Interfaces) == 0 && len(u.Allow) == 0 {
		return
	}
	if u.Enabled && len(u.Interfaces) == 0 {
		v.add("upnp.interfaces", "pick at least one LAN-side interface to serve")
	}
	addrs := map[string]*netip.Prefix{}
	for _, in := range v.cfg.Interfaces {
		addrs[in.Name] = in.Address
	}
	for _, vl := range v.cfg.VLANs {
		addrs[vl.Name] = vl.Address
	}
	seen := map[string]bool{}
	for i, name := range u.Interfaces {
		p := fmt.Sprintf("upnp.interfaces[%d]", i)
		role, ok := v.netifRole(name)
		switch {
		case !ok:
			v.add(p, "unknown interface %q", name)
		case role == "wan":
			v.add(p, "%q is a WAN interface: UPnP serves LAN-side interfaces", name)
		case addrs[name] == nil:
			v.add(p, "%q has no static IPv4 address for UPnP to listen on", name)
		case seen[name]:
			v.add(p, "%q is listed twice", name)
		}
		seen[name] = true
	}
	if u.Enabled {
		hasWAN := false
		for _, in := range v.cfg.Interfaces {
			hasWAN = hasWAN || in.Enabled && in.Role == "wan"
		}
		if !hasWAN {
			v.add("upnp.enabled", "UPnP needs an enabled WAN interface")
		}
	}
	for i, a := range u.Allow {
		p := fmt.Sprintf("upnp.allow[%d]", i)
		if _, err := v.cfg.UPnPAllowPrefix(a.Address); err != nil {
			v.add(p+".address", "%v", err)
		}
		if lo, hi, ok := portOrRange(a.Ports); !ok || lo < 1 || hi > 65535 {
			v.add(p+".ports", "want a port or a range like 1024-65535, got %q", a.Ports)
		}
	}
	if u.MaxLease < 0 || u.MaxLease > 7*86400 {
		v.add("upnp.max_lease", "max_lease %d: want 0 (default) to 604800 seconds", u.MaxLease)
	}
}
