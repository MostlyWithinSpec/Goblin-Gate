package config

import (
	"encoding/base64"
	"fmt"
	"github.com/networkgoblinlabs/goblin-gate/internal/appctrl"
	"net/netip"
	"regexp"
	"sort"
	"strings"
)

// ValidationError describes one problem found by Validate. Path points at the
// offending field with JSON-style indexing, e.g. "policies[2].dst_addr[0]"; it
// is empty for whole-config problems.
type ValidationError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// Error implements the error interface.
func (e ValidationError) Error() string {
	if e.Path == "" {
		return e.Message
	}
	return e.Path + ": " + e.Message
}

// validator accumulates errors while the check functions walk the config.
type validator struct {
	cfg  *Config
	errs []ValidationError
}

func (v *validator) add(path, format string, args ...any) {
	v.errs = append(v.errs, ValidationError{Path: path, Message: fmt.Sprintf(format, args...)})
}

// Validate checks cfg and returns every problem it finds, each with a
// JSON-style path to the offending field. An empty result means the config is
// valid. Checks never stop at the first error.
//
// A valid config is also renderable: Validate rejects what the renderers do
// not support yet (IPv6, policy schedules) so problems surface here, with a
// path, instead of as a render error during commit.
func Validate(cfg *Config) []ValidationError {
	if cfg == nil {
		return []ValidationError{{Message: "config is nil"}}
	}
	v := &validator{cfg: cfg}
	v.checkSystem()
	v.checkNames()
	v.checkInterfaces()
	v.checkVLANs()
	v.checkInterfaceOverlap()
	v.checkIPv6()
	v.checkAddresses()
	v.checkGroups()
	v.checkServices()
	v.checkPolicies()
	v.checkSchedules()
	v.checkUPnP()
	v.checkRouting()
	v.checkPortForwards()
	v.checkDHCP()
	v.checkFeedsAndAdmins()
	v.checkDNSFilters()
	v.checkIPSProfiles()
	v.checkWebFilters()
	v.checkTailscale()
	v.checkWireGuard()
	v.checkAppCtrl()
	v.checkNotifications()
	v.checkDNS()
	return v.errs
}

func (v *validator) checkSystem() {
	c := v.cfg
	if c.SchemaVersion < 1 || c.SchemaVersion > SchemaVersion {
		v.add("schema_version", "unsupported schema version %d: this build reads 1-%d", c.SchemaVersion, SchemaVersion)
	}
	if !validHostname(c.System.Hostname) {
		v.add("system.hostname", "invalid hostname %q", c.System.Hostname)
	}
	if c.System.Domain != "" && !validHostname(c.System.Domain) {
		v.add("system.domain", "invalid domain %q", c.System.Domain)
	}
	if c.System.AdminPort < 1 || c.System.AdminPort > 65535 {
		v.add("system.admin_port", "admin_port %d out of range 1-65535", c.System.AdminPort)
	}
	v.checkSystemSettings()
}

// checkNames enforces valid, unique names within each object type, keeps
// "all"/"any" free for wildcards, and keeps names that policies reference
// unambiguous across object and group types.
func (v *validator) checkNames() {
	c := v.cfg
	type kind struct {
		label, base, field string
		names              []string
		referable          bool // referenced from policies, so "all"/"any" are reserved
	}
	addrs := nameList(c.Addresses, func(x Address) string { return x.Name })
	agroups := nameList(c.AddressGroups, func(x AddressGroup) string { return x.Name })
	svcs := nameList(c.Services, func(x Service) string { return x.Name })
	sgroups := nameList(c.ServiceGroups, func(x ServiceGroup) string { return x.Name })
	kinds := []kind{
		{"interface", "interfaces", "name", nameList(c.Interfaces, func(x Interface) string { return x.Name }), true},
		{"VLAN", "vlans", "name", nameList(c.VLANs, func(x VLAN) string { return x.Name }), true},
		{"address", "addresses", "name", addrs, true},
		{"address group", "address_groups", "name", agroups, true},
		{"service", "services", "name", svcs, true},
		{"service group", "service_groups", "name", sgroups, true},
		{"policy", "policies", "name", nameList(c.Policies, func(x Policy) string { return x.Name }), false},
		{"schedule", "schedules", "name", nameList(c.Schedules, func(x Schedule) string { return x.Name }), false},
		{"port forward", "nat.port_forwards", "name", nameList(c.NAT.PortForwards, func(x PortForward) string { return x.Name }), false},
		{"feed", "feeds", "name", nameList(c.Feeds, func(x Feed) string { return x.Name }), false},
		{"IPS profile", "profiles.ips", "name", nameList(c.Profiles.IPS, func(x IPSProfile) string { return x.Name }), false},
		{"web filter profile", "profiles.webfilter", "name", nameList(c.Profiles.WebFilter, func(x WebFilterProfile) string { return x.Name }), false},
		{"DNS filter profile", "profiles.dnsfilter", "name", nameList(c.Profiles.DNSFilter, func(x DNSFilterProfile) string { return x.Name }), false},
		{"app control profile", "profiles.appctrl", "name", nameList(c.Profiles.AppCtrl, func(x AppCtrlProfile) string { return x.Name }), false},
		{"admin", "admins", "username", nameList(c.Admins, func(x Admin) string { return x.Username }), false},
	}
	for _, k := range kinds {
		seen := make(map[string]int, len(k.names))
		for i, n := range k.names {
			p := fmt.Sprintf("%s[%d].%s", k.base, i, k.field)
			if !validName(n) {
				v.add(p, "invalid name %q: must be 1-63 characters of [A-Za-z0-9_.-]", n)
			}
			if k.referable && isWildcard(n) {
				v.add(p, "name %q is reserved: \"all\" and \"any\" mean match everything", n)
			}
			if j, dup := seen[n]; dup {
				v.add(p, "duplicate %s name (first used at %s[%d])", k.label, k.base, j)
			} else {
				seen[n] = i
			}
		}
	}

	// Policies refer to addresses and address groups (and services and
	// service groups) by bare name, so each pair shares one namespace.
	addrSet, svcSet := toSet(addrs), toSet(svcs)
	for i, n := range agroups {
		if addrSet[n] {
			v.add(fmt.Sprintf("address_groups[%d].name", i), "name %q is also used by an address", n)
		}
	}
	for i, n := range sgroups {
		if svcSet[n] {
			v.add(fmt.Sprintf("service_groups[%d].name", i), "name %q is also used by a service", n)
		}
	}
}

