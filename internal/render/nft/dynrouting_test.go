package nft

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

func TestDynamicRoutingRules(t *testing.T) {
	c := config.Default()
	c.Routing.BGP = config.BGP{Enabled: true, AS: 65001, Neighbors: []config.BGPNeighbor{
		{Address: netip.MustParseAddr("192.0.2.1"), RemoteAS: 64512, Enabled: true},
		{Address: netip.MustParseAddr("192.0.2.2"), RemoteAS: 64512, Enabled: true},
		{Address: netip.MustParseAddr("2001:db8::1"), RemoteAS: 64512, Enabled: true},
		{Address: netip.MustParseAddr("192.0.2.9"), RemoteAS: 64513},
	}}
	c.Routing.OSPF = config.OSPF{Enabled: true, Areas: []config.OSPFArea{{ID: "0", Interfaces: []config.OSPFInterface{{Name: "lan"}}}}}
	if errs := config.Validate(c); len(errs) > 0 {
		t.Fatal(errs)
	}
	out, err := build(c)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		`ip saddr { 192.0.2.1, 192.0.2.2 } tcp dport 179 accept comment "bgp neighbours"`,
		`ip6 saddr 2001:db8::1 tcp dport 179 accept comment "bgp neighbours"`,
		`iifname "eth1" meta l4proto 89 accept comment "ospf"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing: %s", want)
		}
	}
	if strings.Contains(s, "192.0.2.9") {
		t.Error("a disabled neighbour is let in")
	}
	// passive: no OSPF packets; off: nothing
	c.Routing.OSPF.Areas[0].Interfaces[0].Passive = true
	c.Routing.BGP.Enabled = false
	out, _ = build(c)
	if strings.Contains(string(out), "dport 179") || strings.Contains(string(out), "protocol 89") {
		t.Error("rules left while off or passive")
	}
}
