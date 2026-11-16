//go:build linux

package nft

import (
	"bytes"
	"net/netip"
	"os"
	"os/exec"
	"slices"
	"testing"
)

// TestCountryBatchLoad loads a ruleset with country objects, fills the sets
// through CountryBatch twice (a database refresh) and checks membership.
// Root and nft in a throwaway namespace: scripts/linux-test.ps1 runs it
// with GOBLIN_NFT_LOAD=1.
func TestCountryBatchLoad(t *testing.T) {
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
	cfg := geoConfig()
	rules, err := build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	nft(rules, "-f", "-")
	sets, _ := CountrySets(cfg)
	r := func(lo, hi string) Range { return Range{netip.MustParseAddr(lo), netip.MustParseAddr(hi)} }
	nz := []Range{r("203.0.113.0", "203.0.113.255"), r("2001:db8:a::", "2001:db8:a::ffff")}
	nft(CountryBatch(sets, func(c string) []Range {
		if c == "NZ" {
			return nz
		}
		return nil
	}), "-f", "-")
	for _, c := range []struct {
		set, addr string
		want      bool
	}{
		{"addr_nz", "203.0.113.9", true},
		{"addr6_nz", "2001:db8:a::5", true},
		{"addrgrp_trusted", "203.0.113.9", true},
		{"addrgrp_trusted", "198.51.100.7", true}, // the group's fixed member survives the flush
		{"addr_north_america", "203.0.113.9", false},
	} {
		if got := has(c.set, c.addr); got != c.want {
			t.Errorf("%s has %s = %v, want %v", c.set, c.addr, got, c.want)
		}
	}
	// a refresh replaces the ranges
	nz = []Range{r("192.0.2.0", "192.0.2.255")}
	nft(CountryBatch(sets, func(c string) []Range {
		if c == "NZ" {
			return nz
		}
		return nil
	}), "-f", "-")
	if has("addr_nz", "203.0.113.9") || !has("addr_nz", "192.0.2.1") || !has("addrgrp_trusted", "198.51.100.7") {
		t.Error("refresh did not replace the ranges")
	}
}

// TestLiveBatchLoad loads a ruleset with a live object (alone, in a group
// with a country and a fixed host, and in a policy union), fills it with
// RuntimeBatch, then refills with new members: removed members go, the
// country ranges and the fixed host stay. GOBLIN_NFT_LOAD=1 as above.
func TestLiveBatchLoad(t *testing.T) {
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
	cfg := liveConfig()
	rules, err := build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	nft(rules, "-f", "-")
	sets, _ := RuntimeSets(cfg)
	r := func(lo, hi string) Range { return Range{netip.MustParseAddr(lo), netip.MustParseAddr(hi)} }
	ranges := func(c string) []Range {
		if c == "NZ" {
			return []Range{r("203.0.113.0", "203.0.113.255")}
		}
		return nil
	}
	members := []netip.Prefix{netip.MustParsePrefix("10.20.0.5/32"), netip.MustParsePrefix("10.20.1.0/24"), netip.MustParsePrefix("fd00:20::/64")}
	live := func(o string) []netip.Prefix {
		if o == "containers" {
			return members
		}
		return nil
	}
	nft(RuntimeBatch(sets, ranges, live), "-f", "-")
	for _, c := range []struct {
		set, addr string
		want      bool
	}{
		{"addr_containers", "10.20.0.5", true},
		{"addr_containers", "10.20.1.77", true},
		{"addr6_containers", "fd00:20::9", true},
		{"addrgrp_lab", "10.20.0.5", true},
		{"addrgrp_lab", "203.0.113.9", true},  // its country
		{"addrgrp_lab", "198.51.100.7", true}, // its fixed host
		{"addr_containers", "10.20.0.6", false},
	} {
		if got := has(c.set, c.addr); got != c.want {
			t.Errorf("%s has %s = %v, want %v", c.set, c.addr, got, c.want)
		}
	}
	// the plugin replaces the members: 10.20.1.0/24 goes, 10.20.9.9 comes
	members = []netip.Prefix{netip.MustParsePrefix("10.20.0.5/32"), netip.MustParsePrefix("10.20.9.9/32")}
	var touched []RuntimeSet
	for _, s := range sets {
		if slices.Contains(s.Live, "containers") {
			touched = append(touched, s)
		}
	}
	nft(RuntimeBatch(touched, ranges, live), "-f", "-")
	if has("addr_containers", "10.20.1.77") || !has("addr_containers", "10.20.9.9") || has("addr6_containers", "fd00:20::9") {
		t.Error("the refill did not replace the members")
	}
	if !has("addrgrp_lab", "203.0.113.9") || !has("addrgrp_lab", "198.51.100.7") {
		t.Error("the live refill dropped the group's country or fixed member")
	}
}
