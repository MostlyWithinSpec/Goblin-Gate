package geoip

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// liveCfg: a live object "containers" on its own and in a group with a
// country, so one set needs both sources.
func liveCfg(source string) *config.Config {
	cfg := geoCfg(source)
	cfg.Plugins = []config.Plugin{{Name: "homelab", Enabled: true, Capabilities: []string{"objects:write"}}}
	cfg.Addresses = append(cfg.Addresses, config.Address{Name: "containers", Type: "live", Value: "homelab"})
	cfg.AddressGroups = []config.AddressGroup{{Name: "mixed", Members: []string{"containers", "nz"}}}
	cfg.Policies = append(cfg.Policies,
		config.Policy{ID: 2, Name: "lab", Enabled: true, SrcIntf: []string{"lan"}, DstIntf: []string{"wan1"}, SrcAddr: []string{"containers"}, DstAddr: []string{"all"}, Service: []string{"any"}, Action: "deny"},
		config.Policy{ID: 3, Name: "mixed", Enabled: true, SrcIntf: []string{"lan"}, DstIntf: []string{"wan1"}, SrcAddr: []string{"all"}, DstAddr: []string{"mixed"}, Service: []string{"any"}, Action: "deny"},
	)
	return cfg
}

func TestLiveRefill(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/db-2026-10.csv.gz" {
			w.Write(gz(sample))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	m, scripts := testManager(t, srv, time.Date(2026, 10, 2, 8, 0, 0, 0, time.UTC))
	members := []netip.Prefix{netip.MustParsePrefix("10.20.0.5/32")}
	m.Live = func(o string) []netip.Prefix {
		if o == "containers" {
			return members
		}
		return nil
	}
	cfg := liveCfg(srv.URL + "/db-{month}.csv.gz")

	// no database yet: the live sets are still filled, country-only ones not
	m.Resync(cfg)
	if len(*scripts) != 1 {
		t.Fatalf("%d scripts", len(*scripts))
	}
	s := string((*scripts)[0])
	if !strings.Contains(s, "add element inet goblin addr_containers { 10.20.0.5 }") || !strings.Contains(s, "add element inet goblin addrgrp_mixed { 10.20.0.5 }") ||
		strings.Contains(s, "addr_nz") {
		t.Fatalf("without a database:\n%s", s)
	}

	// the database arrives: the group gets both parts in one go
	if err := m.Ensure(context.Background(), cfg.GeoIP.SourceURL); err != nil {
		t.Fatal(err)
	}
	s = string((*scripts)[len(*scripts)-1])
	if !strings.Contains(s, "add element inet goblin addrgrp_mixed { 10.20.0.5, 203.0.113.0/24 }") || !strings.Contains(s, "addr_nz { 203.0.113.0/24 }") {
		t.Fatalf("with a database:\n%s", s)
	}

	// members change: only the sets holding the object, still with countries
	n := len(*scripts)
	members = []netip.Prefix{netip.MustParsePrefix("10.20.0.6/32")}
	m.RefillLive("containers")
	if len(*scripts) != n+1 {
		t.Fatal("RefillLive loaded nothing")
	}
	s = string((*scripts)[n])
	if !strings.Contains(s, "addrgrp_mixed { 10.20.0.6, 203.0.113.0/24 }") || strings.Contains(s, "10.20.0.5") || strings.Contains(s, "addr_nz ") {
		t.Fatalf("refill live:\n%s", s)
	}
	// an object no set holds: nothing to do
	m.RefillLive("nope")
	if len(*scripts) != n+1 {
		t.Error("refilled for an unknown object")
	}
}
