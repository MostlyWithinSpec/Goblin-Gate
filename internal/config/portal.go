package config

import (
	"encoding/base64"
	"net/netip"
	"strconv"
	"strings"
)

// CaptivePortal shows a welcome page to new clients of one interface or VLAN
// (a guest network) before they get out: accept the terms, or enter a
// password or a voucher. Until then they reach only DHCP, the box's DNS and
// the portal; plain HTTP is redirected to the portal and HTTPS refused.
type CaptivePortal struct {
	// Interface is the interface or VLAN the portal guards.
	Interface string `json:"interface"`
	Enabled   bool   `json:"enabled"`
	// Mode: click (accept the terms), password (one shared password) or
	// voucher (a code from a voucher batch).
	Mode     string `json:"mode"`
	Password string `json:"password,omitempty"`
	// SessionMinutes is how long a let-in client stays in (0 = 1440). A
	// voucher's own length wins over it.
	SessionMinutes int `json:"session_minutes,omitempty"`
	// IdleMinutes ends a session after that long without traffic (0 = never).
	IdleMinutes int `json:"idle_minutes,omitempty"`
	// RateLimitMbps caps each client's download and upload (0 = none).
	RateLimitMbps int `json:"rate_limit_mbps,omitempty"`
	// Title, Text (the terms or a welcome, plain text) and Logo (a data: URI of
	// a PNG, JPEG, WebP or SVG image, at most MaxPortalLogo bytes decoded)
	// make up the page.
	Title string `json:"title,omitempty"`
	Text  string `json:"text,omitempty"`
	Logo  string `json:"logo,omitempty"`
}

const (
	// PortalPort is the port the portal pages are served on (the box's
	// address on a portal interface only).
	PortalPort            = 8880
	DefaultPortalSession  = 1440
	MaxPortalLogo         = 128 << 10
	maxPortalText         = 4000
	maxPortalTitle        = 80
	minPortalPasswordSize = 4
)

// PortalAPIURL is the RFC 8908 API of the portal at the box address a,
// handed out by DHCP option 114.
func PortalAPIURL(a netip.Addr) string {
	return "http://" + netip.AddrPortFrom(a, PortalPort).String() + "/captive-portal/api"
}

// PortalURL is the portal page at the box address a.
func PortalURL(a netip.Addr) string {
	return "http://" + netip.AddrPortFrom(a, PortalPort).String() + "/"
}

// Session returns the session length in minutes.
func (p CaptivePortal) Session() int {
	if p.SessionMinutes == 0 {
		return DefaultPortalSession
	}
	return p.SessionMinutes
}

// PortalAddr is a portal interface's own address and network.
type PortalAddr struct {
	Prefix netip.Prefix // the box's address with the network's length
}

// ActivePortals returns the enabled portals on enabled interfaces with a
// static IPv4 address, by interface name.
func (c *Config) ActivePortals() map[string]CaptivePortal {
	out := map[string]CaptivePortal{}
	addrs := c.portalAddrs()
	for _, p := range c.CaptivePortals {
		if _, ok := addrs[p.Interface]; ok && p.Enabled {
			out[p.Interface] = p
		}
	}
	return out
}

// PortalAddrs returns the address of each active portal's interface.
func (c *Config) PortalAddrs() map[string]PortalAddr {
	addrs := c.portalAddrs()
	out := map[string]PortalAddr{}
	for name := range c.ActivePortals() {
		out[name] = addrs[name]
	}
	return out
}

// portalAddrs: enabled interfaces and VLANs that are not WAN and have a
// static IPv4 address.
func (c *Config) portalAddrs() map[string]PortalAddr {
	out := map[string]PortalAddr{}
	up := map[string]bool{}
	for _, in := range c.Interfaces {
		up[in.Name] = in.Enabled && in.Role != "wan"
		if in.Role != "wan" && in.Enabled && in.Address != nil && in.Address.Addr().Is4() {
			out[in.Name] = PortalAddr{Prefix: *in.Address}
		}
	}
	for _, vl := range c.VLANs {
		if up[vl.Parent] && vl.Enabled && vl.Address != nil && vl.Address.Addr().Is4() {
			out[vl.Name] = PortalAddr{Prefix: *vl.Address}
		}
	}
	return out
}

