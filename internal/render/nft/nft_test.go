package nft

import (
	"bytes"
	"flag"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/rendertest"
)

var update = flag.Bool("update", false, "rewrite golden files in testdata/")

// fullConfig exercises every feature of the renderer.
func fullConfig() *config.Config {
	cfg := config.Default()
	dmz := netip.MustParsePrefix("10.10.0.1/24")
	guest := netip.MustParsePrefix("10.20.0.1/24")
	cfg.Interfaces = append(cfg.Interfaces, config.Interface{
		Name: "dmz", Device: "eth2", Role: "dmz", Mode: "static", Address: &dmz,
		Enabled: true, AllowAccess: []string{"ping"},
	}, config.Interface{
		Name: "spare", Device: "eth3", Role: "lan", Mode: "none", Enabled: false,
		AllowAccess: []string{"ssh"},
	})
	cfg.Interfaces[0].AllowAccess = []string{"ping"}
	cfg.System.AdminPort = 8443
	cfg.VLANs = []config.VLAN{{
		Name: "guest", Parent: "lan", VLANID: 20, Mode: "static", Address: &guest, Enabled: true,
	}, {
		// ISP-style tagged uplink: inherits wan role, so no DNS is opened on it.
		Name: "isp", Parent: "wan1", VLANID: 7, Mode: "dhcp", Enabled: true,
	}}
	cfg.DHCP = append(cfg.DHCP, config.DHCPServer{
		Interface:  "guest",
		RangeStart: netip.MustParseAddr("10.20.0.100"),
		RangeEnd:   netip.MustParseAddr("10.20.0.199"),
	})
	cfg.Addresses = []config.Address{
		{Name: "lan-net", Type: "subnet", Value: "192.168.1.0/24"},
		{Name: "web-server", Type: "host", Value: "10.10.0.10"},
		{Name: "mail.server", Type: "host", Value: "10.10.0.11"},
		{Name: "printers", Type: "range", Value: "192.168.1.50-192.168.1.59"},
		{Name: "lan_net", Type: "subnet", Value: "192.168.2.0/24"},
		{Name: "updates", Type: "fqdn", Value: "updates.example.com"},
	}
	cfg.AddressGroups = []config.AddressGroup{
		{Name: "servers", Members: []string{"web-server", "mail.server"}},
		{Name: "everything", Members: []string{"servers", "lan-net", "printers", "updates"}},
	}
	cfg.Services = []config.Service{
		{Name: "web", Protocol: "tcp", Ports: "80,443,8000-8100"},
		{Name: "dns", Protocol: "udp", Ports: "53"},
		{Name: "ping", Protocol: "icmp"},
		{Name: "all-tcp", Protocol: "tcp", Ports: "1-65535"},
		{Name: "everything", Protocol: "any"},
		{Name: "smtp", Protocol: "tcp", Ports: "25"},
	}
	cfg.ServiceGroups = []config.ServiceGroup{
		{Name: "mail", Members: []string{"smtp", "web"}},
	}
	cfg.Feeds = []config.Feed{
		{Name: "spamhaus-drop", Type: "ip", URL: "https://example.com/drop.txt", Enabled: true},
		{Name: "botnet-c2", Type: "ip", URL: "https://example.com/c2.txt", Enabled: true, BlockWAN: true},
		{Name: "malware-domains", Type: "domain", URL: "https://example.com/d.txt", Enabled: true},
		{Name: "old", Type: "ip", URL: "https://example.com/old.txt", Enabled: false},
	}
	cfg.Profiles.IPS = []config.IPSProfile{{Name: "default"}}
	cfg.Profiles.DNSFilter = []config.DNSFilterProfile{{Name: "family", SafeSearch: true}}
	cfg.Profiles.WebFilter = []config.WebFilterProfile{{Name: "office", Categories: map[string]string{"gambling": "block"}}}
	cfg.VPN.Tailscale = config.Tailscale{Enabled: true, AllowAccess: []string{"ping", "ssh"}}
	cfg.VPN.WireGuard = []config.WireGuard{{Name: "wg0", Enabled: true, Address: netip.MustParsePrefix("10.66.0.1/24"), AllowAccess: []string{"https"}}}
	cfg.Policies = append(cfg.Policies,
		config.Policy{
			ID: 20, Name: "tailnet-to-lan", Enabled: true,
			SrcIntf: []string{"tailscale", "wg0"}, DstIntf: []string{"lan"},
			SrcAddr: []string{"all"}, DstAddr: []string{"all"}, Service: []string{"all"}, Action: "accept",
		},
		config.Policy{
			ID: 10, Name: "lan-to-dmz-web", Enabled: true,
			SrcIntf: []string{"lan", "guest"}, DstIntf: []string{"dmz"},
			SrcAddr: []string{"lan-net", "printers"}, DstAddr: []string{"web-server"},
			Service: []string{"web", "ping"}, Action: "accept", Log: "all",
			IPSProfile: "default", ThreatFeeds: []string{"spamhaus-drop"}, WebFilterProfile: "office",
		},
		config.Policy{
			ID: 11, Name: "dmz-mail", Enabled: true,
			SrcIntf: []string{"dmz"}, DstIntf: []string{"wan1"},
			SrcAddr: []string{"servers"}, DstAddr: []string{"all"},
			Service: []string{"mail"}, Action: "accept", NAT: true,
		},
		config.Policy{
			ID: 12, Name: "block-guest-dns", Enabled: true,
			SrcIntf: []string{"guest"}, DstIntf: []string{"any"},
			SrcAddr: []string{"all"}, DstAddr: []string{"all"},
			Service: []string{"dns", "all-tcp"}, Action: "deny", Log: "deny",
		},
		config.Policy{
			ID: 13, Name: "old-rule", Enabled: false,
			SrcIntf: []string{"lan"}, DstIntf: []string{"wan1"},
			SrcAddr: []string{"all"}, DstAddr: []string{"all"}, Service: []string{"everything"},
			Action: "accept",
		},
		config.Policy{
			ID: 14, Name: "guest-out", Enabled: true, DNSFilterProfile: "family",
			SrcIntf: []string{"guest"}, DstIntf: []string{"wan1"},
			SrcAddr: []string{"everything"}, DstAddr: []string{"all"},
			Service: []string{"everything"}, Action: "accept", NAT: true, Log: "deny",
		},
	)
	cfg.NAT.PortForwards = []config.PortForward{
		{Name: "https-in", InIntf: "wan1", ExtPort: "443", Protocol: "tcp", ToAddr: "10.10.0.10", ToPort: "443"},
		{Name: "alt-web", InIntf: "wan1", ExtPort: "8080", Protocol: "tcp", ToAddr: "10.10.0.10", ToPort: "80"},
		{Name: "game", InIntf: "wan1", ExtPort: "27000-27010", Protocol: "udp", ToAddr: "192.168.1.20"},
	}
	cfg.UPnP = config.UPnP{Enabled: true, Interfaces: []string{"lan"}}
	return cfg
}

