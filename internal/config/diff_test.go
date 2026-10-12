package config

import (
	"net/netip"
	"reflect"
	"testing"
)

func TestDiffIdentical(t *testing.T) {
	if d := Diff(Default(), Default()); len(d) != 0 {
		t.Errorf("Diff(Default, Default) = %v, want none", d)
	}
}

func TestDiffChanges(t *testing.T) {
	a := Default()
	a.Addresses = []Address{
		{Name: "nas", Type: "ipmask", Value: "192.168.1.10/32"},
		{Name: "printer", Type: "ipmask", Value: "192.168.1.20/32"},
	}
	b := Default()
	b.System.Hostname = "renamed"
	b.Policies[0].Action = "deny"
	b.Addresses = []Address{
		{Name: "printer", Type: "ipmask", Value: "192.168.1.21/32"},
		{Name: "camera", Type: "ipmask", Value: "192.168.1.30/32"},
	}

	got := Diff(a, b)
	want := []Change{
		{Path: "addresses[name=nas]", Op: OpRemove},
		{Path: "addresses[name=printer].value", Op: OpChange, Old: "192.168.1.20/32", New: "192.168.1.21/32"},
		{Path: "addresses[name=camera]", Op: OpAdd},
		{Path: "policies[id=1].action", Op: OpChange, Old: "accept", New: "deny"},
		{Path: "system.hostname", Op: OpChange, Old: "goblin-gate", New: "renamed"},
	}
	if len(got) != len(want) {
		t.Fatalf("Diff() returned %d changes, want %d:\n%+v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if g.Path != w.Path || g.Op != w.Op {
			t.Errorf("change %d = %s %s, want %s %s", i, g.Op, g.Path, w.Op, w.Path)
			continue
		}
		if w.Op == OpChange && (!reflect.DeepEqual(g.Old, w.Old) || !reflect.DeepEqual(g.New, w.New)) {
			t.Errorf("change %d %s: %v -> %v, want %v -> %v", i, g.Path, g.Old, g.New, w.Old, w.New)
		}
	}
	if m, ok := got[0].Old.(map[string]any); !ok || m["name"] != "nas" {
		t.Errorf("removed item Old = %v, want the nas object", got[0].Old)
	}
	if m, ok := got[2].New.(map[string]any); !ok || m["name"] != "camera" {
		t.Errorf("added item New = %v, want the camera object", got[2].New)
	}
}

func TestDiffPolicyRenameKeepsID(t *testing.T) {
	a, b := Default(), Default()
	b.Policies[0].Name = "lan-out"
	got := Diff(a, b)
	if len(got) != 1 || got[0].Path != "policies[id=1].name" || got[0].Op != OpChange {
		t.Errorf("Diff() = %+v, want a single name change on policies[id=1]", got)
	}
}

func TestDiffReorderIsNotAChange(t *testing.T) {
	a := Default()
	a.Addresses = []Address{
		{Name: "x", Type: "ipmask", Value: "10.0.0.1/32"},
		{Name: "y", Type: "ipmask", Value: "10.0.0.2/32"},
	}
	b := Default()
	b.Addresses = []Address{a.Addresses[1], a.Addresses[0]}
	if d := Diff(a, b); len(d) != 0 {
		t.Errorf("reordered keyed list produced changes: %+v", d)
	}
}

func TestDiffStable(t *testing.T) {
	a := Default()
	b := Default()
	b.System.Hostname = "h"
	b.Interfaces[1].MTU = 9000
	gw := netip.MustParseAddr("192.168.1.254")
	b.Interfaces[1].Gateway = &gw
	first := Diff(a, b)
	for i := 0; i < 20; i++ {
		if got := Diff(a, b); !reflect.DeepEqual(got, first) {
			t.Fatalf("Diff() not stable:\n%+v\nvs\n%+v", got, first)
		}
	}
}

func TestDiffUnkeyedListByIndex(t *testing.T) {
	a, b := Default(), Default()
	a.Policies[0].SrcAddr = []string{"all"}
	b.Policies[0].SrcAddr = []string{"all", "nas"}
	got := Diff(a, b)
	if len(got) != 1 || got[0].Path != "policies[id=1].src_addr[1]" || got[0].Op != OpAdd || got[0].New != "nas" {
		t.Errorf("Diff() = %+v, want add at policies[id=1].src_addr[1]", got)
	}
}
