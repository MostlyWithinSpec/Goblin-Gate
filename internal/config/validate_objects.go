package config

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

func (v *validator) checkAddresses() {
	for i, a := range v.cfg.Addresses {
		p := fmt.Sprintf("addresses[%d]", i)
		switch a.Type {
		case "subnet":
			pf, err := netip.ParsePrefix(a.Value)
			if err != nil {
				v.add(p+".value", "invalid subnet %q: %v", a.Value, err)
			} else if pf.Addr().Is4In6() {
				v.add(p+".value", "write IPv4-mapped subnet %q as plain IPv4", a.Value)
			}
		case "host":
			if _, err := netip.ParseAddr(a.Value); err != nil {
				v.add(p+".value", "invalid host address %q: %v", a.Value, err)
			}
		case "range":
			v.checkRange(p+".value", a.Value)
		case "fqdn":
			if !validHostname(a.Value) {
				v.add(p+".value", "invalid hostname %q", a.Value)
			}
		case "country":
			codes := CountryCodes(a.Value)
			if len(codes) == 0 {
				v.add(p+".value", "pick at least one country")
			} else if len(codes) > MaxCountriesPerObject {
				v.add(p+".value", "at most %d countries per object", MaxCountriesPerObject)
			}
			for _, c := range codes {
				if _, ok := Countries[c]; !ok {
					v.add(p+".value", "unknown country code %q: want ISO 3166 codes like US, CA", c)
				}
			}
		case "live":
			v.checkLiveAddress(p, a)
		default:
			v.add(p+".type", "unknown type %q: want subnet, host, range, fqdn, country or live", a.Type)
		}
	}
}

// MaxLiveObjectsPerPlugin is how many live address objects one plugin may
// keep.
const MaxLiveObjectsPerPlugin = 32

// checkLiveAddress: a live object's value names the plugin that keeps its
// members (capability objects:write). The plugin's entry must approve it, so
// a typo cannot leave an object nobody fills; a disabled plugin is fine (its
// object keeps the last members it set).
func (v *validator) checkLiveAddress(p string, a Address) {
	if !ValidPluginName(a.Value) {
		v.add(p+".value", "a live object's value is the plugin that keeps it up to date; %q is not a plugin name", a.Value)
		return
	}
	approved := false
	for _, pl := range v.cfg.Plugins {
		if pl.Name == a.Value && slices.Contains(pl.Capabilities, "objects:write") {
			approved = true
		}
	}
	if !approved {
		v.add(p+".value", "plugin %s does not have objects:write approved: enable it with that capability first", a.Value)
		return
	}
	n := 0
	for _, o := range v.cfg.Addresses {
		if o.Type == "live" && o.Value == a.Value {
			n++
		}
	}
	if n > MaxLiveObjectsPerPlugin && a.Name == lastLiveOf(v.cfg.Addresses, a.Value) {
		v.add(p+".value", "plugin %s keeps %d live objects, at most %d", a.Value, n, MaxLiveObjectsPerPlugin)
	}
}

// lastLiveOf names the last live object of plugin, so a limit error is
// reported once.
func lastLiveOf(addrs []Address, plugin string) string {
	name := ""
	for _, a := range addrs {
		if a.Type == "live" && a.Value == plugin {
			name = a.Name
		}
	}
	return name
}

func (v *validator) checkRange(p, value string) {
	parts := strings.Split(value, "-")
	if len(parts) != 2 {
		v.add(p, "invalid range %q: want \"start-end\"", value)
		return
	}
	lo, errL := netip.ParseAddr(strings.TrimSpace(parts[0]))
	if errL != nil {
		v.add(p, "invalid range %q: cannot parse start address: %v", value, errL)
		return
	}
	hi, errH := netip.ParseAddr(strings.TrimSpace(parts[1]))
	if errH != nil {
		v.add(p, "invalid range %q: cannot parse end address: %v", value, errH)
		return
	}
	c, ok := addrCmp(lo, hi)
	if !ok {
		v.add(p, "range %q mixes IPv4 and IPv6", value)
		return
	}
	if c > 0 {
		v.add(p, "range %q: start address is greater than end address", value)
	}
}

// checkGroups checks group members exist and no group includes itself.
func (v *validator) checkGroups() {
	c := v.cfg
	addrNames := nameList(c.Addresses, func(x Address) string { return x.Name })
	agNames := nameList(c.AddressGroups, func(x AddressGroup) string { return x.Name })
	agMembers := make([][]string, len(c.AddressGroups))
	for i, g := range c.AddressGroups {
		agMembers[i] = g.Members
	}
	v.checkGroupKind("address_groups", "address", toSet(addrNames, agNames), agNames, agMembers)

	svcNames := nameList(c.Services, func(x Service) string { return x.Name })
	sgNames := nameList(c.ServiceGroups, func(x ServiceGroup) string { return x.Name })
	sgMembers := make([][]string, len(c.ServiceGroups))
	for i, g := range c.ServiceGroups {
		sgMembers[i] = g.Members
	}
	v.checkGroupKind("service_groups", "service", toSet(svcNames, sgNames), sgNames, sgMembers)
}

func (v *validator) checkGroupKind(base, kind string, known map[string]bool, names []string, members [][]string) {
	for i, ms := range members {
		for j, m := range ms {
			if !known[m] {
				v.add(fmt.Sprintf("%s[%d].members[%d]", base, i, j), "unknown member %q: not a %s or %s group", m, kind, kind)
			}
		}
	}
	cyclic := selfIncluding(names, members)
	for i := range names {
		if !cyclic[i] {
			continue
		}
		msg := fmt.Sprintf("group includes itself through other %s groups", kind)
		for _, m := range members[i] {
			if m == names[i] {
				msg = "group includes itself"
				break
			}
		}
		v.add(fmt.Sprintf("%s[%d]", base, i), "%s", msg)
	}
}

func (v *validator) checkServices() {
	for i, s := range v.cfg.Services {
		p := fmt.Sprintf("services[%d]", i)
		switch s.Protocol {
		case "tcp", "udp":
			if !portSpecOK(s.Ports) {
				v.add(p+".ports", "invalid ports %q: want a comma list of 1-65535 or \"low-high\" ranges", s.Ports)
			}
		case "icmp", "any":
			if s.Ports != "" {
				v.add(p+".ports", "%s services must not have ports (got %q)", s.Protocol, s.Ports)
			}
		default:
			v.add(p+".protocol", "unknown protocol %q: want tcp, udp, icmp or any", s.Protocol)
		}
	}
}
