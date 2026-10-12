//go:build linux

package nft

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestFeedBatchLoad loads the golden ruleset into the kernel, then fills
// and replaces a feed set with 200k entries through FeedBatch. Needs root
// and nft in a throwaway network namespace: scripts/linux-test.ps1 runs it
// with GOBLIN_NFT_LOAD=1.
func TestFeedBatchLoad(t *testing.T) {
	if os.Getenv("GOBLIN_NFT_LOAD") != "1" {
		t.Skip("set GOBLIN_NFT_LOAD=1 (root, throwaway netns)")
	}
	nft := func(stdin []byte, args ...string) string {
		t.Helper()
		cmd := exec.Command("nft", args...)
		if stdin != nil {
			cmd.Stdin = bytes.NewReader(stdin)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("nft %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	has := func(addr string) bool {
		err := exec.Command("nft", "get", "element", "inet", "goblin", "feed_spamhaus_drop", "{", addr, "}").Run()
		return err == nil
	}
	nft(nil, "-f", "testdata/full.nft")

	gen := func(first int) []string {
		var e []string
		for i := range 200_000 {
			n := first + i
			e = append(e, fmt.Sprintf("10.%d.%d.%d", n>>16&255, n>>8&255, n&255))
		}
		e = append(e, "198.51.100.0/24")
		return e
	}
	start := time.Now()
	nft(FeedBatch("feed_spamhaus_drop", gen(0)), "-f", "-")
	t.Logf("loaded 200k entries in %v", time.Since(start))
	if !has("10.0.0.5") || !has("198.51.100.77") || has("10.200.0.1") {
		t.Fatal("membership wrong after the first load")
	}

	start = time.Now()
	nft(FeedBatch("feed_spamhaus_drop", gen(1_000_000)), "-f", "-")
	t.Logf("replaced 200k entries in %v", time.Since(start))
	if has("10.0.0.5") || !has("10.15.66.64") {
		t.Fatal("old entries survived the replace, or new ones are missing")
	}

	// a commit reloads the ruleset: the set starts empty again
	nft(nil, "-f", "testdata/full.nft")
	if has("10.15.66.64") || !strings.Contains(nft(nil, "list", "set", "inet", "goblin", "feed_spamhaus_drop"), "feed_spamhaus_drop") {
		t.Fatal("set not reset by the reload")
	}
}
