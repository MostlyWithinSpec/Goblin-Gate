package render

import (
	"net/netip"
	"sort"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// ProfileAssignment says that DNS queries from one interface's subnet are
// handled with a profile (DNS filter or web filter).
type ProfileAssignment struct {
	Profile   string
	Interface string       // interface or VLAN name
	Device    string       // its device (eth1, eth1.20)
	Address   netip.Prefix // the box's address there, e.g. 192.168.1.1/24
}

// DNSFilterAssignment is kept for the DNS filter's callers.
type DNSFilterAssignment = ProfileAssignment

// DNSFilterAssignments decides which DNS filter profile applies to which
// client subnet. DNS answers do not know where the client is going, so the
// rule is per source interface: walking the policies in order, the first
// enabled accept policy with a dnsfilter_profile that lists an interface in
// src_intf (or "all"/"any") gives that interface its profile. Only enabled,
// non-WAN interfaces and VLANs with a static address count (the ones Unbound
// listens on). Sorted by interface name.
func DNSFilterAssignments(cfg *config.Config) []ProfileAssignment {
	names := map[string]bool{}
	for _, p := range cfg.Profiles.DNSFilter {
		names[p.Name] = true
	}
	return assign(cfg, func(p config.Policy) string { return p.DNSFilterProfile }, names)
}

// WebFilterAssignments is DNSFilterAssignments for webfilter_profile (the
// web filter is enforced at DNS too, task 033).
func WebFilterAssignments(cfg *config.Config) []ProfileAssignment {
	names := map[string]bool{}
	for _, p := range cfg.Profiles.WebFilter {
		names[p.Name] = true
	}
	return assign(cfg, func(p config.Policy) string { return p.WebFilterProfile }, names)
}

// AppCtrlAssignments is DNSFilterAssignments for appctrl_profile
// (application control is enforced by the same DNS proxy, task 035).
func AppCtrlAssignments(cfg *config.Config) []ProfileAssignment {
	names := map[string]bool{}
	for _, p := range cfg.Profiles.AppCtrl {
		names[p.Name] = true
	}
	return assign(cfg, func(p config.Policy) string { return p.AppCtrlProfile }, names)
}

func assign(cfg *config.Config, pick func(config.Policy) string, profiles map[string]bool) []ProfileAssignment {
	type link struct {
		name string
		addr netip.Prefix
	}
	var links []link
	wan := map[string]bool{}
	for _, i := range cfg.Interfaces {
		wan[i.Name] = i.Role == "wan"
		if i.Enabled && i.Role != "wan" && i.Address != nil {
			links = append(links, link{i.Name, *i.Address})
		}
	}
	for _, v := range cfg.VLANs {
		if v.Enabled && !wan[v.Parent] && v.Address != nil {
			links = append(links, link{v.Name, *v.Address})
		}
	}
	devs := Devices(cfg)
	assigned := map[string]string{}
	for _, pol := range cfg.Policies {
		prof := pick(pol)
		if !pol.Enabled || pol.Action != "accept" || !profiles[prof] {
			continue
		}
		for _, l := range links {
			if _, done := assigned[l.name]; done {
				continue
			}
			for _, src := range pol.SrcIntf {
				if src == l.name || src == "all" || src == "any" {
					assigned[l.name] = prof
					break
				}
			}
		}
	}
	var out []ProfileAssignment
	for _, l := range links {
		if p, ok := assigned[l.name]; ok {
			out = append(out, ProfileAssignment{Profile: p, Interface: l.name, Device: devs[l.name], Address: l.addr})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Interface < out[j].Interface })
	return out
}
