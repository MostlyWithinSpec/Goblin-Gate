package unbound

import (
	"flag"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

var update = flag.Bool("update", false, "rewrite golden files in testdata/")

func pfx(s string) *netip.Prefix { p := netip.MustParsePrefix(s); return &p }

// full has forwarders, DNSSEC, local records, static-lease hostnames, a dmz,
// a disabled interface, an internal VLAN and a VLAN on the wan.
func full() *config.Config {
	cfg := config.Default()
	cfg.System.Domain = "home.arpa"
	cfg.Interfaces = append(cfg.Interfaces,
		config.Interface{Name: "dmz", Device: "eth2", Role: "dmz", Mode: "static", Address: pfx("10.10.0.1/24"), Enabled: true},
		config.Interface{Name: "spare", Device: "eth3", Role: "lan", Mode: "static", Address: pfx("10.30.0.1/24"), Enabled: false},
	)
	cfg.VLANs = []config.VLAN{
		{Name: "guest", Parent: "lan", VLANID: 20, Mode: "static", Address: pfx("192.168.20.1/24"), Enabled: true},
		{Name: "isp", Parent: "wan1", VLANID: 7, Mode: "static", Address: pfx("198.51.100.2/30"), Enabled: true},
	}
	cfg.DNS.Forwarders = []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("9.9.9.9")}
	cfg.DNS.DNSSEC = true
	cfg.DNS.LocalRecords = map[string]string{"nas.home.arpa": "192.168.1.5", "files.home.arpa": "192.168.1.6"}
	cfg.DHCP[0].StaticLeases = []config.DHCPStaticLease{
		{MAC: "aa:bb:cc:dd:ee:01", IP: netip.MustParseAddr("192.168.1.10"), Hostname: "printer"},
		{MAC: "aa:bb:cc:dd:ee:02", IP: netip.MustParseAddr("192.168.1.11")},
	}
	return cfg
}

// fakeFeeds is a FeedSource with fixed entries.
type fakeFeeds map[string][]string

func (f fakeFeeds) Entries(name string) []string { return f[name] }

var testFeeds = fakeFeeds{
	"malware": {"evil.example", "ads.tracker.example", "allowed.example", "bad.example"},
	"adult":   {"nsfw.example"},
}

// filter adds two DNS filter profiles to full(): kids (lan, block page,
// safe search, a feed, a block and an allow list) and guests (guest VLAN,
// NXDOMAIN). dmz has no profile; a later lan policy with another profile
// does not change lan's (first match per source interface wins).
func filter() *config.Config {
	cfg := full()
	cfg.Feeds = []config.Feed{
		{Name: "malware", Type: "domain", URL: "https://example.com/m.txt", Enabled: true},
		{Name: "adult", Type: "domain", URL: "https://example.com/a.txt", Enabled: true},
	}
	cfg.Profiles.DNSFilter = []config.DNSFilterProfile{
		{Name: "kids", BlockFeeds: []string{"malware", "adult"}, BlockDomains: []string{"games.example"},
			AllowDomains: []string{"allowed.example"}, SafeSearch: true, Action: "blockpage"},
		{Name: "guests", BlockFeeds: []string{"malware"}, BlockDomains: []string{"*.torrent.example."}},
		{Name: "unused", BlockDomains: []string{"x.example"}},
	}
	cfg.Policies = []config.Policy{
		{ID: 1, Name: "lan-out", Enabled: true, SrcIntf: []string{"lan"}, DstIntf: []string{"wan1"},
			SrcAddr: []string{"all"}, DstAddr: []string{"all"}, Service: []string{"all"}, Action: "accept", NAT: true, DNSFilterProfile: "kids"},
		{ID: 2, Name: "guest-out", Enabled: true, SrcIntf: []string{"guest"}, DstIntf: []string{"wan1"},
			SrcAddr: []string{"all"}, DstAddr: []string{"all"}, Service: []string{"all"}, Action: "accept", NAT: true, DNSFilterProfile: "guests"},
		{ID: 3, Name: "lan-again", Enabled: true, SrcIntf: []string{"lan", "dmz"}, DstIntf: []string{"wan1"},
			SrcAddr: []string{"all"}, DstAddr: []string{"all"}, Service: []string{"all"}, Action: "deny", DNSFilterProfile: "unused"},
	}
	return cfg
}

