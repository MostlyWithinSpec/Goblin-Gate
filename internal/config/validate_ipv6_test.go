package config

import (
	"net/netip"
	"strings"
	"testing"
)

func TestValidateIPv6(t *testing.T) {
	a := func(s string) *netip.Addr { x := netip.MustParseAddr(s); return &x }
	// dual-stack default: dhcp6 WAN, delegated LAN
	ds := func(c *Config) {
		c.Interfaces[0].IPv6 = &IPv6{Mode: "dhcp6"}
		c.Interfaces[1].IPv6 = &IPv6{Mode: "delegated"}
	}
	tests := []struct {
		name   string
		mutate func(*Config)
		want   []string // nil = valid
	}{
		{"dhcp6 wan + delegated lan", ds, nil},
		{"static both sides", func(c *Config) {
			c.Interfaces[0].IPv6 = &IPv6{Mode: "static", Address: pfx("2001:db8::2/64"), Gateway: a("fe80::1")}
			c.Interfaces[1].IPv6 = &IPv6{Mode: "static", Address: pfx("2001:db8:1::1/64"), RA: &RA{Enabled: true}}
		}, nil},
		{"delegated vlans need distinct prefix ids", func(c *Config) {
			ds(c)
			c.VLANs = []VLAN{
				{Name: "iot", Parent: "lan", VLANID: 20, Mode: "none", Enabled: true, IPv6: &IPv6{Mode: "delegated", PrefixID: 1}},
				{Name: "cam", Parent: "lan", VLANID: 30, Mode: "none", Enabled: true, IPv6: &IPv6{Mode: "delegated", PrefixID: 1}},
			}
		}, []string{"vlans[1].ipv6.prefix_id", "already used by iot"}},
		{"prefix id must fit pd size", func(c *Config) {
			ds(c)
			c.Interfaces[0].IPv6.PDSize = 62
			c.Interfaces[1].IPv6.PrefixID = 4
		}, []string{"interfaces[1].ipv6.prefix_id", "0-3"}},
		{"dhcp6 on lan", func(c *Config) { c.Interfaces[1].IPv6 = &IPv6{Mode: "dhcp6"} }, []string{"interfaces[1].ipv6.mode", "WAN"}},
		{"delegated on wan", func(c *Config) { c.Interfaces[0].IPv6 = &IPv6{Mode: "delegated"} }, []string{"interfaces[0].ipv6.mode"}},
		{"delegated without dhcp6 wan", func(c *Config) { c.Interfaces[1].IPv6 = &IPv6{Mode: "delegated"} }, []string{"interfaces[1].ipv6.uplink"}},
		{"delegated from unknown uplink", func(c *Config) {
			ds(c)
			c.Interfaces[1].IPv6.Uplink = "wan9"
		}, []string{"interfaces[1].ipv6.uplink", "not a dhcp6"}},
		{"bad pd size", func(c *Config) { ds(c); c.Interfaces[0].IPv6.PDSize = 40 }, []string{"interfaces[0].ipv6.pd_size"}},
		{"static needs v6 address", func(c *Config) {
			c.Interfaces[1].IPv6 = &IPv6{Mode: "static", Address: pfx("192.168.9.1/24")}
		}, []string{"interfaces[1].ipv6.address", "not an IPv6"}},
		{"slaac needs a /64", func(c *Config) {
			c.Interfaces[1].IPv6 = &IPv6{Mode: "static", Address: pfx("2001:db8:1::1/56")}
		}, []string{"interfaces[1].ipv6.address", "/64"}},
		{"overlapping statics", func(c *Config) {
			c.Interfaces[0].IPv6 = &IPv6{Mode: "static", Address: pfx("2001:db8::2/48")}
			c.Interfaces[1].IPv6 = &IPv6{Mode: "static", Address: pfx("2001:db8:0:1::1/64")}
		}, []string{"interfaces[1].ipv6.address", "overlaps"}},
		{"lan gateway", func(c *Config) {
			c.Interfaces[1].IPv6 = &IPv6{Mode: "static", Address: pfx("2001:db8:1::1/64"), Gateway: a("2001:db8:1::fe")}
		}, []string{"interfaces[1].ipv6.gateway"}},
		{"ra on wan dhcp6", func(c *Config) { c.Interfaces[0].IPv6 = &IPv6{Mode: "dhcp6", RA: &RA{Enabled: true}} }, []string{"interfaces[0].ipv6.ra"}},
		{"unknown mode", func(c *Config) { c.Interfaces[0].IPv6 = &IPv6{Mode: "slaac"} }, []string{"interfaces[0].ipv6.mode"}},
		{"v4 field with v6 address", func(c *Config) { c.Interfaces[1].Address = pfx("fd00::1/64") }, []string{"interfaces[1].address", "set it under ipv6"}},
		{"v6 address objects", func(c *Config) {
			c.Addresses = append(c.Addresses,
				Address{Name: "h6", Type: "host", Value: "2001:db8::1"},
				Address{Name: "n6", Type: "subnet", Value: "2001:db8::/32"},
				Address{Name: "r6", Type: "range", Value: "2001:db8::1-2001:db8::9"})
		}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			tc.mutate(cfg)
			var b strings.Builder
			for _, e := range Validate(cfg) {
				b.WriteString(e.Error() + "\n")
			}
			if tc.want == nil {
				if b.Len() != 0 {
					t.Fatalf("expected a valid config, got:\n%s", b.String())
				}
				return
			}
			for _, w := range tc.want {
				if !strings.Contains(b.String(), w) {
					t.Errorf("expected an error containing %q\ngot:\n%s", w, b.String())
				}
			}
		})
	}
}
