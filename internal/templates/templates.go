// Package templates turns common setups into a few questions: a guest or
// IoT network, a port forward only some places may use, a network sent
// through a VPN provider, a bedtime for the kids' devices. A template
// changes a copy of the candidate config; the caller previews the diff and
// saves it, and nothing goes live until the admin commits.
package templates

import (
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// Input types.
const (
	TypeName      = "name"      // a new object name: lowercase letters, digits, "-"
	TypeInterface = "interface" // one of Options (interfaces and VLANs)
	TypeTunnel    = "tunnel"    // one of Options (VPN provider tunnels)
	TypeProfile   = "profile"   // one of Options, may be empty (a DNS filter profile)
	TypeVLANID    = "vlan_id"   // 1-4094
	TypeCIDR      = "cidr"      // the box's address with the prefix, e.g. 192.168.50.1/24
	TypeIP        = "ip"        // one IPv4 or IPv6 address
	TypeIPs       = "ips"       // addresses separated by commas or spaces
	TypePort      = "port"      // 1-65535
	TypeProtocol  = "protocol"  // tcp | udp
	TypeSources   = "sources"   // two-letter country codes and CIDRs, comma separated
	TypeDays      = "days"      // mon..sun, comma separated
	TypeTime      = "time"      // HH:MM
	TypeBool      = "bool"      // "true" | "false"
)

// Input is one question a template asks.
type Input struct {
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Type     string   `json:"type"`
	Help     string   `json:"help,omitempty"`
	Default  string   `json:"default,omitempty"`
	Required bool     `json:"required"`
	Options  []string `json:"options,omitempty"` // for interface, tunnel and profile
}

// Info describes a template for a list.
type Info struct {
	ID          string  `json:"id"`
	Version     int     `json:"version"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Inputs      []Input `json:"inputs"`
	// Unavailable says why the template can't be used with this config
	// (e.g. no VPN provider yet); "" when it can.
	Unavailable string `json:"unavailable,omitempty"`
}

// InputError is a problem with one answer.
type InputError struct {
	Input   string `json:"input"`
	Message string `json:"message"`
}

// InputErrors is returned by Apply when answers are wrong.
type InputErrors []InputError

func (e InputErrors) Error() string {
	var parts []string
	for _, x := range e {
		parts = append(parts, x.Input+": "+x.Message)
	}
	return strings.Join(parts, "; ")
}

// Template is one setup.
type Template struct {
	ID          string
	Version     int
	Title       string
	Description string
	inputs      func(c *config.Config) []Input
	unavailable func(c *config.Config) string
	apply       func(c *config.Config, a *answers)
}

// List returns every template with its inputs for cfg.
func List(cfg *config.Config) []Info {
	out := make([]Info, 0, len(all))
	for _, t := range all {
		out = append(out, t.Info(cfg))
	}
	return out
}

// Get finds a template by id.
func Get(id string) (*Template, bool) {
	for _, t := range all {
		if t.ID == id {
			return t, true
		}
	}
	return nil, false
}

// Info describes t for cfg.
func (t *Template) Info(cfg *config.Config) Info {
	i := Info{ID: t.ID, Version: t.Version, Title: t.Title, Description: t.Description, Inputs: t.inputs(cfg)}
	if t.unavailable != nil {
		i.Unavailable = t.unavailable(cfg)
	}
	return i
}

// Apply returns a copy of cfg with the template applied for values. Wrong
// answers come back as InputErrors; the result can still fail config
// validation, which the caller checks.
func (t *Template) Apply(cfg *config.Config, values map[string]string) (*config.Config, error) {
	if t.unavailable != nil {
		if why := t.unavailable(cfg); why != "" {
			return nil, InputErrors{{Input: "", Message: why}}
		}
	}
	out := cfg.Clone()
	a := &answers{cfg: out, values: values, inputs: t.inputs(cfg)}
	for k := range values {
		if !slices.ContainsFunc(a.inputs, func(in Input) bool { return in.Name == k }) {
			a.fail(k, "unknown input")
		}
	}
	if len(a.errs) == 0 {
		t.apply(out, a)
	}
	if len(a.errs) > 0 {
		return nil, a.errs
	}
	return out, nil
}

// answers reads typed values and collects errors.
type answers struct {
	cfg    *config.Config
	values map[string]string
	inputs []Input
	errs   InputErrors
}

func (a *answers) fail(input, format string, args ...any) {
	a.errs = append(a.errs, InputError{Input: input, Message: fmt.Sprintf(format, args...)})
}

// raw returns the trimmed answer or the input's default; it reports a
// missing required answer.
func (a *answers) raw(name string) string {
	var in Input
	for _, x := range a.inputs {
		if x.Name == name {
			in = x
		}
	}
	v := strings.TrimSpace(a.values[name])
	if v == "" {
		v = in.Default
	}
	if v == "" && in.Required {
		a.fail(name, "required")
	}
	if v != "" && len(in.Options) > 0 && !slices.Contains(in.Options, v) {
		a.fail(name, "not one of %s", strings.Join(in.Options, ", "))
	}
	return v
}

var newNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,14}$`)

// name is a new object name; taken lists the names it must not clash with.
func (a *answers) name(input string, taken ...[]string) string {
	v := a.raw(input)
	if v == "" {
		return ""
	}
	if !newNameRe.MatchString(v) {
		a.fail(input, "use 1-15 lowercase letters, digits or \"-\", starting with a letter")
		return v
	}
	for _, list := range taken {
		if slices.Contains(list, v) {
			a.fail(input, "%q is already used; pick another name or remove the old one first", v)
		}
	}
	return v
}

func (a *answers) int(input string, lo, hi int) int {
	v := a.raw(input)
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < lo || n > hi {
		a.fail(input, "want a number from %d to %d", lo, hi)
	}
	return n
}

func (a *answers) prefix(input string) netip.Prefix {
	v := a.raw(input)
	if v == "" {
		return netip.Prefix{}
	}
	p, err := netip.ParsePrefix(v)
	if err != nil || !p.Addr().Is4() || p.Bits() < 16 || p.Bits() > 28 {
		a.fail(input, "want the box's IPv4 address with a /16 to /28 prefix, e.g. 192.168.50.1/24")
		return netip.Prefix{}
	}
	if p.Addr() == p.Masked().Addr() {
		a.fail(input, "%s is the network address; give the box's own address, e.g. %s", p.Addr(), p.Addr().Next())
	}
	return p
}

func (a *answers) ips(input string) []netip.Addr {
	var out []netip.Addr
	for _, f := range strings.FieldsFunc(a.raw(input), func(r rune) bool { return r == ',' || r == ' ' }) {
		ip, err := netip.ParseAddr(f)
		if err != nil {
			a.fail(input, "%q is not an IP address", f)
			continue
		}
		out = append(out, ip)
	}
	return out
}

var dayNames = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

func (a *answers) days(input string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(strings.ToLower(a.raw(input)), func(r rune) bool { return r == ',' || r == ' ' }) {
		if !slices.Contains(dayNames, f) {
			a.fail(input, "%q is not a day (mon, tue, wed, thu, fri, sat, sun)", f)
			continue
		}
		if !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out
}

var timeRe = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

func (a *answers) time(input string) string {
	v := a.raw(input)
	if v != "" && !timeRe.MatchString(v) {
		a.fail(input, "want a time like 21:30")
	}
	return v
}

// sources splits country codes from CIDRs.
func (a *answers) sources(input string) (countries []string, nets []netip.Prefix) {
	for _, f := range strings.FieldsFunc(a.raw(input), func(r rune) bool { return r == ',' || r == ' ' }) {
		if len(f) == 2 && strings.ToUpper(f) == f && f[0] >= 'A' && f[0] <= 'Z' && f[1] >= 'A' && f[1] <= 'Z' {
			countries = append(countries, f)
			continue
		}
		if p, err := netip.ParsePrefix(f); err == nil {
			nets = append(nets, p.Masked())
			continue
		}
		if ip, err := netip.ParseAddr(f); err == nil {
			nets = append(nets, netip.PrefixFrom(ip, ip.BitLen()))
			continue
		}
		a.fail(input, "%q is neither a two-letter country code (US, DE) nor a network (203.0.113.0/24)", f)
	}
	return countries, nets
}

// --- config helpers -------------------------------------------------------

func netifNames(c *config.Config) []string {
	var out []string
	for _, i := range c.Interfaces {
		out = append(out, i.Name)
	}
	for _, v := range c.VLANs {
		out = append(out, v.Name)
	}
	for _, w := range c.VPN.WireGuardClients {
		out = append(out, w.Name)
	}
	return out
}

func roleOf(c *config.Config, name string) string {
	for _, i := range c.Interfaces {
		if i.Name == name {
			return i.Role
		}
	}
	for _, v := range c.VLANs {
		if v.Name == name {
			return roleOf(c, v.Parent)
		}
	}
	return ""
}

func wans(c *config.Config) []string {
	var out []string
	for _, i := range c.Interfaces {
		if i.Role == "wan" && i.Enabled {
			out = append(out, i.Name)
		}
	}
	return out
}

// inside lists the enabled interfaces and VLANs that are not WANs.
func inside(c *config.Config) []string {
	var out []string
	for _, i := range c.Interfaces {
		if i.Role != "wan" && i.Enabled {
			out = append(out, i.Name)
		}
	}
	for _, v := range c.VLANs {
		if roleOf(c, v.Parent) != "wan" && v.Enabled {
			out = append(out, v.Name)
		}
	}
	return out
}

// physicalInside lists interfaces a VLAN can be added to.
func physicalInside(c *config.Config) []string {
	var out []string
	for _, i := range c.Interfaces {
		if i.Role != "wan" && i.Enabled {
			out = append(out, i.Name)
		}
	}
	return out
}

func addressNames(c *config.Config) []string {
	var out []string
	for _, a := range c.Addresses {
		out = append(out, a.Name)
	}
	for _, g := range c.AddressGroups {
		out = append(out, g.Name)
	}
	return out
}

func policyNames(c *config.Config) []string {
	var out []string
	for _, p := range c.Policies {
		out = append(out, p.Name)
	}
	return out
}

func nextPolicyID(c *config.Config) uint32 {
	var id uint32
	for _, p := range c.Policies {
		id = max(id, p.ID)
	}
	return id + 1
}

// addPolicy puts p first: a template's policy is about traffic no other
// policy was written for (a new network) or must win over the existing
// ones (a bedtime deny, a VPN route).
func addPolicy(c *config.Config, p config.Policy) {
	p.ID = nextPolicyID(c)
	p.Enabled = true
	c.Policies = slices.Insert(c.Policies, 0, p)
}

// dhcpRange picks a pool in the middle of the network, away from the
// box's own address: .100-.199 on a /24.
func dhcpRange(box netip.Prefix) (netip.Addr, netip.Addr, bool) {
	if !box.IsValid() || !box.Addr().Is4() {
		return netip.Addr{}, netip.Addr{}, false
	}
	size := uint32(1) << (32 - box.Bits())
	base := box.Masked().Addr().As4()
	n := uint32(base[0])<<24 | uint32(base[1])<<16 | uint32(base[2])<<8 | uint32(base[3])
	start := n + max(2, size*100/256)
	end := min(start+99, n+size-2)
	addr := func(x uint32) netip.Addr {
		return netip.AddrFrom4([4]byte{byte(x >> 24), byte(x >> 16), byte(x >> 8), byte(x)})
	}
	b4 := box.Addr().As4()
	boxN := uint32(b4[0])<<24 | uint32(b4[1])<<16 | uint32(b4[2])<<8 | uint32(b4[3])
	if end < start || (boxN >= start && boxN <= end) {
		return netip.Addr{}, netip.Addr{}, false
	}
	return addr(start), addr(end), true
}

func dnsFilterNames(c *config.Config) []string {
	var out []string
	for _, p := range c.Profiles.DNSFilter {
		out = append(out, p.Name)
	}
	return out
}

func tunnelNames(c *config.Config) []string {
	var out []string
	for _, w := range c.VPN.WireGuardClients {
		out = append(out, w.Name)
	}
	return out
}
