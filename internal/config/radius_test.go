package config

import (
	"net/netip"
	"testing"
)

func radiusConfig() *Config {
	c := Default()
	iot := netip.MustParsePrefix("192.168.20.1/24")
	c.VLANs = append(c.VLANs, VLAN{Name: "iot", Parent: "lan", VLANID: 20, Mode: "static", Address: &iot, Enabled: true})
	c.InternalCA.Enabled = true
	c.RADIUS = RADIUS{Enabled: true, MAB: true, Dot1X: true, Unknown: "quarantine", QuarantineVLAN: "iot",
		Clients: []RADIUSClient{{Name: "switch", Address: "192.168.1.2", Secret: "s3cret-switch"}, {Name: "aps", Address: "fd00::/64", Secret: "s3cret-aps"}},
		Groups:  []RADIUSGroup{{Name: "things", VLAN: "iot"}},
		Users:   []RADIUSUser{{Name: "Alice", Enabled: true, Password: "alice-pass", Group: "things"}, {Name: "bob"}},
	}
	return c
}

func TestRadiusValid(t *testing.T) {
	c := radiusConfig()
	if errs := Validate(c); len(errs) != 0 {
		t.Fatal(errs)
	}
	if !c.RADIUSActive() || c.VLANID("iot") != 20 || c.VLANID("nope") != 0 {
		t.Error("helpers")
	}
	if u, ok := c.RADIUS.RADIUSUserByName("alice"); !ok || c.RADIUS.GroupVLAN(u.Group) != "iot" {
		t.Error("user lookup is case-insensitive and finds the group's VLAN")
	}
	if _, ok := c.RADIUS.RADIUSUserByName("bob"); ok {
		t.Error("disabled user found")
	}
	if p, _ := c.RADIUS.Clients[0].Prefix(); p.String() != "192.168.1.2/32" || c.RADIUS.Clients[0].CoA() != 3799 {
		t.Error("client prefix or CoA port")
	}
	// off with nothing set is fine
	d := Default()
	d.RADIUS = RADIUS{}
	if errs := Validate(d); len(errs) != 0 {
		t.Fatal(errs)
	}
}

func TestRadiusInvalid(t *testing.T) {
	for name, tc := range map[string]struct {
		edit func(*Config)
		path string
	}{
		"no clients":       {func(c *Config) { c.RADIUS.Clients = nil }, "radius.clients"},
		"nothing on":       {func(c *Config) { c.RADIUS.MAB, c.RADIUS.Dot1X = false, false }, "radius.mab"},
		"no CA":            {func(c *Config) { c.InternalCA.Enabled = false }, "radius.dot1x"},
		"unknown":          {func(c *Config) { c.RADIUS.Unknown = "maybe" }, "radius.unknown"},
		"no quarantine":    {func(c *Config) { c.RADIUS.QuarantineVLAN = "" }, "radius.quarantine_vlan"},
		"bad quarantine":   {func(c *Config) { c.RADIUS.QuarantineVLAN = "nope" }, "radius.quarantine_vlan"},
		"client name":      {func(c *Config) { c.RADIUS.Clients[0].Name = "1bad" }, "radius.clients[0].name"},
		"duplicate client": {func(c *Config) { c.RADIUS.Clients[1].Name = "switch" }, "radius.clients[1].name"},
		"client address":   {func(c *Config) { c.RADIUS.Clients[0].Address = "switch.lan" }, "radius.clients[0].address"},
		"short secret":     {func(c *Config) { c.RADIUS.Clients[0].Secret = "short" }, "radius.clients[0].secret"},
		"quote in secret":  {func(c *Config) { c.RADIUS.Clients[0].Secret = `abc"defghi` }, "radius.clients[0].secret"},
		"space in secret":  {func(c *Config) { c.RADIUS.Clients[0].Secret = "abc defghi" }, "radius.clients[0].secret"},
		"coa port":         {func(c *Config) { c.RADIUS.Clients[0].CoAPort = 70000 }, "radius.clients[0].coa_port"},
		"group vlan":       {func(c *Config) { c.RADIUS.Groups[0].VLAN = "nope" }, "radius.groups[0].vlan"},
		"user name":        {func(c *Config) { c.RADIUS.Users[0].Name = "a b" }, "radius.users[0].name"},
		"duplicate user":   {func(c *Config) { c.RADIUS.Users[1].Name = "ALICE" }, "radius.users[1].name"},
		"short password":   {func(c *Config) { c.RADIUS.Users[0].Password = "abc" }, "radius.users[0].password"},
		"enabled, no pass": {func(c *Config) { c.RADIUS.Users[1].Enabled = true }, "radius.users[1].password"},
		"unknown group":    {func(c *Config) { c.RADIUS.Users[0].Group = "nope" }, "radius.users[0].group"},
	} {
		t.Run(name, func(t *testing.T) {
			c := radiusConfig()
			tc.edit(c)
			if errorsAt(c)[tc.path] == "" {
				t.Errorf("no error at %s: %v", tc.path, Validate(c))
			}
		})
	}
}

func TestRadiusSecretsRedacted(t *testing.T) {
	prev := radiusConfig()
	red := prev.Clone()
	RedactSecrets(red)
	if red.RADIUS.Clients[0].Secret != "" || red.RADIUS.Users[0].Password != "" {
		t.Fatal("secrets not redacted")
	}
	next := red.Clone()
	next.RADIUS.Users[0].Group = "" // edited without retyping the password
	next.RADIUS.Clients[1].Secret = "a-new-secret"
	KeepRedactedSecrets(prev, next)
	if next.RADIUS.Clients[0].Secret != "s3cret-switch" || next.RADIUS.Clients[1].Secret != "a-new-secret" || next.RADIUS.Users[0].Password != "alice-pass" {
		t.Errorf("kept: %+v %+v", next.RADIUS.Clients, next.RADIUS.Users)
	}
	// a new entry with a stored entry's name only gets that entry's secret
	next = red.Clone()
	next.RADIUS.Users = append(next.RADIUS.Users, RADIUSUser{Name: "carol", Enabled: true})
	KeepRedactedSecrets(prev, next)
	if next.RADIUS.Users[2].Password != "" {
		t.Error("new user got a password")
	}
}
