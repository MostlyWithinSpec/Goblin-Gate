package config

import (
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// DynamicRouting exchanges routes with other routers through FRR: OSPF inside
// a site, BGP with a provider, a cloud or a lab. Learned routes go into the
// main routing table; the box's own default route stays with its gateways.
type DynamicRouting struct {
	// RouterID identifies the box to its peers (an IPv4 address it need not
	// own); zero = the first static LAN-side IPv4 address.
	RouterID netip.Addr `json:"router_id,omitzero"`
	BGP      BGP        `json:"bgp,omitzero"`
	OSPF     OSPF       `json:"ospf,omitzero"`
}

// BGP is one BGP speaker with its neighbours.
type BGP struct {
	Enabled bool   `json:"enabled"`
	AS      uint32 `json:"as"`
	// Announce lists what the box announces: address objects of type host or
	// subnet, and interfaces or VLANs (their network). A prefix is only
	// announced while the box has a route to it.
	Announce              []string      `json:"announce,omitempty"`
	RedistributeConnected bool          `json:"redistribute_connected,omitempty"`
	RedistributeStatic    bool          `json:"redistribute_static,omitempty"`
	Neighbors             []BGPNeighbor `json:"neighbors,omitempty"`
}

// BGPNeighbor is one BGP session.
type BGPNeighbor struct {
	Address     netip.Addr `json:"address"`
	RemoteAS    uint32     `json:"remote_as"`
	Enabled     bool       `json:"enabled"`
	Description string     `json:"description,omitempty"`
	// Password is the TCP MD5 password both sides share (a secret).
	Password string `json:"password,omitempty"`
	// Multihop allows a neighbour this many hops away (eBGP between
	// loopbacks or across a tunnel); 0 = directly connected.
	Multihop int `json:"multihop,omitempty"`
	// Import and Export filter the routes learned from and announced to
	// this neighbour, first match wins; empty = everything. A non-empty list
	// ends with an implicit deny.
	Import []PrefixRule `json:"import,omitempty"`
	Export []PrefixRule `json:"export,omitempty"`
}

// PrefixRule permits or denies a prefix, and with MaxLength the prefixes
// inside it up to that length.
type PrefixRule struct {
	Action    string       `json:"action"` // permit | deny
	Prefix    netip.Prefix `json:"prefix"`
	MaxLength int          `json:"max_length,omitempty"` // 0 = exactly Prefix
}

// OSPF is OSPFv2 (IPv4).
type OSPF struct {
	Enabled               bool       `json:"enabled"`
	Areas                 []OSPFArea `json:"areas,omitempty"`
	RedistributeConnected bool       `json:"redistribute_connected,omitempty"`
	RedistributeStatic    bool       `json:"redistribute_static,omitempty"`
}

// OSPFArea is an area with the interfaces in it.
type OSPFArea struct {
	ID         string          `json:"id"` // 0, 0.0.0.0, 10.0.0.1
	Interfaces []OSPFInterface `json:"interfaces"`
}

// OSPFInterface runs OSPF on an interface or VLAN.
type OSPFInterface struct {
	Name string `json:"name"`
	Cost int    `json:"cost,omitempty"` // 0 = FRR's default
	// Passive announces the interface's network without talking OSPF on
	// it (a LAN with no routers).
	Passive bool `json:"passive,omitempty"`
}

// MaxBGPNeighbors and MaxPrefixRules bound the section.
const (
	MaxBGPNeighbors = 64
	MaxPrefixRules  = 256
)

var frrTextRe = regexp.MustCompile(`^[A-Za-z0-9 ._,:/()+@#-]*$`)

// OSPFAreaID returns an area's ID in dotted form, ok false when invalid.
func OSPFAreaID(id string) (string, bool) {
	if n, err := strconv.ParseUint(id, 10, 32); err == nil {
		return netip.AddrFrom4([4]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}).String(), true
	}
	a, err := netip.ParseAddr(id)
	if err != nil || !a.Is4() {
		return "", false
	}
	return a.String(), true
}