var portalLogoTypes = []string{"image/png", "image/jpeg", "image/webp", "image/svg+xml"}

// PortalLogo decodes a logo data URI into its type and bytes.
func PortalLogo(uri string) (string, []byte, bool) {
	rest, ok := strings.CutPrefix(uri, "data:")
	if !ok {
		return "", nil, false
	}
	typ, data, ok := strings.Cut(rest, ";base64,")
	if !ok || !oneOf(typ, portalLogoTypes...) {
		return "", nil, false
	}
	b, err := base64.StdEncoding.DecodeString(data)
	if err != nil || len(b) == 0 || len(b) > MaxPortalLogo {
		return "", nil, false
	}
	return typ, b, true
}

func (v *validator) checkCaptivePortals() {
	addrs := v.cfg.portalAddrs()
	known := map[string]bool{}
	for _, in := range v.cfg.Interfaces {
		known[in.Name] = in.Role != "wan"
	}
	for _, vl := range v.cfg.VLANs {
		known[vl.Name] = true
	}
	seen := map[string]bool{}
	for i, p := range v.cfg.CaptivePortals {
		path := "captive_portals[" + strconv.Itoa(i) + "]"
		switch lan, ok := known[p.Interface]; {
		case !ok:
			v.add(path+".interface", "unknown interface or VLAN %q", p.Interface)
		case !lan:
			v.add(path+".interface", "a portal cannot guard a WAN interface")
		case seen[p.Interface]:
			v.add(path+".interface", "%s already has a portal", p.Interface)
		case p.Enabled && !addrs[p.Interface].Prefix.IsValid() && !v.disabledIntf(p.Interface):
			v.add(path+".interface", "%s needs a static IPv4 address for a portal", p.Interface)
		}
		seen[p.Interface] = true
		if !oneOf(p.Mode, "click", "password", "voucher") {
			v.add(path+".mode", "unknown mode %q: want click, password or voucher", p.Mode)
		}
		if p.Mode == "password" && len(p.Password) < minPortalPasswordSize {
			v.add(path+".password", "want a password of at least %d characters", minPortalPasswordSize)
		}
		if p.SessionMinutes != 0 && (p.SessionMinutes < 5 || p.SessionMinutes > 43200) {
			v.add(path+".session_minutes", "want 5-43200 minutes (30 days)")
		}
		if p.IdleMinutes != 0 && (p.IdleMinutes < 5 || p.IdleMinutes > 1440) {
			v.add(path+".idle_minutes", "want 5-1440 minutes, or 0 for no idle timeout")
		}
		if p.RateLimitMbps < 0 || p.RateLimitMbps > 10000 {
			v.add(path+".rate_limit_mbps", "want 1-10000 Mbit/s, or 0 for no limit")
		}
		if len([]rune(p.Title)) > maxPortalTitle {
			v.add(path+".title", "at most %d characters", maxPortalTitle)
		}
		if len([]rune(p.Text)) > maxPortalText {
			v.add(path+".text", "at most %d characters", maxPortalText)
		}
		if p.Logo != "" {
			if _, _, ok := PortalLogo(p.Logo); !ok {
				v.add(path+".logo", "want a data: URI of a PNG, JPEG, WebP or SVG image of at most %d KB", MaxPortalLogo>>10)
			}
		}
	}
}

// disabledIntf: the interface or VLAN (or a VLAN's parent) is switched off;
// a portal on it is kept but not active.
func (v *validator) disabledIntf(name string) bool {
	for _, in := range v.cfg.Interfaces {
		if in.Name == name {
			return !in.Enabled
		}
	}
	for _, vl := range v.cfg.VLANs {
		if vl.Name == name {
			if !vl.Enabled {
				return true
			}
			for _, in := range v.cfg.Interfaces {
				if in.Name == vl.Parent {
					return !in.Enabled
				}
			}
		}
	}
	return false
}