func TestGolden(t *testing.T) {
	tests := []struct {
		name string
		cfg  *config.Config
	}{
		{"default", config.Default()},
		{"full", fullConfig()},
		{"dual-stack", dualStackConfig()},
		{"multi-wan", rendertest.MultiWAN()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files, err := Renderer{}.Render(tt.cfg)
			if err != nil {
				t.Fatal(err)
			}
			if len(files) != 1 {
				t.Fatalf("got %d files, want 1", len(files))
			}
			got, ok := files[Path]
			if !ok {
				t.Fatalf("missing %s", Path)
			}
			golden := filepath.Join("testdata", tt.name+".nft")
			if *update {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run go test -update to create it)", err)
			}
			want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
			if !bytes.Equal(got, want) {
				t.Errorf("output differs from %s (run go test ./internal/render/nft -update)\n--- got ---\n%s", golden, got)
			}
		})
	}
}

// TestFullConfigIsValid keeps the golden config honest: everything the
// renderer is tested with must also pass config.Validate.
func TestFullConfigIsValid(t *testing.T) {
	if errs := config.Validate(fullConfig()); len(errs) != 0 {
		t.Fatalf("fullConfig() is not valid: %v", errs)
	}
	if errs := config.Validate(dualStackConfig()); len(errs) != 0 {
		t.Fatalf("dualStackConfig() is not valid: %v", errs)
	}
}

