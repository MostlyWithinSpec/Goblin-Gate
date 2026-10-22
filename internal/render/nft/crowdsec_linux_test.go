//go:build linux

package nft

import (
	"bytes"
	"net/netip"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestCrowdSecLoad loads a ruleset with CrowdSec on, applies bans and a
// removal twice (the script must work whatever the sets hold) and checks
// membership. scripts/linux-test.ps1 runs it with GOBLIN_NFT_LOAD=1.
func TestCrowdSecLoad(t *testing.T) {
	if os.Getenv("GOBLIN_NFT_LOAD") != "1" {
		t.Skip("set GOBLIN_NFT_LOAD=1 (root, throwaway netns)")
	}
	nft := func(stdin []byte, args ...string) {
		t.Helper()
		cmd := exec.Command("nft", args...)
		cmd.Stdin = bytes.NewReader(stdin)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("nft %v: %v\n%s", args, err, out)
		}
	}
	has := func(set, addr string) bool {
		return exec.Command("nft", "get", "element", "inet", "goblin", set, "{", addr, "}").Run() == nil
	}
	rules, err := build(crowdSecConfig(""))
	if err != nil {
		t.Fatal(err)
	}
	nft(rules, "-f", "-")
	bans := []CrowdSecBan{
		{Prefix: netip.MustParsePrefix("203.0.113.9/32"), Left: time.Hour},
		{Prefix: netip.MustParsePrefix("198.51.100.0/24"), Left: time.Hour},
		{Prefix: netip.MustParsePrefix("2001:db8::66/128"), Left: time.Hour},
	}
	for range 2 {
		nft(CrowdSecScript(bans, nil), "-f", "-")
	}
	for _, c := range []struct {
		set, addr string
		want      bool
	}{
		{CrowdSecSet4, "203.0.113.9", true},
		{CrowdSecRange4, "198.51.100.77", true},
		{CrowdSecSet6, "2001:db8::66", true},
		{CrowdSecSet4, "203.0.113.10", false},
	} {
		if got := has(c.set, c.addr); got != c.want {
			t.Errorf("%s has %s = %v, want %v", c.set, c.addr, got, c.want)
		}
	}
	for range 2 {
		nft(CrowdSecScript(nil, []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}), "-f", "-")
	}
	if has(CrowdSecRange4, "198.51.100.77") {
		t.Error("removed range still banned")
	}
}
