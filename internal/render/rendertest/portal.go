package rendertest

import (
	"net/netip"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// Portal: a guest VLAN 30 (192.168.30.1/24) on the LAN port with DHCP and a
// voucher portal (30 min idle timeout, 10 Mbit/s per client); a disabled
// click-through portal on the LAN.
func Portal() *config.Config {
	c := config.Default()
	guest := netip.MustParsePrefix("192.168.30.1/24")
	c.VLANs = append(c.VLANs, config.VLAN{Name: "guest", Parent: "lan", VLANID: 30, Mode: "static", Address: &guest, Enabled: true})
	c.DHCP = append(c.DHCP, config.DHCPServer{Interface: "guest",
		RangeStart: netip.MustParseAddr("192.168.30.100"), RangeEnd: netip.MustParseAddr("192.168.30.199")})
	c.Policies = append(c.Policies, config.Policy{ID: 2, Name: "guest-to-internet", Enabled: true, SrcIntf: []string{"guest"},
		DstIntf: []string{"wan1"}, SrcAddr: []string{"all"}, DstAddr: []string{"all"}, Service: []string{"all"}, Action: "accept", NAT: true})
	c.CaptivePortals = []config.CaptivePortal{
		{Interface: "guest", Enabled: true, Mode: "voucher", IdleMinutes: 30, RateLimitMbps: 10, Title: "Guest Wi-Fi", Text: "Be nice."},
		{Interface: "lan", Enabled: false, Mode: "click"},
	}
	return c
}