func TestRenderDeterministic(t *testing.T) {
	a, err := Renderer{}.Render(fullConfig())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		b, err := Renderer{}.Render(fullConfig())
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a[Path], b[Path]) {
			t.Fatal("render output is not deterministic")
		}
	}
}

func TestName(t *testing.T) {
	if got := (Renderer{}).Name(); got != "nft" {
		t.Errorf("Name() = %q, want nft", got)
	}
}

func TestRenderErrors(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*config.Config)
		wantErr string
	}{
		{"unknown src intf", func(c *config.Config) { c.Policies[0].SrcIntf = []string{"nope"} }, `unknown interface "nope"`},
		{"unknown address", func(c *config.Config) { c.Policies[0].DstAddr = []string{"nope"} }, `unknown address "nope"`},
		{"unknown service", func(c *config.Config) { c.Policies[0].Service = []string{"nope"} }, `unknown service "nope"`},
		{"unknown feed", func(c *config.Config) { c.Policies[0].ThreatFeeds = []string{"nope"} }, `unknown threat feed "nope"`},
		{"bad action", func(c *config.Config) { c.Policies[0].Action = "maybe" }, `unsupported action`},
		{"schedule", func(c *config.Config) { c.Policies[0].Schedule = "workhours" }, `unknown schedule "workhours"`},
		{"duplicate id", func(c *config.Config) { c.Policies = append(c.Policies, c.Policies[0]) }, `duplicate id`},
		{"mixed family range", func(c *config.Config) {
			c.Addresses = []config.Address{{Name: "mix", Type: "range", Value: "10.0.0.1-2001:db8::1"}}
		}, `mixes IPv4 and IPv6`},
		{"bad ports", func(c *config.Config) {
			c.Services = []config.Service{{Name: "s", Protocol: "tcp", Ports: "70000"}}
		}, `invalid port`},
		{"bad protocol", func(c *config.Config) {
			c.Services = []config.Service{{Name: "s", Protocol: "gre"}}
		}, `unsupported protocol`},
		{"group cycle", func(c *config.Config) {
			c.AddressGroups = []config.AddressGroup{{Name: "a", Members: []string{"b"}}, {Name: "b", Members: []string{"a"}}}
		}, `includes itself`},
		{"service group cycle", func(c *config.Config) {
			c.ServiceGroups = []config.ServiceGroup{{Name: "a", Members: []string{"a"}}}
		}, `includes itself`},
		{"group/address clash", func(c *config.Config) {
			c.Addresses = []config.Address{{Name: "x", Type: "host", Value: "10.0.0.1"}}
			c.AddressGroups = []config.AddressGroup{{Name: "x", Members: []string{"x"}}}
		}, `name also used by an address`},
		{"pf range remap", func(c *config.Config) {
			c.NAT.PortForwards = []config.PortForward{{Name: "p", InIntf: "wan1", ExtPort: "100-110", Protocol: "tcp", ToAddr: "192.168.1.5", ToPort: "200-210"}}
		}, `must equal ext_port`},
		{"pf port list", func(c *config.Config) {
			c.NAT.PortForwards = []config.PortForward{{Name: "p", InIntf: "wan1", ExtPort: "80,81", Protocol: "tcp", ToAddr: "192.168.1.5"}}
		}, `want one port or one range`},
		{"pf bad addr", func(c *config.Config) {
			c.NAT.PortForwards = []config.PortForward{{Name: "p", InIntf: "wan1", ExtPort: "80", Protocol: "tcp", ToAddr: "server"}}
		}, `to_addr`},
		{"pf unknown intf", func(c *config.Config) {
			c.NAT.PortForwards = []config.PortForward{{Name: "p", InIntf: "wan9", ExtPort: "80", Protocol: "tcp", ToAddr: "192.168.1.5"}}
		}, `unknown interface "wan9"`},
		{"pf bad protocol", func(c *config.Config) {
			c.NAT.PortForwards = []config.PortForward{{Name: "p", InIntf: "wan1", ExtPort: "80", Protocol: "sctp", ToAddr: "192.168.1.5"}}
		}, `unsupported protocol`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			tt.mutate(cfg)
			_, err := Renderer{}.Render(cfg)
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) || !strings.HasPrefix(err.Error(), "render nft: ") {
				t.Fatalf("error %q: want prefix %q and to contain %q", err, "render nft: ", tt.wantErr)
			}
		})
	}
}

