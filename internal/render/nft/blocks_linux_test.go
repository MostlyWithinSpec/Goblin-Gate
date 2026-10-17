//go:build linux

package nft

import (
	"bytes"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestBlockLoad loads the golden ruleset, blocks an address for a few
// seconds with BlockScript and checks that its traffic is dropped, that the
// element expires on its own, that blocking again replaces the timeout and
// that removing works. Needs root and nft in a throwaway network namespace:
// scripts/linux-test.ps1 runs it with GOBLIN_NFT_LOAD=1.
func TestBlockLoad(t *testing.T) {
	if os.Getenv("GOBLIN_NFT_LOAD") != "1" {
		t.Skip("set GOBLIN_NFT_LOAD=1 (root, throwaway netns)")
	}
	nft := func(stdin []byte, args ...string) {
		t.Helper()
		cmd := exec.Command("nft", args...)
		if stdin != nil {
			cmd.Stdin = bytes.NewReader(stdin)
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("nft %v: %v\n%s\n%s", args, err, out, stdin)
		}
	}
	exec.Command("ip", "link", "set", "lo", "up").Run()
	nft(nil, "-f", "testdata/full.nft")
	defer exec.Command("nft", "delete", "table", "inet", "goblin").Run()

	// a TCP service on another loopback address: the input chain accepts
	// lo, so only the block can stop it
	target := netip.MustParseAddr("127.0.0.2")
	ln, err := net.Listen("tcp", "127.0.0.2:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	reachable := func() bool {
		c, err := net.DialTimeout("tcp", ln.Addr().String(), 700*time.Millisecond)
		if err == nil {
			c.Close()
		}
		return err == nil
	}
	has := func() bool {
		return exec.Command("nft", "get", "element", "inet", "goblin", BlockSet4, "{", target.String(), "}").Run() == nil
	}
	if !reachable() {
		t.Fatal("not reachable before the block")
	}

	nft(BlockScript(map[netip.Addr]time.Duration{target: 3 * time.Second}, nil), "-f", "-")
	if !has() || reachable() {
		t.Fatal("block not in effect")
	}
	// blocking again (the element exists) replaces the timeout
	nft(BlockScript(map[netip.Addr]time.Duration{target: 3 * time.Second}, nil), "-f", "-")
	time.Sleep(4 * time.Second)
	if has() || !reachable() {
		t.Fatal("block did not expire")
	}

	// remove before it expires; removing a missing element works too
	nft(BlockScript(map[netip.Addr]time.Duration{target: time.Hour}, nil), "-f", "-")
	if reachable() {
		t.Fatal("second block not in effect")
	}
	nft(BlockScript(nil, []netip.Addr{target}), "-f", "-")
	nft(BlockScript(nil, []netip.Addr{target}), "-f", "-")
	if has() || !reachable() {
		t.Fatal("remove failed")
	}

	// IPv6 set
	v6 := netip.MustParseAddr("2001:db8::7")
	nft(BlockScript(map[netip.Addr]time.Duration{v6: time.Minute}, nil), "-f", "-")
	if exec.Command("nft", "get", "element", "inet", "goblin", BlockSet6, "{", v6.String(), "}").Run() != nil {
		t.Fatal("IPv6 block missing")
	}
}
