package config

import (
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

// RADIUS lets switches and Wi-Fi access points ask the box who may connect
// and which VLAN a device belongs in (FreeRADIUS, configured by goblind):
// MAC authentication (MAB) for printers and TVs, 802.1X (PEAP with a local
// user's password, or EAP-TLS with a certificate from the internal CA) for
// laptops and phones. A device's VLAN comes from the device inventory (set
// at run time, no commit) or else from its 802.1X user's group.
type RADIUS struct {
	Enabled bool           `json:"enabled"`
	Clients []RADIUSClient `json:"clients,omitempty"`
	// MAB accepts MAC authentication requests (the user name is the MAC).
	MAB bool `json:"mab,omitempty"`
	// Dot1X accepts 802.1X (EAP-PEAP and EAP-TLS); it needs the internal CA,
	// which issues the server certificate.
	Dot1X bool `json:"dot1x,omitempty"`
	// Unknown is what a device without a VLAN of its own gets over MAB:
	// "reject" (default), "quarantine" (QuarantineVLAN) or "accept" (no VLAN
	// attribute: the port's own VLAN).
	Unknown        string        `json:"unknown,omitempty"`
	QuarantineVLAN string        `json:"quarantine_vlan,omitempty"` // a VLAN name
	Groups         []RADIUSGroup `json:"groups,omitempty"`
	Users          []RADIUSUser  `json:"users,omitempty"`
}

// RADIUSClient is a switch or access point that sends requests.
type RADIUSClient struct {
	Name    string `json:"name"`
	Address string `json:"address"` // IP or CIDR
	Secret  string `json:"secret,omitempty"`
	// CoAPort is where the client takes Disconnect requests (0 = 3799).
	CoAPort     int    `json:"coa_port,omitempty"`
	Description string `json:"description,omitempty"`
}

// RADIUSGroup gives its 802.1X users a VLAN ("" = the port's own).
type RADIUSGroup struct {
	Name string `json:"name"`
	VLAN string `json:"vlan,omitempty"`
}

// RADIUSUser is a local 802.1X user (PEAP-MSCHAPv2 password; for EAP-TLS the
// certificate's common name).
type RADIUSUser struct {
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	Password string `json:"password,omitempty"`
	Group    string `json:"group,omitempty"`
}

// RADIUS ports and defaults.
const (
	RADIUSAuthPort    = 1812
	RADIUSAcctPort    = 1813
	DefaultCoAPort    = 3799
	minRADIUSSecret   = 8
	minRADIUSPassword = 8
)

// CoA returns the client's Disconnect port.
func (c RADIUSClient) CoA() int {
	if c.CoAPort == 0 {
		return DefaultCoAPort
	}
	return c.CoAPort
}

// Prefix is the client's address as a prefix (a bare IP is a /32 or /128).
func (c RADIUSClient) Prefix() (netip.Prefix, bool) {
	if p, err := netip.ParsePrefix(c.Address); err == nil {
		return p.Masked(), true
	}
	if a, err := netip.ParseAddr(c.Address); err == nil {
		return netip.PrefixFrom(a, a.BitLen()), true
	}
	return netip.Prefix{}, false
}

// VLANID returns the 802.1Q id of a VLAN by name (0 when there is none).
func (c *Config) VLANID(name string) int {
	for _, v := range c.VLANs {
		if v.Name == name {
			return v.VLANID
		}
	}
	return 0
}

// RADIUSActive: RADIUS is on and has at least one client.
func (c *Config) RADIUSActive() bool {
	return c.RADIUS.Enabled && len(c.RADIUS.Clients) > 0 && (c.RADIUS.MAB || c.RADIUS.Dot1X)
}

var (
	radiusNameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,31}$`)
	radiusUserRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@+-]{0,63}$`)
)

// secretOK: printable ASCII without quotes or backslashes (the secret goes
// into FreeRADIUS's configuration).
func secretOK(s string) bool {
	for _, r := range s {
		if r < 0x21 || r > 0x7e || r == '"' || r == '\'' || r == '\\' || r == '`' {
			return false
		}
	}
	return true
}

