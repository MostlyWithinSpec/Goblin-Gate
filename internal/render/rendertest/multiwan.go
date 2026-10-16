// Package rendertest holds config fixtures shared by renderer tests.
package rendertest

import (
	"net/netip"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// MultiWAN (task 049): wan1 by DHCP, wan2 static, a WireGuard VPN client
// with kill switch; groups "failover" (wan1, then wan2) as the default route
// and "balance" (2:1 in one tier); kids' devices go through the VPN, the
// NAS reaches 10.50.0.0/16 via wan2.
func MultiWAN() *config.Config {
	cfg := config.Default()
	wan2 := netip.MustParsePrefix("198.51.100.2/24")
	cfg.Interfaces = append(cfg.Interfaces, config.Interface{Name: "wan2", Device: "eth2", Role: "wan", Mode: "static", Address: &wan2, Enabled: true})
	cfg.VPN.WireGuardClients = []config.WireGuardClient{{
		Name: "vpn", Enabled: true, Address: netip.MustParsePrefix("10.64.0.2/32"),
		PeerPublicKey: "VGhpc0lzQVRlc3RLZXlUaGlzSXNBVGVzdEtleTMyQj0=", Endpoint: "wg.vpn.example:51820",
		DNS: []netip.Addr{netip.MustParseAddr("10.64.0.1")}, KillSwitch: true,
	}}
	cfg.Gateways = []config.Gateway{
		{Name: "wan1", Interface: "wan1", Address: "dhcp"},
		{Name: "wan2", Interface: "wan2", Address: "198.51.100.1", Monitor: config.GatewayMonitor{IP: "9.9.9.9", IntervalSeconds: 5}},
		{Name: "vpn", Interface: "vpn"},
	}
	cfg.GatewayGroups = []config.GatewayGroup{
		{Name: "failover", Members: []config.GroupMember{{Gateway: "wan1", Tier: 1}, {Gateway: "wan2", Tier: 2}}},
		{Name: "balance", Members: []config.GroupMember{{Gateway: "wan1", Weight: 2}, {Gateway: "wan2", Weight: 1}}},
	}
	cfg.DefaultGateway = "failover"
	cfg.Routes = []config.Route{{Destination: netip.MustParsePrefix("10.50.0.0/16"), Gateway: "wan2", Comment: "branch office"}}
	cfg.Addresses = []config.Address{{Name: "kids", Type: "range", Value: "192.168.1.50-192.168.1.59"}}
	all := []string{"all"}
	cfg.Policies = []config.Policy{
		{ID: 2, Name: "kids-via-vpn", Enabled: true, SrcIntf: []string{"lan"}, DstIntf: all, SrcAddr: []string{"kids"},
			DstAddr: all, Service: all, Action: "accept", NAT: true, Gateway: "vpn"},
		{ID: 3, Name: "downloads-balanced", Enabled: true, SrcIntf: []string{"lan"}, DstIntf: []string{"wan1", "wan2"},
			SrcAddr: all, DstAddr: all, Service: all, Action: "accept", NAT: true, Gateway: "balance"},
		{ID: 1, Name: "lan-to-internet", Enabled: true, SrcIntf: []string{"lan"}, DstIntf: all, SrcAddr: all,
			DstAddr: all, Service: all, Action: "accept", NAT: true},
	}
	return cfg
}
