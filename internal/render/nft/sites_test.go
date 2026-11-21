package nft

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/render/rendertest"
)

// Site tunnels (task 155): their WAN port, ping from the other gates, the
// shared admin access, and policies on the site zone.
func TestSitesRuleset(t *testing.T) {
	cfg := rendertest.Sites()
	files, err := Renderer{}.Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var rs string
	for _, b := range files {
		rs += string(b)
	}
	for _, want := range []string{
		`udp dport 51830 accept comment "site cabin"`,
		`iifname "cabin" icmp type echo-request accept comment "site cabin: ping"`,
		`iifname "cabin" tcp dport 443 accept comment "site cabin: https admin"`,
		`oifname "cabin"`,
	} {
		if !strings.Contains(rs, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, bad := range []string{"51831", `"flat"`, `"office"`, `"site cabin: ssh admin"`} {
		if strings.Contains(rs, bad) {
			t.Errorf("unexpected %q", bad)
		}
	}
}

func TestSitesSimulate(t *testing.T) {
	cfg := rendertest.Sites()
	ip := netip.MustParseAddr
	for name, tc := range map[string]struct {
		f    SimFlow
		want string
		in   string
	}{
		"lan to cabin":       {SimFlow{SrcIP: ip("192.168.1.30"), DstIP: ip("192.168.50.9"), Protocol: "tcp", DstPort: 80}, "accept", "lan"},
		"lan to office":      {SimFlow{SrcIP: ip("192.168.1.30"), DstIP: ip("192.168.60.9"), Protocol: "tcp", DstPort: 80}, "accept", "lan"},
		"cabin ssh to nas":   {SimFlow{SrcIP: ip("192.168.50.9"), DstIP: ip("192.168.1.20"), Protocol: "tcp", DstPort: 22}, "accept", "cabin"},
		"cabin http to nas":  {SimFlow{SrcIP: ip("192.168.50.9"), DstIP: ip("192.168.1.20"), Protocol: "tcp", DstPort: 80}, "deny", "cabin"},
		"office via cabin":   {SimFlow{SrcIP: ip("192.168.60.9"), DstIP: ip("192.168.1.20"), Protocol: "tcp", DstPort: 22}, "accept", "cabin"},
		"cabin gate pings":   {SimFlow{SrcIP: ip("10.254.2.2"), DstIP: ip("10.254.1.1"), Protocol: "icmp"}, "accept", "cabin"},
		"cabin gate ssh box": {SimFlow{SrcIP: ip("10.254.2.2"), DstIP: ip("10.254.1.1"), Protocol: "tcp", DstPort: 22}, "deny", "cabin"},
	} {
		res, err := Simulate(cfg, tc.f, SimRuntime{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.Verdict != tc.want || res.SrcIntf != tc.in {
			t.Errorf("%s: %s on %s (%s), want %s on %s", name, res.Verdict, res.SrcIntf, res.Reason, tc.want, tc.in)
		}
	}
}
