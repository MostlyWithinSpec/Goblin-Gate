//go:build linux

package webfilter_test

import (
	"bytes"
	"context"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/logs"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/unbound"
	"github.com/networkgoblinlabs/goblin-gate/internal/webfilter"
)

// TestWebFilterE2E runs the proxy in front of a real Unbound: a subnet with
// both a web filter profile (gambling blocked) and a DNS filter profile
// (example.org blocked). The proxy must answer blocked categories itself and
// forward the rest from the box address, so Unbound still applies the DNS
// filter of that subnet. Needs root, unbound, dig, internet and a throwaway
// container: scripts/linux-test.ps1 runs it with GOBLIN_WF_E2E=1.
func TestWebFilterE2E(t *testing.T) {
	if os.Getenv("GOBLIN_WF_E2E") != "1" {
		t.Skip("set GOBLIN_WF_E2E=1 (root, throwaway container)")
	}
	sh := func(args ...string) {
		t.Helper()
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	exec.Command("ip", "link", "del", "ggwf").Run()
	sh("ip", "link", "add", "ggwf", "type", "dummy")
	sh("ip", "addr", "add", "192.168.81.1/24", "dev", "ggwf")
	sh("ip", "link", "set", "ggwf", "up")

	pfx := netip.MustParsePrefix("192.168.81.1/24")
	cfg := config.Default()
	cfg.Interfaces = []config.Interface{
		{Name: "wan1", Device: "eth0", Role: "wan", Mode: "dhcp", Enabled: true},
		{Name: "kids", Device: "ggwf", Role: "lan", Mode: "static", Address: &pfx, Enabled: true},
	}
	cfg.DHCP = nil
	cfg.DNS.DNSSEC = false
	cfg.DNS.Forwarders = []netip.Addr{netip.MustParseAddr("1.1.1.1")}
	cfg.Profiles.DNSFilter = []config.DNSFilterProfile{{Name: "kids", BlockDomains: []string{"example.org"}}}
	cfg.Profiles.WebFilter = []config.WebFilterProfile{{Name: "kids", Categories: map[string]string{"gambling": "block"}}}
	cfg.Policies = []config.Policy{{ID: 1, Name: "kids", Enabled: true, SrcIntf: []string{"kids"}, DstIntf: []string{"wan1"},
		SrcAddr: []string{"all"}, DstAddr: []string{"all"}, Service: []string{"all"}, Action: "accept",
		DNSFilterProfile: "kids", WebFilterProfile: "kids"}}
	if errs := config.Validate(cfg); len(errs) > 0 {
		t.Fatal(errs)
	}
	files, err := unbound.Renderer{}.Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for p, b := range files {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, b, 0o644)
	}
	ub := exec.Command("unbound", "-d", "-c", "/etc/unbound/unbound.conf")
	if err := ub.Start(); err != nil {
		t.Fatal(err)
	}
	defer ub.Process.Kill()

	b := webfilter.NewBuilder()
	b.Add("gambling", "wikipedia.org")
	var buf bytes.Buffer
	b.WriteTo(&buf)
	db, _ := webfilter.Read(&buf)
	var events []logs.Event
	f := &webfilter.Filter{Event: func(e logs.Event) { events = append(events, e) }}
	f.SetDB(db)
	f.Resync(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pc, err := net.ListenPacket("udp", "192.168.81.1:5300")
	if err != nil {
		t.Fatal(err)
	}
	go f.ServeUDP(ctx, pc)

	dig := func(name string) string {
		out, _ := exec.Command("dig", "+time=3", "+tries=1", "-p", "5300", "@192.168.81.1", "-b", "192.168.81.1", name, "A").CombinedOutput()
		return string(out)
	}
	// answer is the ANSWER section only (the ";; SERVER:" line also names
	// the box address)
	answer := func(out string) string {
		var lines []string
		in := false
		for _, l := range strings.Split(out, "\n") {
			switch {
			case strings.HasPrefix(l, ";; ANSWER SECTION"):
				in = true
			case in && strings.TrimSpace(l) == "":
				in = false
			case in:
				lines = append(lines, strings.Join(strings.Fields(l), " "))
			}
		}
		return strings.Join(lines, " | ")
	}
	deadline := time.Now().Add(15 * time.Second)
	for !strings.Contains(dig("example.com"), "status: NOERROR") {
		if time.Now().After(deadline) {
			t.Fatalf("no answer through the proxy:\n%s", dig("example.com"))
		}
		time.Sleep(300 * time.Millisecond)
	}
	if out := dig("en.wikipedia.org"); answer(out) != "en.wikipedia.org. 60 IN A 192.168.81.1" {
		t.Errorf("gambling not answered with the block page address:\n%s", out)
	}
	if out := dig("www.example.org"); !strings.Contains(out, "status: NXDOMAIN") {
		t.Errorf("the DNS filter of the subnet did not apply through the proxy:\n%s", out)
	}
	if out := dig("example.com"); !strings.Contains(out, "status: NOERROR") || answer(out) == "" || strings.Contains(answer(out), "192.168.81.1") {
		t.Errorf("an allowed name was not resolved normally:\n%s", out)
	}
	if len(events) != 1 || events[0].Action != "deny" || events[0].Category != "gambling" {
		t.Errorf("events: %+v", events)
	}
}