func (v *validator) checkFeedsAndAdmins() {
	for i, f := range v.cfg.Feeds {
		if !oneOf(f.Type, "ip", "domain", "url") {
			v.add(fmt.Sprintf("feeds[%d].type", i), "unknown type %q: want ip, domain or url", f.Type)
		}
		if !strings.HasPrefix(f.URL, "https://") && !strings.HasPrefix(f.URL, "http://") {
			v.add(fmt.Sprintf("feeds[%d].url", i), "invalid url %q: want http:// or https://", f.URL)
		}
		if f.BlockWAN && f.Type != "ip" {
			v.add(fmt.Sprintf("feeds[%d].block_wan", i), "block_wan needs an ip feed")
		}
		if !validFeedFormat(f.Format) {
			v.add(fmt.Sprintf("feeds[%d].format", i), "unknown format %q: want plain, csv:<column> or json:<path>", f.Format)
		}
		if f.RefreshMinutes != 0 && (f.RefreshMinutes < 15 || f.RefreshMinutes > 7*24*60) {
			v.add(fmt.Sprintf("feeds[%d].refresh_minutes", i), "refresh_minutes %d out of range 15-10080", f.RefreshMinutes)
		}
		if h := f.AuthHeader; h != "" {
			if name, val, ok := strings.Cut(h, ":"); !ok || strings.TrimSpace(name) == "" || strings.TrimSpace(val) == "" || strings.ContainsAny(h, "\r\n") {
				v.add(fmt.Sprintf("feeds[%d].auth_header", i), "auth_header must look like \"Name: value\" on one line")
			}
		}
	}
	for i, a := range v.cfg.Admins {
		if !oneOf(a.Role, "admin", "readonly") {
			v.add(fmt.Sprintf("admins[%d].role", i), "unknown role %q: want admin or readonly", a.Role)
		}
	}
	if len(v.cfg.Admins) == 0 && !v.cfg.AllowNoAdmin {
		v.add("admins", "at least one admin is required")
	}
}

// validFeedFormat accepts "", "plain", "csv:<column>" and "json:<path>"
// (the json path may be empty: the document itself is the list).
func validFeedFormat(f string) bool {
	switch {
	case f == "" || f == "plain":
		return true
	case strings.HasPrefix(f, "csv:"):
		return len(f) > len("csv:")
	case strings.HasPrefix(f, "json:"):
		return true
	}
	return false
}

