package config

import (
	"net/netip"
	"strings"
	"testing"
)

// TestValidateDefaultIsValid ensures the shipped default config passes
// validation, both as-is and with an extra admin added.
func TestValidateDefaultIsValid(t *testing.T) {
	if errs := Validate(Default()); len(errs) != 0 {
		t.Fatalf("Default() should be valid, got errors: %v", errs)
	}
	cfg := Default()
	cfg.Admins = append(cfg.Admins, Admin{Username: "auditor", Role: "readonly"})
	if errs := Validate(cfg); len(errs) != 0 {
		t.Fatalf("Default() plus an extra admin should be valid, got errors: %v", errs)
	}
}

// TestValidateRules exercises every validation rule with at least one failing
// case each and finishes with a fully valid extended config.
func TestValidateRules(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*Config)
		want      []string
		wantNot   []string
		wantValid bool
	}{
		{
			name: "invalid address name",
			mutate: func(c *Config) {
				c.Addresses = append(c.Addresses, Address{Name: "bad name!", Type: "host", Value: "10.0.0.9"})
			},
			want: []string{"addresses[0].name", "invalid name"},
		},
		{
			name: "address name too long",
			mutate: func(c *Config) {
				c.Addresses = append(c.Addresses, Address{Name: strings.Repeat("a", 64), Type: "host", Value: "10.0.0.9"})
			},
			want: []string{"addresses[0].name", "invalid name"},
		},
		{
			name: "duplicate address names",
			mutate: func(c *Config) {
				c.Addresses = append(c.Addresses,
					Address{Name: "same", Type: "host", Value: "10.0.0.1"},
					Address{Name: "same", Type: "host", Value: "10.0.0.2"})
			},
			want: []string{"addresses[1].name", "duplicate address name"},
		},
		{
			name:   "duplicate interface device",
			mutate: func(c *Config) { c.Interfaces[0].Device = "eth1" },
			want:   []string{"interfaces[1].device", "already used by"},
		},
		{
			name: "VLAN parent missing",
			mutate: func(c *Config) {
				c.VLANs = append(c.VLANs, VLAN{Name: "v10", Parent: "nope", VLANID: 10, Mode: "none"})
			},
			want: []string{"vlans[0].parent", "unknown parent"},
		},
		{
			name: "vlan_id out of range",
			mutate: func(c *Config) {
				c.VLANs = append(c.VLANs, VLAN{Name: "v10", Parent: "lan", VLANID: 4095, Mode: "none"})
			},
			want: []string{"vlans[0].vlan_id", "out of range"},
		},
		{
			name: "duplicate vlan_id on same parent only",
			mutate: func(c *Config) {
				c.VLANs = append(c.VLANs,
					VLAN{Name: "v10", Parent: "lan", VLANID: 10, Mode: "none"},
					VLAN{Name: "v11", Parent: "lan", VLANID: 10, Mode: "none"},
					VLAN{Name: "v12", Parent: "wan1", VLANID: 10, Mode: "none"})
			},
			want:    []string{"vlans[1].vlan_id", "duplicate vlan_id"},
			wantNot: []string{"vlans[2]"},
		},
		{
			name:   "static interface without address",
			mutate: func(c *Config) { c.Interfaces[0].Mode = "static" },
			want:   []string{"interfaces[0].address", "static interface has no address"},
		},
		{
			name: "overlapping interface addresses",
			mutate: func(c *Config) {
				c.Interfaces = append(c.Interfaces,
					Interface{Name: "dmz1", Device: "eth2", Role: "dmz", Mode: "static", Address: pfx("192.168.1.128/25")},
					Interface{Name: "srv", Device: "eth3", Role: "lan", Mode: "static", Address: pfx("10.0.0.1/24")})
			},
			want:    []string{"interfaces[2].address", "overlaps"},
			wantNot: []string{"interfaces[3]"},
		},
		{
			name: "invalid subnet value",
			mutate: func(c *Config) {
				c.Addresses = append(c.Addresses, Address{Name: "badsub", Type: "subnet", Value: "192.168.1.0/33"})
			},
			want: []string{"addresses[0].value", "invalid subnet"},
		},
		{
			name: "invalid host value",
			mutate: func(c *Config) {
				c.Addresses = append(c.Addresses, Address{Name: "badhost", Type: "host", Value: "not-an-ip"})
			},
			want: []string{"addresses[0].value", "invalid host address"},
		},
		{
			name: "range start after end",
			mutate: func(c *Config) {
				c.Addresses = append(c.Addresses, Address{Name: "badrange", Type: "range", Value: "10.0.0.9-10.0.0.5"})
			},
			want: []string{"addresses[0].value", "greater than end"},
		},
		{
			name: "range missing dash",
			mutate: func(c *Config) {
				c.Addresses = append(c.Addresses, Address{Name: "badrange", Type: "range", Value: "10.0.0.5"})
			},
			want: []string{"addresses[0].value", `start-end`},
		},
		{
			name: "invalid hostname",
			mutate: func(c *Config) {
				c.Addresses = append(c.Addresses, Address{Name: "badfqdn", Type: "fqdn", Value: "bad_host"})
			},
			want: []string{"addresses[0].value", "invalid hostname"},
		},
		{
			name: "unknown address type",
			mutate: func(c *Config) {
				c.Addresses = append(c.Addresses, Address{Name: "badtype", Type: "bogus", Value: "x"})
			},
			want: []string{"addresses[0].type", "unknown type"},
		},
		{
			name: "address group unknown member",
			mutate: func(c *Config) {
				c.Addresses = append(c.Addresses, Address{Name: "a1", Type: "host", Value: "10.1.1.1"})
				c.AddressGroups = append(c.AddressGroups, AddressGroup{Name: "g1", Members: []string{"ghost"}})
			},
			want: []string{"address_groups[0].members[0]", "unknown member"},
		},
		{
			name: "address group includes itself",
			mutate: func(c *Config) {
				c.Addresses = append(c.Addresses, Address{Name: "a1", Type: "host", Value: "10.1.1.1"})
				c.AddressGroups = append(c.AddressGroups, AddressGroup{Name: "g1", Members: []string{"g1"}})
			},
			want: []string{"address_groups[0]", "group includes itself"},
		},
		{
			name: "address group transitive self-inclusion",
			mutate: func(c *Config) {
				c.Addresses = append(c.Addresses, Address{Name: "a1", Type: "host", Value: "10.1.1.1"})
				c.AddressGroups = append(c.AddressGroups,
					AddressGroup{Name: "g1", Members: []string{"g2"}},
					AddressGroup{Name: "g2", Members: []string{"g1"}})
			},
			want: []string{"address_groups[0]", "address_groups[1]", "through other address groups"},
		},
		{
			name:   "tcp port zero",
			mutate: func(c *Config) { c.Services = append(c.Services, Service{Name: "s1", Protocol: "tcp", Ports: "0"}) },
			want:   []string{"services[0].ports", "invalid ports"},
		},
		{
			name:   "tcp port above 65535",
			mutate: func(c *Config) { c.Services = append(c.Services, Service{Name: "s1", Protocol: "tcp", Ports: "70000"}) },
			want:   []string{"services[0].ports", "invalid ports"},
		},
		{
			name: "tcp range low above high",
			mutate: func(c *Config) {
				c.Services = append(c.Services, Service{Name: "s1", Protocol: "tcp", Ports: "8000-7000"})
			},
			want: []string{"services[0].ports", "invalid ports"},
		},
		{
			name:   "icmp with ports",
			mutate: func(c *Config) { c.Services = append(c.Services, Service{Name: "s1", Protocol: "icmp", Ports: "8"}) },
			want:   []string{"services[0].ports", "must not have ports"},
		},
		{
			name:   "any with ports",
			mutate: func(c *Config) { c.Services = append(c.Services, Service{Name: "s1", Protocol: "any", Ports: "8"}) },
			want:   []string{"services[0].ports", "must not have ports"},
		},
		{
			name:   "tcp without ports",
			mutate: func(c *Config) { c.Services = append(c.Services, Service{Name: "s1", Protocol: "tcp"}) },
			want:   []string{"services[0].ports", "invalid ports"},
		},
		{
			name:   "unknown protocol",
			mutate: func(c *Config) { c.Services = append(c.Services, Service{Name: "s1", Protocol: "sctp"}) },
			want:   []string{"services[0].protocol", "unknown protocol"},
		},
		{
			name:   "policy unknown dst interface",
			mutate: func(c *Config) { c.Policies[0].DstIntf = []string{"ghost"} },
			want:   []string{"policies[0].dst_intf[0]", "unknown dst_intf reference"},
		},
		{
			name:   "policy unknown src address",
			mutate: func(c *Config) { c.Policies[0].SrcAddr = []string{"ghost"} },
			want:   []string{"policies[0].src_addr[0]", "unknown src_addr reference"},
		},
		{
			name:   "policy unknown service",
			mutate: func(c *Config) { c.Policies[0].Service = []string{"ghost"} },
			want:   []string{"policies[0].service[0]", "unknown service reference"},
		},
		{
			name:   "policy unknown IPS profile",
			mutate: func(c *Config) { c.Policies[0].IPSProfile = "ghost" },
			want:   []string{"policies[0].ips_profile", "unknown ips_profile reference"},
		},
		{
			name:   "policy unknown threat feed",
			mutate: func(c *Config) { c.Policies[0].ThreatFeeds = []string{"ghost"} },
			want:   []string{"policies[0].threat_feeds[0]", "unknown threat_feeds reference"},
		},
		{
			name:   "duplicate policy id",
			mutate: func(c *Config) { c.Policies = append(c.Policies, Policy{ID: 1, Name: "p2"}) },
			want:   []string{"policies[1].id", "duplicate policy id"},
		},
		{
			name: "port forward on non-wan interface",
			mutate: func(c *Config) {
				c.NAT.PortForwards = append(c.NAT.PortForwards, PortForward{Name: "pf", InIntf: "lan"})
			},
			want: []string{"nat.port_forwards[0].in_intf", "want wan"},
		},
		{
			name: "port forward unknown interface",
			mutate: func(c *Config) {
				c.NAT.PortForwards = append(c.NAT.PortForwards, PortForward{Name: "pf", InIntf: "ghost"})
			},
			want: []string{"nat.port_forwards[0].in_intf", "unknown interface"},
		},
		{
			name: "port forward bad ext port",
			mutate: func(c *Config) {
				c.NAT.PortForwards = append(c.NAT.PortForwards, PortForward{Name: "pf", InIntf: "wan1", ExtPort: "99999"})
			},
			want: []string{"nat.port_forwards[0].ext_port", "invalid port"},
		},
		{
			name: "port forward bad to port",
			mutate: func(c *Config) {
				c.NAT.PortForwards = append(c.NAT.PortForwards, PortForward{Name: "pf", InIntf: "wan1", ToPort: "0-65536"})
			},
			want: []string{"nat.port_forwards[0].to_port", "invalid port"},
		},
		{
			name: "port forward unknown protocol",
			mutate: func(c *Config) {
				c.NAT.PortForwards = append(c.NAT.PortForwards, PortForward{Name: "pf", InIntf: "wan1", Protocol: "sctp"})
			},
			want: []string{"nat.port_forwards[0].protocol", "unknown protocol"},
		},
		{
			name:   "dhcp range outside subnet",
			mutate: func(c *Config) { c.DHCP[0].RangeStart = netip.MustParseAddr("10.9.9.9") },
			want:   []string{"dhcp[0].range_start", "outside the interface subnet"},
		},
		{
			name: "dhcp range end before start",
			mutate: func(c *Config) {
				c.DHCP[0].RangeStart = netip.MustParseAddr("192.168.1.200")
				c.DHCP[0].RangeEnd = netip.MustParseAddr("192.168.1.100")
			},
			want: []string{"dhcp[0].range_end", "before range_start"},
		},
		{
			name:   "dhcp on non-static interface",
			mutate: func(c *Config) { c.Interfaces[1].Mode = "dhcp" },
			want:   []string{"dhcp[0].interface", "not static"},
		},
		{
			name:   "dhcp unknown interface",
			mutate: func(c *Config) { c.DHCP[0].Interface = "ghost" },
			want:   []string{"dhcp[0].interface", "unknown interface"},
		},
		{
			name:   "no admins",
			mutate: func(c *Config) { c.Admins = nil },
			want:   []string{"admins", "at least one admin is required"},
		},
		{
			name:      "no admins allowed on first boot",
			mutate:    func(c *Config) { c.Admins = nil; c.AllowNoAdmin = true },
			wantValid: true,
		},
		{
			name: "valid extended config",
			mutate: func(c *Config) {
				c.VLANs = append(c.VLANs, VLAN{Name: "lan10", Parent: "lan", VLANID: 10, Mode: "static", Enabled: true, Address: pfx("192.168.10.1/24")})
				c.Addresses = append(c.Addresses,
					Address{Name: "printer", Type: "host", Value: "192.168.1.50"},
					Address{Name: "cameras", Type: "range", Value: "192.168.1.200-192.168.1.210"},
					Address{Name: "lab", Type: "subnet", Value: "10.10.0.0/16"},
					Address{Name: "cam-fqdn", Type: "fqdn", Value: "cams.example.com"})
				c.AddressGroups = append(c.AddressGroups, AddressGroup{Name: "iot", Members: []string{"printer", "cameras"}})
				c.Services = append(c.Services,
					Service{Name: "http", Protocol: "tcp", Ports: "80,443"},
					Service{Name: "ping", Protocol: "icmp"})
				c.ServiceGroups = append(c.ServiceGroups, ServiceGroup{Name: "web", Members: []string{"http"}})
				c.Profiles.IPS = append(c.Profiles.IPS, IPSProfile{Name: "ips-default"})
				c.Feeds = append(c.Feeds, Feed{Name: "badips", Type: "ip", URL: "https://example.com/feeds.txt", Enabled: true})
				c.Policies[0].SrcAddr = []string{"iot"}
				c.Policies[0].Service = []string{"web", "any"}
				c.Policies[0].IPSProfile = "ips-default"
				c.Policies[0].ThreatFeeds = []string{"badips"}
			},
			wantValid: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			tc.mutate(cfg)
			errs := Validate(cfg)
			if tc.wantValid {
				if len(errs) != 0 {
					t.Fatalf("expected a valid config, got %d errors: %v", len(errs), errs)
				}
				return
			}
			var b strings.Builder
			for _, e := range errs {
				b.WriteString(e.Error())
				b.WriteByte('\n')
			}
			joined := b.String()
			for _, w := range tc.want {
				if !strings.Contains(joined, w) {
					t.Errorf("expected an error containing %q\ngot:\n%s", w, joined)
				}
			}
			for _, w := range tc.wantNot {
				if strings.Contains(joined, w) {
					t.Errorf("did not expect an error containing %q\ngot:\n%s", w, joined)
				}
			}
		})
	}
}

// TestValidationErrorString checks the Error() formatting.
func TestValidationErrorString(t *testing.T) {
	e := ValidationError{Path: "policies[2].dst_addr[0]", Message: `unknown address reference "ghost"`}
	want := `policies[2].dst_addr[0]: unknown address reference "ghost"`
	if got := e.Error(); got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
	empty := ValidationError{Message: "config is nil"}
	if got := empty.Error(); got != "config is nil" {
		t.Fatalf("Error() = %q, want %q", got, "config is nil")
	}
}

// TestValidateCollectsAllErrors ensures validation keeps going after the first
// problem instead of stopping.
func TestValidateCollectsAllErrors(t *testing.T) {
	cfg := Default()
	cfg.Admins = nil
	cfg.Addresses = append(cfg.Addresses, Address{Name: "x y", Type: "bogus", Value: "nope"})
	if errs := Validate(cfg); len(errs) < 2 {
		t.Fatalf("expected at least 2 errors (bad address plus missing admin), got %d: %v", len(errs), errs)
	}
}

// pfx returns a pointer to a parsed prefix, for Interface/VLAN.Address.
func pfx(s string) *netip.Prefix {
	p := netip.MustParsePrefix(s)
	return &p
}
