package config

import (
	"net/netip"
	"strings"
	"testing"
)

func portalConfig() *Config {
	c := Default()
	guest := netip.MustParsePrefix("192.168.30.1/24")
	c.VLANs = append(c.VLANs, VLAN{Name: "guest", Parent: "lan", VLANID: 30, Mode: "static", Address: &guest, Enabled: true})
	c.CaptivePortals = []CaptivePortal{{Interface: "guest", Enabled: true, Mode: "password", Password: "letmein"}}
	return c
}

func TestPortalValid(t *testing.T) {
	c := portalConfig()
	c.CaptivePortals[0].Logo = "data:image/png;base64,iVBORw0KGgo="
	if errs := Validate(c); len(errs) != 0 {
		t.Fatal(errs)
	}
	if got := c.ActivePortals(); len(got) != 1 || got["guest"].Session() != DefaultPortalSession {
		t.Errorf("active: %v", got)
	}
	if got := c.PortalAddrs()["guest"].Prefix.String(); got != "192.168.30.1/24" {
		t.Errorf("addr %s", got)
	}
	// a portal on a switched-off VLAN is kept but not active
	c.VLANs[0].Enabled = false
	if errs := Validate(c); len(errs) != 0 {
		t.Fatal(errs)
	}
	if len(c.ActivePortals()) != 0 {
		t.Error("portal active on a disabled VLAN")
	}
}

func TestPortalInvalid(t *testing.T) {
	big := "data:image/png;base64," + strings.Repeat("A", (MaxPortalLogo/3+10)*4)
	for name, tc := range map[string]struct {
		edit func(*Config)
		path string
	}{
		"unknown interface": {func(c *Config) { c.CaptivePortals[0].Interface = "nope" }, "captive_portals[0].interface"},
		"wan":               {func(c *Config) { c.CaptivePortals[0].Interface = "wan1" }, "captive_portals[0].interface"},
		"twice": {func(c *Config) {
			c.CaptivePortals = append(c.CaptivePortals, CaptivePortal{Interface: "guest", Mode: "click"})
		}, "captive_portals[1].interface"},
		"no static address": {func(c *Config) { c.VLANs[0].Mode, c.VLANs[0].Address = "dhcp", nil }, "captive_portals[0].interface"},
		"mode":              {func(c *Config) { c.CaptivePortals[0].Mode = "sms" }, "captive_portals[0].mode"},
		"short password":    {func(c *Config) { c.CaptivePortals[0].Password = "abc" }, "captive_portals[0].password"},
		"session":           {func(c *Config) { c.CaptivePortals[0].SessionMinutes = 2 }, "captive_portals[0].session_minutes"},
		"idle":              {func(c *Config) { c.CaptivePortals[0].IdleMinutes = 5000 }, "captive_portals[0].idle_minutes"},
		"rate":              {func(c *Config) { c.CaptivePortals[0].RateLimitMbps = -1 }, "captive_portals[0].rate_limit_mbps"},
		"title":             {func(c *Config) { c.CaptivePortals[0].Title = strings.Repeat("x", 81) }, "captive_portals[0].title"},
		"logo type":         {func(c *Config) { c.CaptivePortals[0].Logo = "data:text/html;base64,PGI+" }, "captive_portals[0].logo"},
		"logo size":         {func(c *Config) { c.CaptivePortals[0].Logo = big }, "captive_portals[0].logo"},
		"logo url":          {func(c *Config) { c.CaptivePortals[0].Logo = "https://example.com/logo.png" }, "captive_portals[0].logo"},
	} {
		t.Run(name, func(t *testing.T) {
			c := portalConfig()
			tc.edit(c)
			if errorsAt(c)[tc.path] == "" {
				t.Errorf("no error at %s: %v", tc.path, Validate(c))
			}
		})
	}
}

func TestPortalPasswordRedacted(t *testing.T) {
	prev := portalConfig()
	red := prev.Clone()
	RedactSecrets(red)
	if red.CaptivePortals[0].Password != "" {
		t.Fatal("password not redacted")
	}
	next := red.Clone()
	KeepRedactedSecrets(prev, next)
	if next.CaptivePortals[0].Password != "letmein" {
		t.Error("password not kept")
	}
	next = red.Clone()
	next.CaptivePortals[0].Mode = "click"
	KeepRedactedSecrets(prev, next)
	if next.CaptivePortals[0].Password != "" {
		t.Error("password kept for a changed entry")
	}
}