func (v *validator) checkDNSFilters() {
	feedType := map[string]string{}
	for _, f := range v.cfg.Feeds {
		feedType[f.Name] = f.Type
	}
	for i, p := range v.cfg.Profiles.DNSFilter {
		path := fmt.Sprintf("profiles.dnsfilter[%d]", i)
		if !oneOf(p.Action, "", "nxdomain", "blockpage") {
			v.add(path+".action", "unknown action %q: want nxdomain or blockpage", p.Action)
		}
		for j, f := range p.BlockFeeds {
			switch t, ok := feedType[f]; {
			case !ok:
				v.add(fmt.Sprintf("%s.block_feeds[%d]", path, j), "unknown feed %q", f)
			case t != "domain":
				v.add(fmt.Sprintf("%s.block_feeds[%d]", path, j), "feed %q is a %s feed; DNS filters use domain feeds", f, t)
			}
		}
		for key, list := range map[string][]string{"block_domains": p.BlockDomains, "allow_domains": p.AllowDomains} {
			for j, d := range list {
				if h := strings.TrimPrefix(strings.TrimSuffix(d, "."), "*."); !validHostname(h) || !strings.Contains(h, ".") {
					v.add(fmt.Sprintf("%s.%s[%d]", path, key, j), "invalid domain %q", d)
				}
			}
		}
	}
}

var ipsCategoryRe = regexp.MustCompile(`^[A-Z0-9_]{1,32}$`)

func (v *validator) checkIPSProfiles() {
	for i, p := range v.cfg.Profiles.IPS {
		path := fmt.Sprintf("profiles.ips[%d]", i)
		if !oneOf(p.Mode, "", "prevent", "detect") {
			v.add(path+".mode", "unknown mode %q: want prevent or detect", p.Mode)
		}
		if p.BlockSeverity < 0 || p.BlockSeverity > 3 {
			v.add(path+".block_severity", "block_severity %d out of range 1-3", p.BlockSeverity)
		}
		for j, c := range p.Categories {
			if !ipsCategoryRe.MatchString(c) {
				v.add(fmt.Sprintf("%s.categories[%d]", path, j), "invalid category %q: upper-case ET category such as MALWARE", c)
			}
		}
		for j, sp := range p.Suppress {
			sp := sp
			sPath := fmt.Sprintf("%s.suppress[%d]", path, j)
			if sp.SID == 0 {
				v.add(sPath+".sid", "sid is required")
			}
			switch sp.Track {
			case "":
				if sp.IP != "" {
					v.add(sPath+".track", "an ip needs track by_src or by_dst")
				}
			case "by_src", "by_dst":
				if _, err := netip.ParsePrefix(sp.IP); err != nil {
					if _, err := netip.ParseAddr(sp.IP); err != nil {
						v.add(sPath+".ip", "invalid ip %q", sp.IP)
					}
				}
			default:
				v.add(sPath+".track", "unknown track %q: want by_src or by_dst", sp.Track)
			}
		}
	}
}

var wfCategoryRe = regexp.MustCompile(`^[a-z0-9_-]{1,40}$`)

func (v *validator) checkWebFilters() {
	for i, p := range v.cfg.Profiles.WebFilter {
		path := fmt.Sprintf("profiles.webfilter[%d]", i)
		for _, c := range sortedMapKeys(p.Categories) {
			if !wfCategoryRe.MatchString(c) {
				v.add(path+".categories."+c, "invalid category name %q", c)
			}
			if !oneOf(p.Categories[c], "allow", "monitor", "warn", "block") {
				v.add(path+".categories."+c, "unknown action %q: want allow, monitor, warn or block", p.Categories[c])
			}
		}
		for key, list := range map[string][]string{"block_domains": p.BlockDomains, "allow_domains": p.AllowDomains} {
			for j, d := range list {
				if h := strings.TrimPrefix(strings.TrimSuffix(d, "."), "*."); !validHostname(h) || !strings.Contains(h, ".") {
					v.add(fmt.Sprintf("%s.%s[%d]", path, key, j), "invalid domain %q", d)
				}
			}
		}
	}
}

func sortedMapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (v *validator) checkTailscale() {
	t := v.cfg.VPN.Tailscale
	const p = "vpn.tailscale"
	if t.Enabled {
		for _, in := range v.cfg.Interfaces {
			if in.Name == TailscaleInterface {
				v.add(p+".enabled", "an interface is already named %q", TailscaleInterface)
			}
		}
		for _, vl := range v.cfg.VLANs {
			if vl.Name == TailscaleInterface {
				v.add(p+".enabled", "a VLAN is already named %q", TailscaleInterface)
			}
		}
	}
	if t.Hostname != "" && !hostLabelRe.MatchString(t.Hostname) {
		v.add(p+".hostname", "invalid hostname %q: one DNS label", t.Hostname)
	}
	if t.LoginServer != "" && !strings.HasPrefix(t.LoginServer, "https://") {
		v.add(p+".login_server", "login_server must be an https:// URL")
	}
	static := map[string]bool{}
	wan := map[string]bool{}
	for _, in := range v.cfg.Interfaces {
		wan[in.Name] = in.Role == "wan"
		static[in.Name] = in.Role != "wan" && in.Mode == "static" && in.Address != nil
	}
	for _, vl := range v.cfg.VLANs {
		static[vl.Name] = !wan[vl.Parent] && vl.Mode == "static" && vl.Address != nil
	}
	for i, r := range t.AdvertiseRoutes {
		if !static[r] {
			v.add(fmt.Sprintf("%s.advertise_routes[%d]", p, i), "%q is not a non-WAN interface or VLAN with a static address", r)
		}
	}
	for i, a := range t.AllowAccess {
		if !oneOf(a, "ping", "https", "ssh") {
			v.add(fmt.Sprintf("%s.allow_access[%d]", p, i), "unknown access %q: want ping, https or ssh", a)
		}
	}
}