func TestQuote(t *testing.T) {
	if got := quote(`a"b\c` + "\n"); got != `"abc"` {
		t.Errorf("quote = %s", got)
	}
	if got := quote(strings.Repeat("x", 200)); len(got) != maxComment+2 {
		t.Errorf("quote did not truncate: len %d", len(got))
	}
}

// dualStackConfig (task 045): DHCPv6 WAN, delegated LAN and guest VLAN (DNS
// filtered), IPv6 and mixed address objects, and policies that match one or
// both families.
func dualStackConfig() *config.Config {
	cfg := config.Default()
	cfg.Interfaces[0].IPv6 = &config.IPv6{Mode: "dhcp6"}
	cfg.Interfaces[1].IPv6 = &config.IPv6{Mode: "delegated"}
	cfg.Interfaces[1].AllowAccess = []string{"ping", "https"}
	cfg.VLANs = []config.VLAN{{Name: "guest", Parent: "lan", VLANID: 20, Mode: "static", Address: pfx("192.168.20.1/24"), Enabled: true,
		IPv6: &config.IPv6{Mode: "delegated", PrefixID: 1}}}
	cfg.Addresses = []config.Address{
		{Name: "nas", Type: "host", Value: "192.168.1.5"},
		{Name: "nas6", Type: "host", Value: "2001:db8:1::5"},
		{Name: "bad6", Type: "subnet", Value: "2001:db8:bad::/48"},
		{Name: "v6range", Type: "range", Value: "2001:db8:2::10-2001:db8:2::1f"},
	}
	cfg.AddressGroups = []config.AddressGroup{{Name: "nas-all", Members: []string{"nas", "nas6"}}}
	cfg.Services = []config.Service{{Name: "ping", Protocol: "icmp"}, {Name: "smb", Protocol: "tcp", Ports: "445"}}
	cfg.Profiles.DNSFilter = []config.DNSFilterProfile{{Name: "kids", BlockDomains: []string{"example.com"}}}
	pol := func(id uint32, name string, src, dst, srcAddr, dstAddr, svc []string, action string) config.Policy {
		return config.Policy{ID: id, Name: name, Enabled: true, SrcIntf: src, DstIntf: dst, SrcAddr: srcAddr, DstAddr: dstAddr, Service: svc, Action: action}
	}
	all := []string{"all"}
	cfg.Policies = []config.Policy{
		pol(10, "block-bad6", []string{"lan"}, []string{"wan1"}, all, []string{"bad6"}, all, "deny"),
		pol(11, "nas-smb", []string{"guest"}, []string{"lan"}, all, []string{"nas-all"}, []string{"smb", "ping"}, "accept"),
		pol(12, "v4-to-v6-never", []string{"lan"}, []string{"wan1"}, []string{"nas"}, []string{"v6range"}, all, "accept"),
		pol(13, "nas-or-range", []string{"lan"}, []string{"wan1"}, []string{"nas6", "v6range", "nas"}, all, all, "accept"),
		{ID: 14, Name: "guest-out", Enabled: true, SrcIntf: []string{"guest"}, DstIntf: []string{"wan1"}, SrcAddr: all, DstAddr: all,
			Service: all, Action: "accept", NAT: true, DNSFilterProfile: "kids"},
		cfg.Policies[0],
	}
	return cfg
}
func pfx(s string) *netip.Prefix { p := netip.MustParsePrefix(s); return &p }
