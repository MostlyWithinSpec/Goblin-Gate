package liveobjects

import (
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

func TestParseMembers(t *testing.T) {
	got, err := ParseMembers([]string{"10.0.0.5", "10.0.1.7/24", "::ffff:10.0.0.9", "fd00::1", "fd00:1::/64", "10.0.0.5"})
	if err != nil {
		t.Fatal(err)
	}
	var s []string
	for _, p := range got {
		s = append(s, p.String())
	}
	// sorted, unique, hosts as /32 and /128, prefixes masked, IPv4-mapped as IPv4
	if strings.Join(s, " ") != "10.0.0.5/32 10.0.0.9/32 10.0.1.0/24 fd00::1/128 fd00:1::/64" {
		t.Errorf("parsed %v", s)
	}
	for _, bad := range []string{"10.0.0.0/8", "0.0.0.0/0", "fd00::/32", "::/0", "example.com", "fe80::1%eth0", "::ffff:10.0.0.0/120", ""} {
		if _, err := ParseMembers([]string{bad}); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	many := make([]string, MaxMembers+1)
	for i := range many {
		many[i] = netip.AddrFrom4([4]byte{10, byte(i >> 16), byte(i >> 8), byte(i)}).String()
	}
	if _, err := ParseMembers(many); err == nil {
		t.Error("accepted more than MaxMembers")
	}
	if got, err := ParseMembers([]string{}); err != nil || len(got) != 0 {
		t.Errorf("an empty list clears: %v %v", got, err)
	}
}

func TestStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "live.json")
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	s := &Store{Path: path, Now: func() time.Time { return now }}
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	m, _ := ParseMembers([]string{"10.0.0.5", "10.0.0.6"})
	ch, err := s.Set("containers", "homelab", m)
	if err != nil || !ch.Changed || ch.Added != 2 || ch.Removed != 0 || ch.Members != 2 {
		t.Fatalf("first set: %+v %v", ch, err)
	}
	// the same members again: no change
	if ch, _ := s.Set("containers", "homelab", m); ch.Changed {
		t.Errorf("unchanged set reported %+v", ch)
	}
	m2, _ := ParseMembers([]string{"10.0.0.6", "10.0.0.7"})
	if ch, _ := s.Set("containers", "homelab", m2); !ch.Changed || ch.Added != 1 || ch.Removed != 1 {
		t.Errorf("replace: %+v", ch)
	}
	if !s.Contains("containers", netip.MustParseAddr("10.0.0.7")) || s.Contains("containers", netip.MustParseAddr("10.0.0.5")) ||
		!s.Contains("containers", netip.MustParseAddr("::ffff:10.0.0.6")) {
		t.Error("Contains")
	}

	// it survives a restart
	s2 := &Store{Path: path}
	if err := s2.Load(); err != nil {
		t.Fatal(err)
	}
	if o, ok := s2.Get("containers"); !ok || len(o.Members) != 2 || o.Plugin != "homelab" || !o.Updated.Equal(now) {
		t.Fatalf("reloaded %+v %v", o, ok)
	}

	// List shows the config's objects, filled or not
	owned := map[string]string{"containers": "homelab", "vms": "homelab"}
	list := s2.List(owned)
	if len(list) != 2 || list[0].Name != "containers" || len(list[0].Members) != 2 || list[1].Name != "vms" || len(list[1].Members) != 0 {
		t.Errorf("list %+v", list)
	}
	// an object given to another plugin is listed empty until that one fills it
	if l := s2.List(map[string]string{"containers": "other"}); len(l[0].Members) != 0 {
		t.Errorf("reassigned object kept its members: %+v", l)
	}

	// Prune drops removed and reassigned objects
	s2.Set("old", "homelab", m)
	if d := s2.Prune(map[string]string{"containers": "other"}); strings.Join(d, ",") != "containers,old" {
		t.Errorf("pruned %v", d)
	}
	if s2.Members("containers") != nil {
		t.Error("pruned object still has members")
	}
	s3 := &Store{Path: path}
	s3.Load()
	if _, ok := s3.Get("old"); ok {
		t.Error("prune was not saved")
	}
}

func TestOwned(t *testing.T) {
	cfg := &config.Config{Addresses: []config.Address{
		{Name: "containers", Type: "live", Value: "homelab"},
		{Name: "office", Type: "host", Value: "10.0.0.1"},
	}}
	if o := Owned(cfg); len(o) != 1 || o["containers"] != "homelab" {
		t.Errorf("owned %v", o)
	}
}
