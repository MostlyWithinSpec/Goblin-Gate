package config

import (
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strings"
)

// Sites joins this gate with other Goblin Gates (site-to-site VPN). Every
// directly connected site is its own WireGuard device named after the site;
// that name is the site's zone in policies (src_intf/dst_intf), as a VLAN's
// is. A site reached through another one (Via) has no device: its networks
// are routed into the tunnel of the site it is reached through, and policies
// see its traffic on that site's zone.
type Sites struct {
	Enabled bool `json:"enabled"`
	// Address is this gate's tunnel address, the same on every site tunnel
	// (IPv4, from DefaultSitesNetwork unless typed). The other gates ping it
	// to tell whether the site is reachable.
	Address netip.Addr `json:"address,omitzero"`
	// Endpoint is the public DNS name or address the other gates connect
	// to; "" = this gate only dials out (its sites need an endpoint).
	Endpoint string `json:"endpoint,omitempty"`
	// Networks are what this gate offers the other sites: address objects
	// of type host or subnet and interface/VLAN names (like BGP's announce).
	Networks []string `json:"networks,omitempty"`
	// AllowAccess lets the other gates reach the box itself: ping (always,
	// for the reachability check), https, ssh.
	AllowAccess []string `json:"allow_access,omitempty"`
	// Announce puts the site networks into BGP and OSPF when they run.
	Announce bool   `json:"announce,omitempty"`
	Remotes  []Site `json:"remotes,omitempty"`
}

// Site is one remote gate.
type Site struct {
	// Name is the device and zone name: 1-15 of a-z, 0-9 and -, starting
	// with a letter; unique among all interfaces.
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	// PublicKey is the remote gate's key for this tunnel (base64).
	PublicKey string `json:"public_key,omitempty"`
	// Endpoint is host:port of the remote gate; "" = it dials this one.
	Endpoint string `json:"endpoint,omitempty"`
	// ListenPort is this gate's UDP port for the tunnel (opened on WAN).
	ListenPort int `json:"listen_port,omitempty"`
	// Address is the remote gate's tunnel address.
	Address netip.Addr `json:"address,omitzero"`
	// Networks are the remote site's networks, routed into the tunnel.
	Networks []netip.Prefix `json:"networks,omitempty"`
	// Keepalive in seconds; 0 = 25 when this gate dials the remote one,
	// off when only the remote dials.
	Keepalive int `json:"keepalive,omitempty"`
	// Via names another site this one is reached through (hub and spoke);
	// such a site has no key, endpoint or port of its own.
	Via         string `json:"via,omitempty"`
	Description string `json:"description,omitempty"`
}

// DefaultSitesNetwork is where tunnel addresses are picked from.
var DefaultSitesNetwork = netip.MustParsePrefix("10.254.0.0/16")

// FirstSitePort is the first listen port given to a site tunnel.
const FirstSitePort = 51830

// DefaultSiteKeepalive applies when this gate dials the remote one.
const DefaultSiteKeepalive = 25

// SiteKeyName is the key file name (in WireGuardKeyDir) of a site tunnel;
// the preshared key is the same name with ".psk".
func SiteKeyName(site string) string { return "site-" + site }

var siteNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,14}$`)

// ValidSiteName reports whether a site name can be a device name.
func ValidSiteName(s string) bool { return siteNameRe.MatchString(s) }

// DirectSites are the enabled sites with a tunnel of their own (none when
// sites are off).
func (c *Config) DirectSites() []Site {
	if !c.VPN.Sites.Enabled {
		return nil
	}
	var out []Site
	for _, s := range c.VPN.Sites.Remotes {
		if s.Enabled && s.Via == "" {
			out = append(out, s)
		}
	}
	return out
}

// SiteRoutes are the prefixes routed into a direct site's tunnel: its
// address, its networks and those of the enabled sites reached through it.
func (c *Config) SiteRoutes(name string) []netip.Prefix {
	var out []netip.Prefix
	for _, s := range c.VPN.Sites.Remotes {
		if !s.Enabled || (s.Name != name && s.Via != name) {
			continue
		}
		if s.Address.IsValid() {
			out = append(out, netip.PrefixFrom(s.Address, s.Address.BitLen()))
		}
		for _, p := range s.Networks {
			out = append(out, p.Masked())
		}
	}
	slices.SortFunc(out, func(a, b netip.Prefix) int { return strings.Compare(a.String(), b.String()) })
	return slices.Compact(out)
}

// SiteNetworks resolves this gate's offered networks to prefixes.
func (c *Config) SiteNetworks() []netip.Prefix {
	var out []netip.Prefix
	for _, name := range c.VPN.Sites.Networks {
		out = append(out, c.routingPrefixes(name)...)
	}
	slices.SortFunc(out, func(a, b netip.Prefix) int { return strings.Compare(a.String(), b.String()) })
	return slices.Compact(out)
}

// RemoteSiteNetworks are all enabled remote sites' networks (for routing
// announcements and the flow lookup), each with the zone it arrives on.
func (c *Config) RemoteSiteNetworks() map[netip.Prefix]string {
	out := map[netip.Prefix]string{}
	if !c.VPN.Sites.Enabled {
		return out
	}
	for _, s := range c.VPN.Sites.Remotes {
		if !s.Enabled {
			continue
		}
		zone := s.Name
		if s.Via != "" {
			zone = s.Via
		}
		for _, p := range s.Networks {
			out[p.Masked()] = zone
		}
		if s.Address.IsValid() {
			out[netip.PrefixFrom(s.Address, 32)] = zone
		}
	}
	return out
}

// localPrefixes are the networks this gate has itself, for overlap checks.
func (c *Config) localPrefixes() []namedNet {
	var out []namedNet
	add := func(name string, p *netip.Prefix) {
		if p != nil && p.IsValid() {
			out = append(out, namedNet{name, p.Masked()})
		}
	}
	for _, in := range c.Interfaces {
		if in.Role == "wan" {
			continue
		}
		add("interface "+in.Name, in.Address)
		if in.IPv6 != nil {
			add("interface "+in.Name, in.IPv6.Address)
		}
	}
	for _, vl := range c.VLANs {
		add("VLAN "+vl.Name, vl.Address)
		if vl.IPv6 != nil {
			add("VLAN "+vl.Name, vl.IPv6.Address)
		}
	}
	for _, w := range c.VPN.WireGuard {
		a := w.Address
		add("WireGuard "+w.Name, &a)
	}
	for _, n := range c.SiteNetworks() {
		add("this site's networks", &n)
	}
	return out
}

type namedNet struct {
	name string
	net  netip.Prefix
}

// SiteOverlap names what a remote network overlaps on this gate (local
// networks, its tunnel address, other sites), "" when nothing. skip is the
// site being checked.
func (c *Config) SiteOverlap(p netip.Prefix, skip string) string {
	p = p.Masked()
	for _, l := range c.localPrefixes() {
		if l.net.Overlaps(p) {
			return fmt.Sprintf("%s (%s)", l.name, l.net)
		}
	}
	if a := c.VPN.Sites.Address; a.IsValid() && p.Contains(a) {
		return "this gate's tunnel address " + a.String()
	}
	for _, s := range c.VPN.Sites.Remotes {
		if s.Name == skip {
			continue
		}
		for _, q := range s.Networks {
			if q.Masked().Overlaps(p) {
				return fmt.Sprintf("site %s (%s)", s.Name, q.Masked())
			}
		}
		if s.Address.IsValid() && p.Contains(s.Address) {
			return fmt.Sprintf("site %s's tunnel address %s", s.Name, s.Address)
		}
	}
	return ""
}

func (v *validator) checkSites(taken map[string]bool, ports map[int]bool) {
	s := v.cfg.VPN.Sites
	if !s.Enabled && len(s.Remotes) == 0 {
		return
	}
	const p = "vpn.sites"
	// the address is picked by the first pairing: sites may be on without one
	// until there is a site
	if s.Address.IsValid() || (s.Enabled && len(s.Remotes) > 0) {
		if !s.Address.Is4() || s.Address.IsLoopback() || s.Address.IsMulticast() || s.Address.IsUnspecified() {
			v.add(p+".address", "want this gate's IPv4 tunnel address, e.g. 10.254.17.1")
		} else {
			for _, l := range v.cfg.localPrefixes() {
				if l.net.Contains(s.Address) {
					v.add(p+".address", "%s is inside %s (%s)", s.Address, l.name, l.net)
					break
				}
			}
		}
	}
	if s.Endpoint != "" && !validHost(s.Endpoint) {
		v.add(p+".endpoint", "want the public DNS name or IP address other gates connect to, e.g. home.example.net")
	}
	for i, name := range s.Networks {
		if len(v.cfg.routingPrefixes(name)) == 0 {
			v.add(fmt.Sprintf("%s.networks[%d]", p, i), "%q is neither an address object of type host or subnet nor an interface with a static address", name)
		}
	}
	for i, a := range s.AllowAccess {
		if !oneOf(a, "ping", "https", "ssh") {
			v.add(fmt.Sprintf("%s.allow_access[%d]", p, i), "unknown access %q: want ping, https or ssh", a)
		}
	}
	names := map[string]Site{}
	for _, r := range s.Remotes {
		names[r.Name] = r
	}
	seenName, seenKey, seenAddr := map[string]bool{}, map[string]bool{}, map[netip.Addr]bool{}
	for i, r := range s.Remotes {
		rp := fmt.Sprintf("%s.remotes[%d]", p, i)
		switch {
		case !ValidSiteName(r.Name):
			v.add(rp+".name", "invalid site name %q: 1-15 of a-z, 0-9 and -, starting with a letter", r.Name)
		case seenName[r.Name]:
			v.add(rp+".name", "duplicate site %q", r.Name)
		case taken[r.Name] && r.Via == "":
			v.add(rp+".name", "name %q is already used by another interface", r.Name)
		}
		seenName[r.Name] = true
		if r.Via == "" {
			taken[r.Name] = true
		}
		if len(r.Description) > 80 {
			v.add(rp+".description", "at most 80 characters")
		}
		if !r.Address.Is4() || r.Address.IsLoopback() || r.Address.IsUnspecified() || r.Address.IsMulticast() {
			v.add(rp+".address", "want the remote gate's IPv4 tunnel address")
		} else {
			switch {
			case r.Address == s.Address:
				v.add(rp+".address", "%s is this gate's own tunnel address", r.Address)
			case seenAddr[r.Address]:
				v.add(rp+".address", "tunnel address %s is used by two sites", r.Address)
			}
			seenAddr[r.Address] = true
		}
		for j, n := range r.Networks {
			np := fmt.Sprintf("%s.networks[%d]", rp, j)
			if !n.IsValid() || n.Masked() != n {
				v.add(np, "want a network in CIDR form without host bits, e.g. 192.168.50.0/24")
				continue
			}
			if n.Bits() == 0 {
				v.add(np, "a site cannot take the whole internet (%s)", n)
				continue
			}
			if what := v.cfg.SiteOverlap(n, r.Name); what != "" {
				v.add(np, "%s overlaps %s", n, what)
			}
			for _, m := range r.Networks[:j] {
				if m.Overlaps(n) {
					v.add(np, "%s overlaps %s of the same site", n, m)
				}
			}
		}
		if r.Keepalive < 0 || r.Keepalive > 3600 {
			v.add(rp+".keepalive", "keepalive must be 0-3600 seconds")
		}
		if r.Via != "" {
			hub, ok := names[r.Via]
			switch {
			case r.Via == r.Name:
				v.add(rp+".via", "a site cannot be reached through itself")
			case !ok:
				v.add(rp+".via", "unknown site %q", r.Via)
			case hub.Via != "":
				v.add(rp+".via", "site %q is itself reached through %q: name the site with the tunnel", r.Via, hub.Via)
			}
			if r.PublicKey != "" || r.Endpoint != "" || r.ListenPort != 0 {
				v.add(rp+".via", "a site reached through another one has no key, endpoint or port of its own")
			}
			continue
		}
		if !validWGKey(r.PublicKey) {
			v.add(rp+".public_key", "want the remote gate's public key (base64, 32 bytes)")
		} else if seenKey[r.PublicKey] {
			v.add(rp+".public_key", "public key used by two sites")
		}
		seenKey[r.PublicKey] = true
		if r.Endpoint != "" && !validHostPort(r.Endpoint) {
			v.add(rp+".endpoint", "want host:port, e.g. cabin.example.net:51830")
		}
		if r.Endpoint == "" && s.Endpoint == "" && r.Enabled {
			v.add(rp+".endpoint", "neither gate has an endpoint: one of them must be reachable from the internet")
		}
		switch {
		case r.ListenPort < 1 || r.ListenPort > 65535:
			v.add(rp+".listen_port", "listen_port must be 1-65535")
		case ports[r.ListenPort]:
			v.add(rp+".listen_port", "listen_port %d is used twice", r.ListenPort)
		}
		ports[r.ListenPort] = true
	}
}

// FreeSitePort is the first listen port from FirstSitePort that no
// WireGuard interface or site uses.
func (c *Config) FreeSitePort(extra ...int) int {
	used := map[int]bool{}
	for _, w := range c.VPN.WireGuard {
		port := w.ListenPort
		if port == 0 {
			port = DefaultWireGuardPort
		}
		used[port] = true
	}
	for _, s := range c.VPN.Sites.Remotes {
		used[s.ListenPort] = true
	}
	for _, p := range extra {
		used[p] = true
	}
	for p := FirstSitePort; p < 65535; p++ {
		if !used[p] {
			return p
		}
	}
	return 0
}
