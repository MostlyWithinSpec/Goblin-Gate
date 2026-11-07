package main

import (
	"net/netip"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

func TestLookupInterface(t *testing.T) {
	c := config.Default() // lan 192.168.1.1/24, wan1 by DHCP (no static network)
	p := netip.MustParsePrefix("192.168.2.1/24")
	c.VLANs = []config.VLAN{{Name: "wifi", Parent: "lan", VLANID: 2, Address: &p, Enabled: true}}
	c.VPN.WireGuard = []config.WireGuard{{Name: "wg0", Enabled: true, Address: netip.MustParsePrefix("10.66.0.1/24")}}
	c.VPN.Tailscale.Enabled = true
	nets := interfaceNets(c, func(dev string) []netip.Prefix {
		switch dev {
		case "eth0": // wan1 by DHCP
			return []netip.Prefix{netip.MustParsePrefix("24.101.68.119/24"), netip.MustParsePrefix("2001:db8:ffff::5/128")}
		case "eth1": // lan: a delegated /64
			return []netip.Prefix{netip.MustParsePrefix("192.168.1.1/24"), netip.MustParsePrefix("2001:db8:1::1/64")}
		}
		return nil
	})
	for _, tc := range []struct{ src, dst, want string }{
		{"192.168.1.115", "104.16.1.1", "lan"},
		{"192.168.2.37", "104.16.1.1", "wifi"},
		{"::ffff:192.168.2.37", "104.16.1.1", "wifi"},
		{"2001:db8:1::abcd", "2606:4700::1", "lan"},
		{"24.101.68.119", "1.1.1.1", "wan1"},
		{"10.66.0.2", "192.168.1.10", "wg0"},
		{"100.101.102.103", "192.168.1.10", "tailscale"},
		// from outside: arrived on the WAN (the only one here)
		{"104.16.1.1", "24.101.68.119", "wan1"},
		{"104.16.1.1", "192.168.1.20", "wan1"},
		{"not an address", "192.168.1.20", ""},
	} {
		if got := lookupInterface(nets, tc.src, tc.dst); got != tc.want {
			t.Errorf("%s -> %s: %q, want %q", tc.src, tc.dst, got, tc.want)
		}
	}
}

func TestLookupInterfaceTwoWANs(t *testing.T) {
	c := config.Default()
	a := netip.MustParsePrefix("203.0.113.2/24")
	c.Interfaces = append(c.Interfaces, config.Interface{Name: "wan2", Device: "eth2", Role: "wan", Mode: "static", Address: &a, Enabled: true})
	nets := interfaceNets(c, nil)
	if got := lookupInterface(nets, "104.16.1.1", "203.0.113.2"); got != "wan2" {
		t.Errorf("to wan2's address: %q", got)
	}
	// behind a port forward with two WANs it could have come in on either
	if got := lookupInterface(nets, "104.16.1.1", "192.168.1.20"); got != "" {
		t.Errorf("forwarded, two WANs: %q, want unknown", got)
	}
}
