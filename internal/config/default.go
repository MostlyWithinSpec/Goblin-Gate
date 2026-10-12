package config

import "net/netip"

// Default returns a working home-router configuration: WAN on eth0 via DHCP,
// LAN 192.168.1.1/24 on eth1 with DHCP .100-.199 and one accept policy to the
// internet. The default admin has no password hash; first boot must set one.
func Default() *Config {
	lanAddr := netip.MustParsePrefix("192.168.1.1/24")
	gw := netip.MustParseAddr("192.168.1.1")
	return &Config{
		SchemaVersion: SchemaVersion,
		System: System{
			Hostname:   "goblin-gate",
			Timezone:   "UTC",
			AdminPort:  443,
			NTPServers: []string{"pool.ntp.org"},
			DNSServers: []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("8.8.8.8")},
		},
		Interfaces: []Interface{
			{
				Name: "wan1", Device: "eth0", Role: "wan", Mode: "dhcp",
				Enabled: true, Description: "WAN uplink (DHCP client)",
			},
			{
				Name: "lan", Device: "eth1", Role: "lan", Mode: "static",
				Address: &lanAddr, Enabled: true, Description: "LAN",
				AllowAccess: []string{"https", "ssh"},
			},
		},
		Policies: []Policy{{
			ID: 1, Name: "lan-to-internet", Enabled: true,
			SrcIntf: []string{"lan"}, DstIntf: []string{"wan1"},
			SrcAddr: []string{"all"}, DstAddr: []string{"all"}, Service: []string{"all"},
			Action: "accept", NAT: true,
		}},
		DHCP: []DHCPServer{{
			Interface:    "lan",
			RangeStart:   netip.MustParseAddr("192.168.1.100"),
			RangeEnd:     netip.MustParseAddr("192.168.1.199"),
			LeaseSeconds: 86400,
			DNS:          []netip.Addr{gw},
			Gateway:      &gw,
		}},
		DNS: DNS{
			Forwarders: []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("8.8.8.8")},
			DNSSEC:     true,
		},
		Admins: []Admin{{Username: "admin", Role: "admin"}},
	}
}