var wgNameRe = regexp.MustCompile(`^[a-z0-9_-]{1,15}$`)

func (v *validator) checkWireGuard() {
	taken := map[string]bool{TailscaleInterface: v.cfg.VPN.Tailscale.Enabled}
	for _, in := range v.cfg.Interfaces {
		taken[in.Name] = true
		taken[in.Device] = true
	}
	for _, vl := range v.cfg.VLANs {
		taken[vl.Name] = true
	}
	ports := map[int]bool{}
	for i, w := range v.cfg.VPN.WireGuard {
		p := fmt.Sprintf("vpn.wireguard[%d]", i)
		if !wgNameRe.MatchString(w.Name) {
			v.add(p+".name", "invalid name %q: 1-15 of a-z, 0-9, _ and -", w.Name)
		} else if taken[w.Name] {
			v.add(p+".name", "name %q is already used by another interface", w.Name)
		}
		taken[w.Name] = true
		port := w.ListenPort
		if port == 0 {
			port = DefaultWireGuardPort
		}
		if port < 1 || port > 65535 {
			v.add(p+".listen_port", "listen_port %d out of range", w.ListenPort)
		} else if ports[port] {
			v.add(p+".listen_port", "listen_port %d is used twice", port)
		}
		ports[port] = true
		if !w.Address.IsValid() || !w.Address.Addr().Is4() || w.Address.Bits() > 30 {
			v.add(p+".address", "address must be an IPv4 CIDR with room for clients, e.g. 10.66.0.1/24")
		}
		for j, a := range w.AllowAccess {
			if !oneOf(a, "ping", "https", "ssh") {
				v.add(fmt.Sprintf("%s.allow_access[%d]", p, j), "unknown access %q: want ping, https or ssh", a)
			}
		}
		seenKey, seenAddr, seenName := map[string]bool{}, map[netip.Addr]bool{}, map[string]bool{}
		for j, peer := range w.Peers {
			pp := fmt.Sprintf("%s.peers[%d]", p, j)
			if !validName(peer.Name) || seenName[peer.Name] {
				v.add(pp+".name", "peer names must be unique and 1-63 of [A-Za-z0-9_.-]")
			}
			seenName[peer.Name] = true
			if k, err := base64.StdEncoding.DecodeString(peer.PublicKey); err != nil || len(k) != 32 {
				v.add(pp+".public_key", "public_key must be a base64 WireGuard key")
			} else if seenKey[peer.PublicKey] {
				v.add(pp+".public_key", "public key used by two peers")
			}
			seenKey[peer.PublicKey] = true
			switch {
			case !peer.Address.Is4() || !w.Address.IsValid() || !w.Address.Masked().Contains(peer.Address):
				v.add(pp+".address", "address must be inside %s", w.Address.Masked())
			case peer.Address == w.Address.Addr() || seenAddr[peer.Address]:
				v.add(pp+".address", "address %s is already used", peer.Address)
			}
			seenAddr[peer.Address] = true
			if peer.Keepalive < 0 || peer.Keepalive > 3600 {
				v.add(pp+".keepalive", "keepalive must be 0-3600 seconds")
			}
		}
	}
}

func (v *validator) checkAppCtrl() {
	groups := map[string]bool{}
	for _, a := range appctrl.Catalog() {
		groups[a.Group] = true
	}
	for i, p := range v.cfg.Profiles.AppCtrl {
		path := fmt.Sprintf("profiles.appctrl[%d]", i)
		for _, g := range sortedMapKeys(p.Groups) {
			if !groups[g] {
				v.add(path+".groups."+g, "unknown application group %q", g)
			}
			if !oneOf(p.Groups[g], "allow", "monitor", "block") {
				v.add(path+".groups."+g, "unknown action %q: want allow, monitor or block", p.Groups[g])
			}
		}
		for _, id := range sortedMapKeys(p.Apps) {
			if !appctrl.Known(id) {
				v.add(path+".apps."+id, "unknown application %q", id)
			}
			if !oneOf(p.Apps[id], "allow", "monitor", "block") {
				v.add(path+".apps."+id, "unknown action %q: want allow, monitor or block", p.Apps[id])
			}
		}
	}
}
