package rendertest

import (
	"net/netip"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// Sites (task 155): home (LAN 192.168.1.0/24, endpoint home.example.net)
// dials a cabin (192.168.50.0/24) and reaches an office (192.168.60.0/24)
// through the cabin; a flat that dials home is turned off. Policies let the
// LAN reach the cabin and the cabin reach the NAS only by SSH.
func Sites() *config.Config {
	c := config.Default()
	c.Addresses = append(c.Addresses, config.Address{Name: "nas", Type: "host", Value: "192.168.1.20"})
	c.Services = append(c.Services, config.Service{Name: "ssh", Protocol: "tcp", Ports: "22"})
	c.VPN.Sites = config.Sites{Enabled: true, Address: netip.MustParseAddr("10.254.1.1"), Endpoint: "home.example.net",
		Networks: []string{"lan"}, AllowAccess: []string{"https"},
		Remotes: []config.Site{
			{Name: "cabin", Enabled: true, PublicKey: "YAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk=", Endpoint: "cabin.example.net:51830",
				ListenPort: 51830, Address: netip.MustParseAddr("10.254.2.2"), Networks: []netip.Prefix{netip.MustParsePrefix("192.168.50.0/24")}},
			{Name: "office", Enabled: true, Via: "cabin", Address: netip.MustParseAddr("10.254.3.3"),
				Networks: []netip.Prefix{netip.MustParsePrefix("192.168.60.0/24")}},
			{Name: "flat", Enabled: false, PublicKey: "xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=", ListenPort: 51831,
				Address: netip.MustParseAddr("10.254.4.4"), Networks: []netip.Prefix{netip.MustParsePrefix("192.168.70.0/24")}},
		}}
	c.Policies = append(c.Policies,
		config.Policy{ID: 2, Name: "lan-to-cabin", Enabled: true, SrcIntf: []string{"lan"}, DstIntf: []string{"cabin"},
			SrcAddr: []string{"all"}, DstAddr: []string{"all"}, Service: []string{"all"}, Action: "accept"},
		config.Policy{ID: 3, Name: "cabin-ssh-to-nas", Enabled: true, SrcIntf: []string{"cabin"}, DstIntf: []string{"lan"},
			SrcAddr: []string{"all"}, DstAddr: []string{"nas"}, Service: []string{"ssh"}, Action: "accept"})
	return c
}