// RoutingRouterID is the router ID FRR uses: the configured one, else the
// first static LAN-side IPv4 address (invalid when there is none).
func (c *Config) RoutingRouterID() netip.Addr {
	if c.Routing.RouterID.IsValid() {
		return c.Routing.RouterID
	}
	wan := map[string]bool{}
	for _, in := range c.Interfaces {
		wan[in.Name] = in.Role == "wan"
		if in.Role != "wan" && in.Enabled && in.Address != nil && in.Address.Addr().Is4() {
			return in.Address.Addr()
		}
	}
	for _, vl := range c.VLANs {
		if !wan[vl.Parent] && vl.Enabled && vl.Address != nil && vl.Address.Addr().Is4() {
			return vl.Address.Addr()
		}
	}
	return netip.Addr{}
}

// AnnouncePrefixes resolves BGP's announce list to prefixes.
func (c *Config) AnnouncePrefixes() []netip.Prefix {
	var out []netip.Prefix
	for _, name := range c.Routing.BGP.Announce {
		out = append(out, c.routingPrefixes(name)...)
	}
	slices.SortFunc(out, func(a, b netip.Prefix) int { return strings.Compare(a.String(), b.String()) })
	return slices.Compact(out)
}

// routingPrefixes is what one announce entry stands for: an address object
// (host, subnet) or an interface/VLAN's networks; nil when it is neither.
func (c *Config) routingPrefixes(name string) []netip.Prefix {
	for _, a := range c.Addresses {
		if a.Name != name {
			continue
		}
		switch a.Type {
		case "host":
			if ad, err := netip.ParseAddr(a.Value); err == nil {
				return []netip.Prefix{netip.PrefixFrom(ad, ad.BitLen())}
			}
		case "subnet":
			if p, err := netip.ParsePrefix(a.Value); err == nil {
				return []netip.Prefix{p.Masked()}
			}
		}
		return nil
	}
	var out []netip.Prefix
	add := func(addr *netip.Prefix, v6 *IPv6) {
		if addr != nil {
			out = append(out, addr.Masked())
		}
		if v6 != nil && v6.Address != nil {
			out = append(out, v6.Address.Masked())
		}
	}
	for _, in := range c.Interfaces {
		if in.Name == name {
			add(in.Address, in.IPv6)
		}
	}
	for _, vl := range c.VLANs {
		if vl.Name == name {
			add(vl.Address, vl.IPv6)
		}
	}
	return out
}

func (v *validator) checkDynamicRouting() {
	r := v.cfg.Routing
	if !r.BGP.Enabled && !r.OSPF.Enabled {
		return
	}
	if r.RouterID.IsValid() && !r.RouterID.Is4() {
		v.add("routing.router_id", "must be an IPv4 address (it need not be one of the box's)")
	}
	if !v.cfg.RoutingRouterID().IsValid() {
		v.add("routing.router_id", "set a router ID: the box has no static LAN-side IPv4 address to take it from")
	}
	ifaces := map[string]bool{}
	for _, in := range v.cfg.Interfaces {
		ifaces[in.Name] = true
	}
	for _, vl := range v.cfg.VLANs {
		ifaces[vl.Name] = true
	}
	if r.BGP.Enabled {
		v.checkBGP(ifaces)
	}
	if r.OSPF.Enabled {
		v.checkOSPF(ifaces)
	}
}