// TestGolden compares the rendered files with testdata/<case>.conf and
// testdata/<case>-<profile>.zone.
// Regenerate with: go test ./internal/render/unbound -update
// scripts/linux-test.ps1 runs the real unbound-checkconf on rendered configs.
func TestGolden(t *testing.T) {
	cases := map[string]*config.Config{"default": config.Default(), "full": full(), "filter": filter()}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			if err := config.Validate(cfg); err != nil {
				t.Fatalf("fixture is not a valid config: %v", err)
			}
			files, err := Renderer{Feeds: testFeeds}.Render(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if files[Path] == nil {
				t.Fatalf("missing %s, got %v", Path, files)
			}
			goldens := map[string]string{Path: filepath.Join("testdata", name+".conf")}
			for p := range files {
				if p != Path {
					goldens[p] = filepath.Join("testdata", name+"-"+strings.TrimSuffix(filepath.Base(p), ".zone")+".zone")
				}
			}
			for p, golden := range goldens {
				got := files[p]
				if *update {
					if err := os.MkdirAll("testdata", 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(golden, got, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				want, err := os.ReadFile(golden)
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != strings.ReplaceAll(string(want), "\r\n", "\n") {
					t.Errorf("differs from %s:\n--- got\n%s--- want\n%s", golden, got, want)
				}
			}
		})
	}
}

func TestDNSFilter(t *testing.T) {
	files, err := Renderer{Feeds: testFeeds}.Render(filter())
	if err != nil {
		t.Fatal(err)
	}
	conf := string(files[Path])
	for _, want := range []string{
		`module-config: "respip validator iterator"`,
		`define-tag: "gg_guests gg_kids"`,
		`access-control-tag: 192.168.1.0/24 "gg_kids"`,
		`access-control-tag: 192.168.20.0/24 "gg_guests"`,
		`zonefile: "/var/lib/unbound/rpz/kids.zone"`,
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("config missing %q", want)
		}
	}
	for _, bad := range []string{"10.10.0.0/24 \"gg", "gg_unused", "rpz/unused"} {
		if strings.Contains(conf, bad) {
			t.Errorf("config must not contain %q", bad)
		}
	}
	if len(files) != 3 {
		t.Fatalf("files: %d, want conf + 2 zones", len(files))
	}
	kids := string(files[ZonePath("kids")])
	for _, want := range []string{
		"$ORIGIN kids.rpz.goblin.\n",
		"evil.example A 192.168.1.1\n*.evil.example A 192.168.1.1\n",
		"nsfw.example A 192.168.1.1\n",
		"games.example A 192.168.1.1\n",
		"allowed.example CNAME rpz-passthru.\n",
		"www.google.com CNAME forcesafesearch.google.com.\n",
	} {
		if !strings.Contains(kids, want) {
			t.Errorf("kids zone missing %q", want)
		}
	}
	if strings.Contains(kids, "allowed.example A") {
		t.Error("an allowed name is blocked")
	}
	guests := string(files[ZonePath("guests")])
	if !strings.Contains(guests, "torrent.example CNAME .\n*.torrent.example CNAME .\n") || strings.Contains(guests, "google") {
		t.Errorf("guests zone:\n%s", guests)
	}
	// without a feed source the zones still render (feeds arrive later)
	if f, err := (Renderer{}).Render(filter()); err != nil || strings.Contains(string(f[ZonePath("kids")]), "evil.example") {
		t.Fatalf("nil feeds: %v", err)
	}
}

func TestListen(t *testing.T) {
	files, err := Renderer{}.Render(full())
	if err != nil {
		t.Fatal(err)
	}
	s := string(files[Path])
	for _, want := range []string{
		"interface: 192.168.1.1\n", "interface: 10.10.0.1\n", "interface: 192.168.20.1\n",
		"access-control: 192.168.20.0/24 allow\n",
		`local-data: "printer.home.arpa A 192.168.1.10"`,
		`module-config: "validator iterator"`,
		"forward-addr: 9.9.9.9\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, bad := range []string{"10.30.0.1", "198.51.100", "auto-trust-anchor-file"} {
		if strings.Contains(s, bad) {
			t.Errorf("must not contain %q (disabled interface, wan VLAN, Debian's anchor)", bad)
		}
	}
}

// TestListenIPv6 (task 045): with IPv6, Unbound listens on every address
// and allows link-local, static LAN prefixes and (with a delegation) all
// global and unique-local space; WAN static prefixes stay refused.
func TestListenIPv6(t *testing.T) {
	cfg := config.Default()
	wan6 := netip.MustParsePrefix("2001:db8::2/64")
	lan6 := netip.MustParsePrefix("fd00:1::1/64")
	cfg.Interfaces[0].IPv6 = &config.IPv6{Mode: "static", Address: &wan6}
	cfg.Interfaces[1].IPv6 = &config.IPv6{Mode: "static", Address: &lan6}
	s := renderConf(t, cfg)
	for _, want := range []string{"interface: ::0\n", "interface-automatic: yes\n", "do-ip6: yes\n",
		"access-control: fe80::/10 allow\n", "access-control: fd00:1::/64 allow\n", "access-control: ::/0 refuse\n",
		"access-control: 192.168.1.0/24 allow\n"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, bad := range []string{"2001:db8::", "2000::/3", "ip-freebind"} {
		if strings.Contains(s, bad) {
			t.Errorf("must not contain %q", bad)
		}
	}
	cfg.Interfaces[0].IPv6 = &config.IPv6{Mode: "dhcp6"}
	cfg.Interfaces[1].IPv6 = &config.IPv6{Mode: "delegated"}
	if s := renderConf(t, cfg); !strings.Contains(s, "access-control: 2000::/3 allow\n") {
		t.Error("a delegated LAN must allow global unicast")
	}
	if s := renderConf(t, config.Default()); strings.Contains(s, "::0") || !strings.Contains(s, "do-ip6: no\n") {
		t.Error("IPv4-only configs must keep listening on their addresses only")
	}
}

func renderConf(t *testing.T, cfg *config.Config) string {
	t.Helper()
	if errs := config.Validate(cfg); len(errs) > 0 {
		t.Fatalf("fixture invalid: %v", errs)
	}
	files, err := Renderer{}.Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return string(files[Path])
}
