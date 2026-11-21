package rendertest

import (
	"net/netip"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// Radius: VLANs iot (20), staff (40) and quarantine (99) on the LAN port;
// RADIUS for a switch (192.168.1.2) and the access points' subnet
// (192.168.1.64/28), MAB and 802.1X, unknown devices to quarantine, a staff
// group and two users (one disabled). 802.1X needs the internal CA.
func Radius() *config.Config {
	c := config.Default()
	for _, v := range []struct {
		name string
		id   int
	}{{"iot", 20}, {"staff", 40}, {"quarantine", 99}} {
		a := netip.PrefixFrom(netip.AddrFrom4([4]byte{192, 168, byte(v.id), 1}), 24)
		c.VLANs = append(c.VLANs, config.VLAN{Name: v.name, Parent: "lan", VLANID: v.id, Mode: "static", Address: &a, Enabled: true})
	}
	c.InternalCA.Enabled = true
	c.RADIUS = config.RADIUS{
		Enabled: true, MAB: true, Dot1X: true, Unknown: "quarantine", QuarantineVLAN: "quarantine",
		Clients: []config.RADIUSClient{
			{Name: "switch", Address: "192.168.1.2", Secret: "switch-secret-1"},
			{Name: "aps", Address: "192.168.1.64/28", Secret: "ap-secret-123", CoAPort: 3800},
		},
		Groups: []config.RADIUSGroup{{Name: "staff", VLAN: "staff"}, {Name: "nobody"}},
		Users: []config.RADIUSUser{
			{Name: "alice", Enabled: true, Password: "alice-password", Group: "staff"},
			{Name: "bob", Enabled: false, Group: "staff"},
		},
	}
	return c
}
