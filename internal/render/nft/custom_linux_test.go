//go:build linux

package nft

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestCustomRulesLoad loads a ruleset with custom rules in every hook and
// checks that a broken rule is blamed on its entry by real nft output.
// Root and nft in a throwaway namespace: scripts/linux-test.ps1 runs it
// with GOBLIN_NFT_LOAD=1.
func TestCustomRulesLoad(t *testing.T) {
	if os.Getenv("GOBLIN_NFT_LOAD") != "1" {
		t.Skip("set GOBLIN_NFT_LOAD=1 (root, throwaway netns)")
	}
	cfg := customConfig()
	rules, err := build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("nft", "-f", "-")
	cmd.Stdin = bytes.NewReader(rules)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("nft -f: %v\n%s", err, out)
	}
	for _, args := range [][]string{
		{"list", "counter", "inet", "goblin", "custom_node_exporter"},
		{"list", "counter", "ip", "goblin_nat", "custom_redirect_ntp"},
	} {
		if out, err := exec.Command("nft", args...).CombinedOutput(); err != nil {
			t.Fatalf("nft %v: %v\n%s", args, err, out)
		}
	}
	if errs := CheckCustomRules(context.Background(), cfg, nil); errs != nil {
		t.Fatalf("valid rules refused: %v", errs)
	}

	cfg.CustomRules[3].Rule = `iifname "eth1" udp dport 123 frobnicate`
	errs := CheckCustomRules(context.Background(), cfg, nil)
	if len(errs) != 1 || errs[0].Path != "custom_rules[3].rule" || !strings.Contains(errs[0].Message, `custom rule "redirect-ntp": nft: `) {
		t.Fatalf("broken rule: %v", errs)
	}
	t.Logf("nft said: %s", errs[0].Message)
}
