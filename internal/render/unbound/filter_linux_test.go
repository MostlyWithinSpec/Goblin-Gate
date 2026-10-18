//go:build linux

package unbound

import (
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// TestDNSFilterE2E runs the rendered config in a real Unbound: two dummy
// subnets with different profiles, queried with dig from each. Needs root,
// unbound, dig, internet access and a throwaway container:
// scripts/linux-test.ps1 runs it with GOBLIN_UNBOUND_E2E=1.
func TestDNSFilterE2E(t *testing.T) {
	if os.Getenv("GOBLIN_UNBOUND_E2E") != "1" {
		t.Skip("set GOBLIN_UNBOUND_E2E=1 (root, throwaway container)")
	}
	sh := func(args ...string) string {
		t.Helper()
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	for _, l := range [][2]string{{"ggkids", "192.168.71.1/24"}, {"ggguest", "192.168.72.1/24"}, {"ggopen", "192.168.73.1/24"}} {
		exec.Command("ip", "link", "del", l[0]).Run()
		sh("ip", "link", "add", l[0], "type", "dummy")
		sh("ip", "addr", "add", l[1], "dev", l[0])
		sh("ip", "link", "set", l[0], "up")
	}

	cfg := config.Default()
	cfg.Interfaces = []config.Interface{
		{Name: "wan1", Device: "eth0", Role: "wan", Mode: "dhcp", Enabled: true},
		{Name: "kids", Device: "ggkids", Role: "lan", Mode: "static", Address: pfx("192.168.71.1/24"), Enabled: true},
		{Name: "guest", Device: "ggguest", Role: "lan", Mode: "static", Address: pfx("192.168.72.1/24"), Enabled: true},
		{Name: "open", Device: "ggopen", Role: "lan", Mode: "static", Address: pfx("192.168.73.1/24"), Enabled: true},
	}
	cfg.DHCP = nil
	cfg.DNS.DNSSEC = false
	cfg.DNS.Forwarders = []netip.Addr{netip.MustParseAddr("1.1.1.1")}
	cfg.Feeds = []config.Feed{{Name: "bad", Type: "domain", URL: "https://example.com/x", Enabled: true}}
	cfg.Profiles.DNSFilter = []config.DNSFilterProfile{
		{Name: "kids", BlockFeeds: []string{"bad"}, AllowDomains: []string{"docs.wikipedia.org"}, SafeSearch: true, Action: "blockpage"},
		{Name: "guests", BlockDomains: []string{"example.org"}},
	}
	pol := func(id uint32, src, prof string) config.Policy {
		return config.Policy{ID: id, Name: src, Enabled: true, SrcIntf: []string{src}, DstIntf: []string{"wan1"},
			SrcAddr: []string{"all"}, DstAddr: []string{"all"}, Service: []string{"all"}, Action: "accept", DNSFilterProfile: prof}
	}
	cfg.Policies = []config.Policy{pol(1, "kids", "kids"), pol(2, "guest", "guests"), pol(3, "open", "")}
	if errs := config.Validate(cfg); len(errs) > 0 {
		t.Fatal(errs)
	}
	files, err := Renderer{Feeds: fakeFeeds{"bad": {"example.org", "wikipedia.org"}}}.Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for p, data := range files {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sh("unbound-checkconf")
	// the unit's RuntimeDirectory on the box
	if err := os.MkdirAll(filepath.Dir(ControlSocket), 0o755); err != nil {
		t.Fatal(err)
	}
	unbound := exec.Command("unbound", "-d", "-c", "/etc/unbound/unbound.conf")
	if err := unbound.Start(); err != nil {
		t.Fatal(err)
	}
	defer unbound.Process.Kill()

	dig := func(from, name string) string {
		out, _ := exec.Command("dig", "+time=3", "+tries=1", "@"+from, "-b", from, name, "A").CombinedOutput()
		return string(out)
	}
	deadline := time.Now().Add(15 * time.Second)
	for !strings.Contains(dig("192.168.73.1", "example.org"), "status: NOERROR") {
		if time.Now().After(deadline) {
			t.Fatalf("unbound did not answer:\n%s", dig("192.168.73.1", "example.org"))
		}
		time.Sleep(300 * time.Millisecond)
	}
	answer := func(out string) string {
		var lines []string
		for _, l := range strings.Split(out, "\n") {
			if l != "" && !strings.HasPrefix(l, ";") && strings.Contains(l, "\tIN\t") {
				lines = append(lines, strings.Join(strings.Fields(l), " "))
			}
		}
		return strings.Join(lines, " | ")
	}
	check := func(from, name, want string) {
		t.Helper()
		out := dig(from, name)
		if !strings.Contains(out, want) && !strings.Contains(answer(out), want) {
			t.Errorf("%s from %s: want %q\n%s", name, from, want, out)
		} else {
			t.Logf("%-28s from %-13s -> %s", name, from, firstNonEmpty(answer(out), statusOf(out)))
		}
	}
	// kids: block page, subdomains too, allow list wins, safe search
	check("192.168.71.1", "example.org", "example.org. 300 IN A 192.168.71.1")
	check("192.168.71.1", "www.example.org", "IN A 192.168.71.1")
	check("192.168.71.1", "en.wikipedia.org", "IN A 192.168.71.1")
	if a := answer(dig("192.168.71.1", "docs.wikipedia.org")); strings.Contains(a, "192.168.71.1") {
		t.Errorf("allowed name blocked: %s", a)
	}
	check("192.168.71.1", "www.google.com", "forcesafesearch.google.com")
	// guests: NXDOMAIN, other names resolve normally, no safe search
	check("192.168.72.1", "example.org", "status: NXDOMAIN")
	check("192.168.72.1", "sub.example.org", "status: NXDOMAIN")
	if a := answer(dig("192.168.72.1", "www.google.com")); strings.Contains(a, "forcesafesearch") || a == "" {
		t.Errorf("guests got safe search or no answer: %q", a)
	}
	// open subnet: no filtering
	if a := answer(dig("192.168.73.1", "example.org")); a == "" || strings.Contains(a, "192.168.7") {
		t.Errorf("unfiltered subnet was filtered: %q", a)
	}
	// unbound-control can reload a zone (the feed syncer's path)
	sh("unbound-control", "-c", "/etc/unbound/unbound.conf", "auth_zone_reload", ZoneName("kids"))
}

func statusOf(out string) string {
	if i := strings.Index(out, "status: "); i >= 0 {
		return strings.TrimRight(strings.Fields(out[i+8:])[0], ",")
	}
	return "?"
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