func (v *validator) checkRADIUS() {
	r := v.cfg.RADIUS
	const p = "radius"
	if r.Enabled && len(r.Clients) == 0 {
		v.add(p+".clients", "add the switches and access points that may ask")
	}
	if r.Enabled && !r.MAB && !r.Dot1X {
		v.add(p+".mab", "turn on MAC authentication, 802.1X or both")
	}
	if r.Dot1X && !v.cfg.InternalCA.Enabled {
		v.add(p+".dot1x", "802.1X needs the internal CA (System → Internal CA): it issues the server certificate")
	}
	if !oneOf(r.Unknown, "", "reject", "quarantine", "accept") {
		v.add(p+".unknown", "unknown choice %q: want reject, quarantine or accept", r.Unknown)
	}
	vlanOK := func(path, name string) {
		if name != "" && v.cfg.VLANID(name) == 0 {
			v.add(path, "unknown VLAN %q", name)
		}
	}
	if r.Unknown == "quarantine" && r.QuarantineVLAN == "" {
		v.add(p+".quarantine_vlan", "pick the quarantine VLAN")
	}
	vlanOK(p+".quarantine_vlan", r.QuarantineVLAN)
	names := map[string]bool{}
	for i, c := range r.Clients {
		cp := p + ".clients[" + strconv.Itoa(i) + "]"
		if !radiusNameRe.MatchString(c.Name) {
			v.add(cp+".name", "invalid name %q: 1-32 of A-Z, a-z, 0-9, _ . -, starting with a letter", c.Name)
		} else if names[c.Name] {
			v.add(cp+".name", "duplicate client %q", c.Name)
		}
		names[c.Name] = true
		if _, ok := c.Prefix(); !ok {
			v.add(cp+".address", "invalid address %q: want an IP or a CIDR, e.g. 192.168.1.2 or 192.168.10.0/24", c.Address)
		}
		if len(c.Secret) < minRADIUSSecret || !secretOK(c.Secret) {
			v.add(cp+".secret", "want a shared secret of at least %d characters, without spaces, quotes or backslashes", minRADIUSSecret)
		}
		if c.CoAPort < 0 || c.CoAPort > 65535 {
			v.add(cp+".coa_port", "want 1-65535, or 0 for 3799")
		}
	}
	groups := map[string]bool{}
	for i, g := range r.Groups {
		gp := p + ".groups[" + strconv.Itoa(i) + "]"
		if !radiusNameRe.MatchString(g.Name) {
			v.add(gp+".name", "invalid name %q", g.Name)
		} else if groups[g.Name] {
			v.add(gp+".name", "duplicate group %q", g.Name)
		}
		groups[g.Name] = true
		vlanOK(gp+".vlan", g.VLAN)
	}
	users := map[string]bool{}
	for i, u := range r.Users {
		up := p + ".users[" + strconv.Itoa(i) + "]"
		if !radiusUserRe.MatchString(u.Name) {
			v.add(up+".name", "invalid user name %q: 1-64 of letters, digits and _ . @ + -", u.Name)
		} else if users[strings.ToLower(u.Name)] {
			v.add(up+".name", "duplicate user %q", u.Name)
		}
		users[strings.ToLower(u.Name)] = true
		if u.Enabled && len(u.Password) < minRADIUSPassword {
			v.add(up+".password", "want a password of at least %d characters", minRADIUSPassword)
		}
		if u.Group != "" && !groups[u.Group] {
			v.add(up+".group", "unknown group %q", u.Group)
		}
	}
}

// RADIUSUserByName finds an enabled user (names are case-insensitive).
func (r RADIUS) RADIUSUserByName(name string) (RADIUSUser, bool) {
	for _, u := range r.Users {
		if u.Enabled && strings.EqualFold(u.Name, name) {
			return u, true
		}
	}
	return RADIUSUser{}, false
}

// GroupVLAN returns the VLAN of a group ("" when none).
func (r RADIUS) GroupVLAN(group string) string {
	for _, g := range r.Groups {
		if g.Name == group {
			return g.VLAN
		}
	}
	return ""
}