func (v *validator) checkBGP(ifaces map[string]bool) {
	b := v.cfg.Routing.BGP
	if b.AS == 0 || b.AS == 23456 || b.AS == 4294967295 {
		v.add("routing.bgp.as", "must be an AS number (1-4294967294, private: 64512-65534 or 4200000000-4294967294)")
	}
	for i, name := range b.Announce {
		p := fmt.Sprintf("routing.bgp.announce[%d]", i)
		if len(v.cfg.routingPrefixes(name)) == 0 {
			if ifaces[name] {
				v.add(p, "interface %s has no static address to announce", name)
			} else {
				v.add(p, "%q is neither an address object of type host or subnet nor an interface", name)
			}
		}
	}
	if len(b.Neighbors) > MaxBGPNeighbors {
		v.add("routing.bgp.neighbors", "at most %d neighbours", MaxBGPNeighbors)
	}
	seen := map[netip.Addr]bool{}
	for i, n := range b.Neighbors {
		p := fmt.Sprintf("routing.bgp.neighbors[%d]", i)
		switch {
		case !n.Address.IsValid() || n.Address.IsUnspecified() || n.Address.IsMulticast() || n.Address.IsLoopback():
			v.add(p+".address", "must be the neighbour's IPv4 or IPv6 address")
		case seen[n.Address]:
			v.add(p+".address", "neighbour %s is listed twice", n.Address)
		}
		seen[n.Address] = true
		if n.RemoteAS == 0 || n.RemoteAS == 23456 || n.RemoteAS == 4294967295 {
			v.add(p+".remote_as", "must be the neighbour's AS number")
		}
		if len(n.Description) > 80 || !frrTextRe.MatchString(n.Description) {
			v.add(p+".description", "at most 80 letters, digits, spaces and . _ , : / ( ) + @ # -")
		}
		if len(n.Password) > 80 || strings.ContainsFunc(n.Password, func(r rune) bool { return r <= ' ' || r > '~' }) {
			v.add(p+".password", "at most 80 printable characters without spaces")
		}
		if n.Multihop < 0 || n.Multihop > 255 {
			v.add(p+".multihop", "must be 0 (directly connected) to 255")
		}
		v.checkPrefixRules(p+".import", n.Import)
		v.checkPrefixRules(p+".export", n.Export)
	}
}

func (v *validator) checkPrefixRules(path string, rules []PrefixRule) {
	if len(rules) > MaxPrefixRules {
		v.add(path, "at most %d rules", MaxPrefixRules)
	}
	for i, r := range rules {
		p := fmt.Sprintf("%s[%d]", path, i)
		if r.Action != "permit" && r.Action != "deny" {
			v.add(p+".action", "must be permit or deny")
		}
		if !r.Prefix.IsValid() {
			v.add(p+".prefix", "must be a prefix such as 10.0.0.0/8")
			continue
		}
		if r.Prefix != r.Prefix.Masked() {
			v.add(p+".prefix", "%s has host bits set: %s", r.Prefix, r.Prefix.Masked())
		}
		if r.MaxLength != 0 && (r.MaxLength < r.Prefix.Bits() || r.MaxLength > r.Prefix.Addr().BitLen()) {
			v.add(p+".max_length", "must be between %d and %d", r.Prefix.Bits(), r.Prefix.Addr().BitLen())
		}
	}
}

func (v *validator) checkOSPF(ifaces map[string]bool) {
	o := v.cfg.Routing.OSPF
	if len(o.Areas) == 0 {
		v.add("routing.ospf.areas", "add an area with at least one interface")
	}
	areas := map[string]bool{}
	used := map[string]bool{}
	for i, a := range o.Areas {
		p := fmt.Sprintf("routing.ospf.areas[%d]", i)
		id, ok := OSPFAreaID(a.ID)
		switch {
		case !ok:
			v.add(p+".id", "must be an area number such as 0 or an ID such as 0.0.0.1")
		case areas[id]:
			v.add(p+".id", "area %s is listed twice", id)
		}
		areas[id] = true
		if len(a.Interfaces) == 0 {
			v.add(p+".interfaces", "add at least one interface")
		}
		for j, in := range a.Interfaces {
			q := fmt.Sprintf("%s.interfaces[%d]", p, j)
			switch {
			case !ifaces[in.Name]:
				v.add(q+".name", "no interface or VLAN %q", in.Name)
			case used[in.Name]:
				v.add(q+".name", "%s is in more than one area", in.Name)
			case len(v.cfg.routingPrefixes(in.Name)) == 0:
				v.add(q+".name", "%s has no static address: OSPF needs one", in.Name)
			}
			used[in.Name] = true
			if in.Cost < 0 || in.Cost > 65535 {
				v.add(q+".cost", "must be 1-65535, or 0 for the default")
			}
		}
	}
}
