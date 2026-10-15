package config

import "fmt"

// v6link is an interface or VLAN with its IPv6 block, for the cross checks.
type v6link struct {
	path, name, role string
	enabled          bool
	x                *IPv6
}

func (v *validator) v6links() []v6link {
	var out []v6link
	for i, in := range v.cfg.Interfaces {
		out = append(out, v6link{fmt.Sprintf("interfaces[%d].ipv6", i), in.Name, in.Role, in.Enabled, in.IPv6})
	}
	for i, vl := range v.cfg.VLANs {
		role, _ := v.netifRole(vl.Name)
		out = append(out, v6link{fmt.Sprintf("vlans[%d].ipv6", i), vl.Name, role, vl.Enabled, vl.IPv6})
	}
	return out
}

// checkIPv6 validates the ipv6 blocks of interfaces and VLANs (task 045):
// dhcp6 only on WAN, delegated only on the LAN side with an existing dhcp6
// uplink and a prefix_id that fits its pd_size and is not used twice,
// static addresses that are IPv6 and do not overlap.
func (v *validator) checkIPv6() {
	links := v.v6links()
	dhcp6 := map[string]*IPv6{}
	for _, l := range links {
		if l.x != nil && l.x.Mode == "dhcp6" {
			dhcp6[l.name] = l.x
		}
	}
	type pid struct {
		uplink string
		id     int
	}
	usedID := map[pid]string{}
	var statics []v6link
	for _, l := range links {
		x, p := l.x, l.path
		if x == nil {
			continue
		}
		if !oneOf(x.Mode, "none", "dhcp6", "static", "delegated", "") {
			v.add(p+".mode", "unknown mode %q: want none, dhcp6, static or delegated", x.Mode)
			continue
		}
		if x.Address != nil && (x.Mode != "static" || !x.Address.Addr().Is6() || x.Address.Addr().Is4In6()) {
			if x.Mode == "static" {
				v.add(p+".address", "%s is not an IPv6 address", x.Address)
			} else {
				v.add(p+".address", "an address is only set in static mode")
			}
		}
		if x.Gateway != nil && (x.Mode != "static" || !x.Gateway.Is6() || x.Gateway.Is4In6()) {
			v.add(p+".gateway", "the gateway must be an IPv6 address in static mode")
		}
		if x.PDSize != 0 && (x.Mode != "dhcp6" || x.PDSize < 48 || x.PDSize > 64) {
			v.add(p+".pd_size", "pd_size %d: want 48-64 on a dhcp6 interface", x.PDSize)
		}
		if x.Mode != "delegated" && (x.Uplink != "" || x.PrefixID != 0) {
			v.add(p+".uplink", "uplink and prefix_id are only used in delegated mode")
		}
		if x.RA != nil && x.Mode != "static" && x.Mode != "delegated" {
			v.add(p+".ra", "router advertisements are sent on static and delegated interfaces only")
		}
		switch x.Mode {
		case "dhcp6":
			if l.role != "wan" {
				v.add(p+".mode", "dhcp6 is for WAN interfaces; LAN-side interfaces use delegated or static")
			}
		case "static":
			if x.Address == nil {
				v.add(p+".address", "static IPv6 needs an address")
			} else if x.Address.Addr().Is6() {
				if x.Address.Addr().IsLinkLocalUnicast() {
					v.add(p+".address", "%s is link-local: use a global or unique local address", x.Address)
				}
				if x.Gateway != nil && !x.Address.Contains(*x.Gateway) && !x.Gateway.IsLinkLocalUnicast() {
					v.add(p+".gateway", "gateway %s is outside %s and not link-local", x.Gateway, x.Address)
				}
				if l.role != "wan" && x.EffectiveRA().Enabled && x.Address.Bits() != 64 {
					v.add(p+".address", "router advertisements (SLAAC) need a /64, not /%d", x.Address.Bits())
				}
				statics = append(statics, l)
			}
			if x.Gateway != nil && l.role != "wan" {
				v.add(p+".gateway", "only WAN interfaces have an IPv6 gateway")
			}
		case "delegated":
			if l.role == "wan" {
				v.add(p+".mode", "delegated is for LAN-side interfaces")
				continue
			}
			up := x.Uplink
			if up == "" {
				if len(dhcp6) != 1 {
					v.add(p+".uplink", "name the dhcp6 WAN to take the prefix from (%d dhcp6 interfaces)", len(dhcp6))
					continue
				}
				for name := range dhcp6 {
					up = name
				}
			}
			ux, ok := dhcp6[up]
			if !ok {
				v.add(p+".uplink", "%q is not a dhcp6 interface", up)
				continue
			}
			size := ux.PDSize
			if size == 0 {
				size = DefaultPDSize
			}
			if x.PrefixID < 0 || x.PrefixID >= 1<<(64-size) {
				v.add(p+".prefix_id", "prefix_id %d does not fit a /%d delegation (0-%d)", x.PrefixID, size, 1<<(64-size)-1)
			}
			k := pid{up, x.PrefixID}
			if other, dup := usedID[k]; dup {
				v.add(p+".prefix_id", "prefix_id %d of %s is already used by %s", x.PrefixID, up, other)
			}
			usedID[k] = l.name
		}
	}
	for i := 1; i < len(statics); i++ {
		for j := 0; j < i; j++ {
			a, b := *statics[i].x.Address, *statics[j].x.Address
			if prefixesOverlap(a, b) {
				v.add(statics[i].path+".address", "%s overlaps with %s (%s)", a, statics[j].name, b)
			}
		}
	}
}
