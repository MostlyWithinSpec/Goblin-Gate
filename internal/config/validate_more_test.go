package config

import (
	"net/netip"
	"strings"
	"testing"
)

// TestValidateMoreRules covers enum, IPv4-only, namespace and port-forward
// checks that keep a valid config renderable.
func TestValidateMoreRules(t *testing.T) {
	wanVLAN := func(c *Config) {
		c.VLANs = append(c.VLANs, VLAN{Name: "isp", Parent: "wan1", VLANID: 7, Mode: "dhcp"})
	}
	pf := func(c *Config, f PortForward) {
		if f.Name == "" {
			f.Name = "pf"
		}
		if f.InIntf == "" {
			f.InIntf = "wan1"
		}
		if f.Protocol == "" {
			f.Protocol = "tcp"
		}
		if f.ExtPort == "" {
			f.ExtPort = "443"
		}
		if f.ToAddr == "" {
			f.ToAddr = "192.168.1.10"
		}
		c.NAT.PortForwards = append(c.NAT.PortForwards, f)
	}
	tests := []struct {
		name   string
		mutate func(*Config)
		want   []string // all must appear; nil = config must be valid
	}{
		{"schema version zero", func(c *Config) { c.SchemaVersion = 0 }, []string{"schema_version", "unsupported schema version"}},
		{"schema version from the future", func(c *Config) { c.SchemaVersion = SchemaVersion + 1 }, []string{"schema_version"}},
		{"bad hostname", func(c *Config) { c.System.Hostname = "goblin gate" }, []string{"system.hostname"}},
		{"bad admin port", func(c *Config) { c.System.AdminPort = 0 }, []string{"system.admin_port", "out of range"}},
		{"unknown role", func(c *Config) { c.Interfaces[1].Role = "inside" }, []string{"interfaces[1].role", "unknown role"}},
		{"unknown mode", func(c *Config) { c.Interfaces[0].Mode = "pppoe" }, []string{"interfaces[0].mode", "unknown mode"}},
		{"missing device", func(c *Config) { c.Interfaces[0].Device = "" }, []string{"interfaces[0].device", "required"}},
		{"bad allow_access", func(c *Config) { c.Interfaces[1].AllowAccess = []string{"telnet"} }, []string{"interfaces[1].allow_access[0]"}},
		{"bad mtu", func(c *Config) { c.Interfaces[1].MTU = 20 }, []string{"interfaces[1].mtu"}},
		{"ipv6 interface address", func(c *Config) { c.Interfaces[1].Address = pfx("fd00::1/64") }, []string{"interfaces[1].address", "IPv6"}},
		{"gateway outside subnet", func(c *Config) {
			gw := netip.MustParseAddr("10.0.0.1")
			c.Interfaces[1].Gateway = &gw
		}, []string{"interfaces[1].gateway", "outside"}},
		{"bad VLAN mode", func(c *Config) {
			c.VLANs = append(c.VLANs, VLAN{Name: "v5", Parent: "lan", VLANID: 5, Mode: "bogus"})
		}, []string{"vlans[0].mode"}},
		{"ipv6 host object", func(c *Config) {
			c.Addresses = append(c.Addresses, Address{Name: "v6", Type: "host", Value: "2001:db8::1"})
		}, []string{"addresses[0].value", "IPv6"}},
		{"ipv6 subnet object", func(c *Config) {
			c.Addresses = append(c.Addresses, Address{Name: "v6", Type: "subnet", Value: "2001:db8::/32"})
		}, []string{"addresses[0].value", "IPv6"}},
		{"ipv6 range object", func(c *Config) {
			c.Addresses = append(c.Addresses, Address{Name: "v6", Type: "range", Value: "2001:db8::1-2001:db8::9"})
		}, []string{"addresses[0].value", "IPv6"}},
		{"object named all", func(c *Config) {
			c.Addresses = append(c.Addresses, Address{Name: "all", Type: "host", Value: "10.0.0.1"})
		}, []string{"addresses[0].name", "reserved"}},
		{"service named ANY", func(c *Config) {
			c.Services = append(c.Services, Service{Name: "ANY", Protocol: "any"})
		}, []string{"services[0].name", "reserved"}},
		{"group shares address name", func(c *Config) {
			c.Addresses = append(c.Addresses, Address{Name: "web", Type: "host", Value: "10.0.0.1"})
			c.AddressGroups = append(c.AddressGroups, AddressGroup{Name: "web", Members: []string{"web"}})
		}, []string{"address_groups[0].name", "also used by an address"}},
		{"service group shares service name", func(c *Config) {
			c.Services = append(c.Services, Service{Name: "web", Protocol: "tcp", Ports: "80"})
			c.ServiceGroups = append(c.ServiceGroups, ServiceGroup{Name: "web", Members: []string{"web"}})
		}, []string{"service_groups[0].name", "also used by a service"}},
		{"unknown action", func(c *Config) { c.Policies[0].Action = "allow" }, []string{"policies[0].action"}},
		{"unknown log mode", func(c *Config) { c.Policies[0].Log = "verbose" }, []string{"policies[0].log"}},
		{"schedule not supported", func(c *Config) { c.Policies[0].Schedule = "workhours" }, []string{"policies[0].schedule", "not supported"}},
		{"policy on VLAN is fine", func(c *Config) {
			c.VLANs = append(c.VLANs, VLAN{Name: "guest", Parent: "lan", VLANID: 20, Mode: "static", Address: pfx("10.20.0.1/24")})
			c.Policies[0].SrcIntf = []string{"guest"}
		}, nil},
		{"port forward without to_port keeps port", func(c *Config) { pf(c, PortForward{}) }, nil},
		{"port forward on wan VLAN", func(c *Config) { wanVLAN(c); pf(c, PortForward{InIntf: "isp"}) }, nil},
		{"port forward range to same range", func(c *Config) { pf(c, PortForward{ExtPort: "8000-8010", ToPort: "8000-8010"}) }, nil},
		{"port forward range to single port", func(c *Config) { pf(c, PortForward{ExtPort: "8000-8010", ToPort: "80"}) }, nil},
		{"port forward range remap", func(c *Config) { pf(c, PortForward{ExtPort: "8000-8010", ToPort: "9000-9010"}) },
			[]string{"nat.port_forwards[0].to_port", "must equal ext_port"}},
		{"port forward port list", func(c *Config) { pf(c, PortForward{ExtPort: "80,443"}) }, []string{"nat.port_forwards[0].ext_port"}},
		{"port forward bad target", func(c *Config) { pf(c, PortForward{ToAddr: "server1"}) }, []string{"nat.port_forwards[0].to_addr"}},
		{"port forward ipv6 target", func(c *Config) { pf(c, PortForward{ToAddr: "2001:db8::5"}) }, []string{"nat.port_forwards[0].to_addr"}},
		{"second DHCP server on same interface", func(c *Config) { c.DHCP = append(c.DHCP, c.DHCP[0]) }, []string{"dhcp[1].interface", "already has a DHCP server"}},
		{"static lease outside subnet", func(c *Config) {
			c.DHCP[0].StaticLeases = []DHCPStaticLease{{MAC: "aa:bb:cc:dd:ee:ff", IP: netip.MustParseAddr("10.0.0.5")}}
		}, []string{"dhcp[0].static_leases[0].ip"}},
		{"unknown feed type", func(c *Config) {
			c.Feeds = append(c.Feeds, Feed{Name: "f", Type: "hash", URL: "https://example.com/x"})
		}, []string{"feeds[0].type"}},
		{"feed url scheme", func(c *Config) {
			c.Feeds = append(c.Feeds, Feed{Name: "f", Type: "ip", URL: "file:///etc/passwd"})
		}, []string{"feeds[0].url"}},
		{"feed format, refresh and auth header", func(c *Config) {
			c.Feeds = append(c.Feeds,
				Feed{Name: "f", Type: "ip", URL: "https://example.com/x", Format: "xml", RefreshMinutes: 5, AuthHeader: "no-colon"},
				Feed{Name: "g", Type: "ip", URL: "https://example.com/y", Format: "csv:2", RefreshMinutes: 15, AuthHeader: "X-Key: abc"},
				Feed{Name: "h", Type: "domain", URL: "https://example.com/z", Format: "json:data.name"},
			)
		}, []string{"feeds[0].format", "feeds[0].refresh_minutes", "feeds[0].auth_header"}},
		{"app control profile", func(c *Config) {
			c.Profiles.AppCtrl = []AppCtrlProfile{{Name: "kids", Groups: map[string]string{"Gaming": "block", "Nope": "block"}, Apps: map[string]string{"steam": "drop", "nope": "allow"}}}
		}, []string{"profiles.appctrl[0].groups.Nope", "profiles.appctrl[0].apps.steam", "profiles.appctrl[0].apps.nope"}},
		{"unknown admin role", func(c *Config) { c.Admins[0].Role = "root" }, []string{"admins[0].role"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			tc.mutate(cfg)
			errs := Validate(cfg)
			var b strings.Builder
			for _, e := range errs {
				b.WriteString(e.Error() + "\n")
			}
			if tc.want == nil {
				if len(errs) != 0 {
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

func TestValidateNil(t *testing.T) {
	if errs := Validate(nil); len(errs) != 1 {
		t.Fatalf("Validate(nil) = %v", errs)
	}
}
