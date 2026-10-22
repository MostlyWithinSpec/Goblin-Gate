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
	nets := interfaceNets(c)
	for ip, want := range map[string]string{
		"192.168.1.115":       "lan",
		"192.168.2.37":        "wifi",
		"::ffff:192.168.2.37": "wifi",
		"104.16.1.1":          "",
		"not an address":      "",
	} {
		if got := lookupInterface(nets, ip); got != want {
			t.Errorf("%s: %q, want %q", ip, got, want)
		}
	}
}
