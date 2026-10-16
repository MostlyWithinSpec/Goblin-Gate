// Package pfsense converts a pfSense backup (Diagnostics → Backup & Restore
// → config.xml, CE 2.7 / Plus 24.x) into a Goblin Gate candidate config plus
// a report of what was converted, approximated or skipped (task 061).
// Nothing is lost silently: every section the importer knows of ends up in
// the report, and unknown packages are listed too.
//
// Never imported: pfSense users' password hashes (different scheme),
// private keys and certificates.
package pfsense

import (
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// Report statuses.
const (
	Converted    = "converted"
	Approximated = "approximated" // converted with a difference the detail explains
	Skipped      = "skipped"      // not converted: the detail says why and what to do
)

// Entry is one line of the import report.
type Entry struct {
	Section string `json:"section"` // interfaces, rules, nat, dhcp, dns, system, ...
	Item    string `json:"item"`
	Status  string `json:"status"`
	Detail  string `json:"detail,omitempty"`
	Risk    bool   `json:"risk,omitempty"` // worth a second look (e.g. every port forwarded)
}

// InterfaceMap tells which pfSense interface became which Goblin interface
// or VLAN and which device of this box it uses. Device "" = not assigned yet
// (pfSense device names like em0 do not exist here).
type InterfaceMap struct {
	PfSense       string `json:"pfsense"`        // wan, lan, opt1
	PfSenseDevice string `json:"pfsense_device"` // em0, igb1.20
	Description   string `json:"description,omitempty"`
	Name          string `json:"name"` // Goblin interface or VLAN name
	Role          string `json:"role"`
	VLAN          bool   `json:"vlan,omitempty"`
	Device        string `json:"device"`
}

// Options steer an import.
type Options struct {
	// Base is the current config: admins, the admin port, update settings,
	// notifications and VPN settings are kept from it, and its WAN/LAN
	// devices are the default assignment for pfSense's wan and lan.
	Base *config.Config
	// Devices assigns devices of this box to pfSense interfaces (key: wan,
	// lan, opt1, ...), overriding the defaults.
	Devices map[string]string
}

// Result is an import: the candidate config, the report, the interface
// assignment and the candidate's validation errors (to fix before commit).
type Result struct {
	Config     *config.Config           `json:"-"`
	Report     []Entry                  `json:"report"`
	Interfaces []InterfaceMap           `json:"interfaces"`
	Errors     []config.ValidationError `json:"validation_errors,omitempty"`
	Summary    map[string]int           `json:"summary"` // per status
}

// importer carries the state of one conversion.
type importer struct {
	pf           *pfConfig
	opts         Options
	cfg          *config.Config
	report       []Entry
	ifmap        []InterfaceMap
	byPf         map[string]*InterfaceMap // pfSense interface name -> mapping
	byDev        map[string]string        // pfSense device -> Goblin name
	names        map[string]bool          // object names in use (addresses, groups, services)
	aliases      map[string]pfAlias
	feeds        map[string]string // url alias -> feed name
	schedOK      map[string]bool
	gatewayNames map[string]string // pfSense gateway/group -> multi-WAN name (task 049)
}

// Import converts config.xml.
func Import(data []byte, opts Options) (*Result, error) {
	var pf pfConfig
	if err := xml.Unmarshal(data, &pf); err != nil {
		return nil, fmt.Errorf("not a pfSense config.xml: %w", err)
	}
	if len(pf.Interfaces.Items) == 0 {
		return nil, fmt.Errorf("not a pfSense config.xml: no interfaces")
	}
	base := opts.Base
	if base == nil {
		base = config.Default()
	}
	cfg := config.Default()
	cfg.Admins = base.Admins
	cfg.System.AdminPort = base.System.AdminPort
	cfg.System.UpdateChannel = base.System.UpdateChannel
	cfg.System.UpdateURL = base.System.UpdateURL
	cfg.Notifications = base.Notifications
	cfg.VPN = base.VPN
	cfg.Policies, cfg.DHCP, cfg.Interfaces = nil, nil, nil
	cfg.Profiles = config.Profiles{}
	im := &importer{pf: &pf, opts: opts, cfg: cfg, byPf: map[string]*InterfaceMap{}, byDev: map[string]string{},
		names: map[string]bool{"all": true, "any": true}, aliases: map[string]pfAlias{}, feeds: map[string]string{}, schedOK: map[string]bool{}}
	im.opts.Base = base

	im.system()
	im.interfaces()
	im.schedules()
	im.aliasObjects()
	im.gateways() // before the rules: they may route through a gateway
	im.portForwards()
	im.filterRules()
	im.outboundNAT()
	im.dhcp()
	im.dns()
	im.upnp()
	im.ids()
	im.pfBlocker()
	im.unsupported()

	res := &Result{Config: cfg, Report: im.report, Interfaces: im.ifmap, Summary: map[string]int{}}
	for _, e := range im.report {
		res.Summary[e.Status]++
	}
	res.Errors = config.Validate(cfg)
	return res, nil
}

func (im *importer) add(section, item, status, detail string, args ...any) {
	if len(args) > 0 {
		detail = fmt.Sprintf(detail, args...)
	}
	im.report = append(im.report, Entry{Section: section, Item: item, Status: status, Detail: detail})
}

func (im *importer) risk(section, item, status, detail string, args ...any) {
	im.add(section, item, status, detail, args...)
	im.report[len(im.report)-1].Risk = true
}

var slugRe = regexp.MustCompile(`[^a-z0-9_.-]+`)

// slug turns a description into a config name (letters, digits, . _ -).
func slug(s, fallback string) string {
	s = strings.Trim(slugRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-"), "-.")
	if len(s) > 48 {
		s = strings.TrimRight(s[:48], "-.")
	}
	if s == "" || s == "all" || s == "any" {
		return fallback
	}
	return s
}

// unique returns name, or name-2, name-3 ... not yet in used, and marks it used.
func unique(used map[string]bool, name string) string {
	n := name
	for i := 2; used[n]; i++ {
		n = fmt.Sprintf("%s-%d", name, i)
	}
	used[n] = true
	return n
}

// ---- system ----

// legacyZones maps pfSense time zone names Go's tzdata lacks.
var legacyZones = map[string]string{
	"EST": "America/New_York", "EST5EDT": "America/New_York", "CST6CDT": "America/Chicago",
	"MST": "America/Phoenix", "MST7MDT": "America/Denver", "PST8PDT": "America/Los_Angeles",
	"HST": "Pacific/Honolulu", "CET": "Europe/Paris", "EET": "Europe/Athens", "WET": "Europe/Lisbon",
	"MET": "Europe/Paris", "GMT": "UTC", "UCT": "UTC", "Zulu": "UTC", "Universal": "UTC",
}

func (im *importer) system() {
	s, sys := im.pf.System, &im.cfg.System
	if s.Hostname != "" {
		sys.Hostname = s.Hostname
		im.add("system", "hostname", Converted, s.Hostname)
	}
	if s.Domain != "" {
		sys.Domain = s.Domain
		im.add("system", "domain", Converted, s.Domain)
	}
	if tz := s.Timezone; tz != "" {
		// legacy names first: Go knows "EST" too, but as a fixed UTC-5
		// without daylight saving, which is rarely what was meant
		if m, ok := legacyZones[tz]; ok {
			sys.Timezone = m
			im.add("system", "time zone", Approximated, "%s is an old zone name (a fixed offset without daylight saving time): using %s, check it on the System page", tz, m)
		} else if _, err := time.LoadLocation(tz); err == nil && !strings.Contains(tz, " ") {
			sys.Timezone = tz
			im.add("system", "time zone", Converted, tz)
		} else {
			im.add("system", "time zone", Skipped, "unknown time zone %q: set it on the System page", tz)
		}
	}
	// pfSense's servers replace the defaults; none keeps the defaults
	if ts := strings.Fields(s.TimeServers); len(ts) > 0 {
		sys.NTPServers = ts
		im.add("system", "NTP servers", Converted, strings.Join(ts, " "))
	}
	var dnsServers []netip.Addr
	for _, d := range s.DNSServers {
		if a, err := netip.ParseAddr(d); err == nil {
			dnsServers = append(dnsServers, a)
		}
	}
	if len(dnsServers) > 0 {
		sys.DNSServers = dnsServers
		im.add("system", "DNS servers", Converted, "%d server(s)", len(dnsServers))
	}
	keys := 0
	for _, u := range s.Users {
		if u.Disabled != nil || u.AuthorizedKeys == "" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(u.AuthorizedKeys)
		if err != nil {
			im.add("system", "user "+u.Name, Skipped, "SSH keys are not valid base64")
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if config.CheckSSHKey(line) != nil {
				im.add("system", "user "+u.Name, Skipped, "an SSH key line was not understood (options or an unsupported type)")
				continue
			}
			keys++
			sys.SSHKeys = append(sys.SSHKeys, config.SSHKey{Name: unique(map[string]bool{}, slug(u.Name, "user")+"-"+strconv.Itoa(keys)), Key: line})
		}
	}
	if keys > 0 {
		im.add("system", "SSH keys", Converted, "%d public key(s) of pfSense users may log in as root over SSH", keys)
	}
	if n := len(s.Users); n > 0 {
		im.add("system", "users", Skipped, "%d pfSense user(s) not imported: their password hashes use another scheme. Your Goblin Gate admins stay as they are", n)
	}
}

// ---- interfaces and VLANs ----

func (im *importer) interfaces() {
	vlanByDev := map[string]pfVLAN{}
	for _, v := range im.pf.VLANs {
		dev := v.VLANIf
		if dev == "" {
			dev = fmt.Sprintf("%s.%d", v.If, v.Tag)
		}
		vlanByDev[dev] = v
	}
	baseDev := map[string]string{}
	for _, i := range im.opts.Base.Interfaces {
		if i.Role == "wan" && baseDev["wan"] == "" {
			baseDev["wan"] = i.Device
		}
		if i.Role == "lan" && baseDev["lan"] == "" {
			baseDev["lan"] = i.Device
		}
	}
	used := map[string]bool{}
	// physical interfaces first, VLANs after (they need their parent)
	var vlanIfs []pfInterface
	for _, p := range im.pf.Interfaces.Items {
		if _, isVLAN := vlanByDev[p.If]; isVLAN {
			vlanIfs = append(vlanIfs, p)
			continue
		}
		key := p.XMLName.Local
		role := "lan"
		if key == "wan" || p.IPAddr == "dhcp" || p.IPAddr == "pppoe" || p.Gateway != "" {
			role = "wan"
		}
		name := unique(used, slug(p.Descr, key))
		dev := baseDev[key]
		if d, ok := im.opts.Devices[key]; ok {
			dev = d
		}
		m := &InterfaceMap{PfSense: key, PfSenseDevice: p.If, Description: p.Descr, Name: name, Role: role, Device: dev}
		im.ifmap = append(im.ifmap, *m)
		im.byPf[key] = &im.ifmap[len(im.ifmap)-1]
		im.byDev[p.If] = name
		devOut := dev
		if devOut == "" {
			devOut = p.If // placeholder: assign a real device before committing
		}
		ifc := config.Interface{Name: name, Device: devOut, Role: role, Enabled: p.Enable != nil, Description: p.Descr}
		im.addressing(p, key, name, &ifc.Mode, &ifc.Address, &ifc.Gateway, &ifc.MTU, &ifc.IPv6)
		if role == "lan" {
			ifc.AllowAccess = []string{"ping", "https", "ssh"}
		}
		im.cfg.Interfaces = append(im.cfg.Interfaces, ifc)
		im.ifaceReport(p, key, name, dev)
	}
	// re-point the map entries (append may have moved the slice)
	for i := range im.ifmap {
		im.byPf[im.ifmap[i].PfSense] = &im.ifmap[i]
	}
	for _, p := range vlanIfs {
		v := vlanByDev[p.If]
		key := p.XMLName.Local
		parent, ok := im.byDev[v.If]
		if !ok {
			// VLAN on an unassigned NIC: add the NIC as an interface without addresses
			parent = unique(used, slug(v.If, "nic"))
			dev := ""
			if d, ok := im.opts.Devices[v.If]; ok {
				dev = d
			}
			devOut := dev
			if devOut == "" {
				devOut = v.If
			}
			im.cfg.Interfaces = append(im.cfg.Interfaces, config.Interface{Name: parent, Device: devOut, Role: "lan", Mode: "none", Enabled: true})
			im.byDev[v.If] = parent
			im.ifmap = append(im.ifmap, InterfaceMap{PfSense: v.If, PfSenseDevice: v.If, Name: parent, Role: "lan", Device: dev})
			im.add("interfaces", v.If, Approximated, "pfSense carries VLANs on %s without assigning it: added as interface %q without addresses", v.If, parent)
		}
		name := unique(used, slug(p.Descr, key))
		role := "lan"
		if key == "wan" || p.IPAddr == "dhcp" || p.IPAddr == "pppoe" {
			role = "wan"
		}
		vl := config.VLAN{Name: name, Parent: parent, VLANID: v.Tag, Enabled: p.Enable != nil, Description: p.Descr}
		im.addressing(p, key, name, &vl.Mode, &vl.Address, &vl.Gateway, &vl.MTU, &vl.IPv6)
		if role == "lan" {
			vl.AllowAccess = []string{"ping"}
		}
		im.cfg.VLANs = append(im.cfg.VLANs, vl)
		im.ifmap = append(im.ifmap, InterfaceMap{PfSense: key, PfSenseDevice: p.If, Description: p.Descr, Name: name, Role: role, VLAN: true, Device: "(VLAN " + strconv.Itoa(v.Tag) + " on " + parent + ")"})
		im.byDev[p.If] = name
		im.add("interfaces", key, Converted, "VLAN %d on %s → %q", v.Tag, parent, name)
	}
	for i := range im.ifmap {
		im.byPf[im.ifmap[i].PfSense] = &im.ifmap[i]
	}
	for dev, v := range vlanByDev {
		if _, ok := im.byDev[dev]; !ok {
			im.add("interfaces", "VLAN "+strconv.Itoa(v.Tag), Skipped, "VLAN %d on %s is defined but not assigned to an interface in pfSense", v.Tag, v.If)
		}
	}
	im.track6Uplinks()
}

// track6Uplinks gives an interface that LAN-side interfaces track (track6)
// but that has no IPv6 setting itself DHCPv6 with prefix delegation:
// tracking needs a delegated prefix to take from.
func (im *importer) track6Uplinks() {
	need := map[string]bool{}
	for _, i := range im.cfg.Interfaces {
		if i.IPv6 != nil && i.IPv6.Mode == "delegated" && i.IPv6.Uplink != "" {
			need[i.IPv6.Uplink] = true
		}
	}
	for _, v := range im.cfg.VLANs {
		if v.IPv6 != nil && v.IPv6.Mode == "delegated" && v.IPv6.Uplink != "" {
			need[v.IPv6.Uplink] = true
		}
	}
	for i := range im.cfg.Interfaces {
		ifc := &im.cfg.Interfaces[i]
		if need[ifc.Name] && (ifc.IPv6 == nil || ifc.IPv6.Mode == "none" || ifc.IPv6.Mode == "") {
			ifc.IPv6 = &config.IPv6{Mode: "dhcp6"}
			im.add("interfaces", ifc.Name, Approximated, "LAN interfaces track %q for IPv6 but it had no IPv6 setting: set to DHCPv6 with prefix delegation (/%d)", ifc.Name, config.DefaultPDSize)
		}
	}
}

func (im *importer) ifaceReport(p pfInterface, key, name, dev string) {
	what := fmt.Sprintf("%s (%s) → %q", key, p.If, name)
	if dev == "" {
		im.add("interfaces", key, Approximated, "%s: choose which network port of this box it uses (pfSense called it %s)", what, p.If)
	} else {
		im.add("interfaces", key, Converted, "%s on %s", what, dev)
	}
	if p.SpoofMAC != "" {
		im.add("interfaces", key, Skipped, "MAC address spoofing (%s) is not supported yet", p.SpoofMAC)
	}
	if p.BlockBogons != nil || p.BlockPriv != nil {
		im.add("interfaces", key, Skipped, "\"block private/bogon networks\" has no switch here: Goblin Gate drops unsolicited WAN traffic anyway; add a bogon threat feed for the rest")
	}
}

// addressing converts IPv4 and IPv6 addressing of an interface.
func (im *importer) addressing(p pfInterface, key, name string, mode *string, addr **netip.Prefix, gw **netip.Addr, mtu *int, v6 **config.IPv6) {
	switch {
	case p.IPAddr == "dhcp":
		*mode = "dhcp"
	case p.IPAddr == "pppoe":
		*mode = "none"
		im.risk("interfaces", key, Skipped, "PPPoE is not supported yet: %q has no IPv4 now", name)
	case p.IPAddr == "":
		*mode = "none"
	default:
		bits, _ := strconv.Atoi(p.Subnet)
		a, err := netip.ParseAddr(p.IPAddr)
		if err != nil || bits <= 0 || bits > 32 {
			*mode = "none"
			im.add("interfaces", key, Skipped, "address %s/%s not understood", p.IPAddr, p.Subnet)
			break
		}
		pf := netip.PrefixFrom(a, bits)
		*mode, *addr = "static", &pf
		if g := im.gatewayIP(p.Gateway); g.IsValid() {
			*gw = &g
		}
	}
	if m, err := strconv.Atoi(p.MTU); err == nil && m > 0 {
		*mtu = m
	}
	switch p.IPAddrV6 {
	case "":
	case "dhcp6":
		size := 0
		if n, err := strconv.Atoi(p.PDLen); err == nil && n >= 0 && n <= 16 {
			size = 64 - n
		}
		*v6 = &config.IPv6{Mode: "dhcp6", PDSize: size}
		im.add("interfaces", key, Converted, "IPv6 by DHCPv6 with prefix delegation")
	case "slaac":
		*v6 = &config.IPv6{Mode: "dhcp6"}
		im.add("interfaces", key, Approximated, "IPv6 SLAAC → dhcp6 (SLAAC plus DHCPv6 prefix delegation)")
	case "track6":
		up := ""
		if m, ok := im.byPf[p.Track6Interface]; ok {
			up = m.Name
		}
		id, _ := strconv.Atoi(p.Track6PrefixID)
		*v6 = &config.IPv6{Mode: "delegated", Uplink: up, PrefixID: id}
		im.add("interfaces", key, Converted, "IPv6 track interface %s (prefix id %d) → delegated", p.Track6Interface, id)
	default:
		a, err := netip.ParseAddr(p.IPAddrV6)
		bits, _ := strconv.Atoi(p.SubnetV6)
		if err != nil || !a.Is6() || bits <= 0 || bits > 128 {
			im.add("interfaces", key, Skipped, "IPv6 mode %q is not supported", p.IPAddrV6)
			break
		}
		pfx := netip.PrefixFrom(a, bits)
		*v6 = &config.IPv6{Mode: "static", Address: &pfx}
		if g := im.gatewayIP(p.GatewayV6); g.IsValid() && g.Is6() {
			(*v6).Gateway = &g
		}
		im.add("interfaces", key, Converted, "static IPv6 %s", pfx)
	}
}

// gatewayIP resolves a pfSense gateway name to its address.
func (im *importer) gatewayIP(name string) netip.Addr {
	for _, g := range im.pf.Gateways.Items {
		if g.Name == name {
			a, _ := netip.ParseAddr(g.Gateway)
			return a
		}
	}
	return netip.Addr{}
}

// ifName maps a pfSense interface key (wan, lan, opt1) to a Goblin name.
func (im *importer) ifName(key string) (string, bool) {
	if m, ok := im.byPf[key]; ok {
		return m.Name, true
	}
	return "", false
}

// ifPrefix returns the IPv4 subnet of a Goblin interface or VLAN.
func (im *importer) ifPrefix(name string) (netip.Prefix, bool) {
	for _, i := range im.cfg.Interfaces {
		if i.Name == name && i.Address != nil {
			return *i.Address, true
		}
	}
	for _, v := range im.cfg.VLANs {
		if v.Name == name && v.Address != nil {
			return *v.Address, true
		}
	}
	return netip.Prefix{}, false
}

func (im *importer) isWAN(name string) bool {
	for _, m := range im.ifmap {
		if m.Name == name {
			return m.Role == "wan"
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
