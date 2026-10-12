package config

import (
	"strconv"
	"strings"
	"testing"
)

func TestParsePath(t *testing.T) {
	tests := []struct {
		in      string
		want    []PathSegment
		wantErr bool
	}{
		{in: "system.hostname", want: []PathSegment{{Kind: SegField, Key: "system"}, {Kind: SegField, Key: "hostname"}}},
		{in: "policies[id=10].action", want: []PathSegment{{Kind: SegField, Key: "policies"}, {Kind: SegMatch, Key: "id", Value: "10"}, {Kind: SegField, Key: "action"}}},
		{in: `addresses[name="web 1"]`, want: []PathSegment{{Kind: SegField, Key: "addresses"}, {Kind: SegMatch, Key: "name", Value: "web 1"}}},
		{in: "interfaces[0].mtu", want: []PathSegment{{Kind: SegField, Key: "interfaces"}, {Kind: SegIndex, Index: 0}, {Kind: SegField, Key: "mtu"}}},
		{in: "dns.local_records.[nas.home.lan]", want: []PathSegment{{Kind: SegField, Key: "dns"}, {Kind: SegField, Key: "local_records"}, {Kind: SegField, Key: "nas.home.lan"}}},
		{in: "", wantErr: true},
		{in: "invalid[", wantErr: true},
		{in: "invalid[=test]", wantErr: true},
		{in: "a..b", wantErr: true},
		{in: "a.", wantErr: true},
		{in: "a[0]b", wantErr: true},
		{in: "a b", wantErr: true},
	}
	for _, tt := range tests {
		got, err := ParsePath(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParsePath(%q): expected error, got %+v", tt.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParsePath(%q): %v", tt.in, err)
			continue
		}
		if len(got) != len(tt.want) {
			t.Errorf("ParsePath(%q) = %+v, want %+v", tt.in, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("ParsePath(%q)[%d] = %+v, want %+v", tt.in, i, got[i], tt.want[i])
			}
		}
	}
}

func TestSetPath(t *testing.T) {
	base := Default()
	pid := base.Policies[0].ID

	t.Run("scalar field", func(t *testing.T) {
		out, err := SetPath(base, "system.hostname", "goblin")
		if err != nil {
			t.Fatal(err)
		}
		if out.System.Hostname != "goblin" {
			t.Fatalf("hostname = %q", out.System.Hostname)
		}
		if base.System.Hostname == "goblin" {
			t.Fatal("SetPath modified its input")
		}
	})

	t.Run("policy by id", func(t *testing.T) {
		out, err := SetPath(base, "policies[id="+itoa(pid)+"].action", "deny")
		if err != nil {
			t.Fatal(err)
		}
		if out.Policies[0].Action != "deny" {
			t.Fatalf("action = %q", out.Policies[0].Action)
		}
	})

	t.Run("append by name match", func(t *testing.T) {
		out, err := SetPath(base, "addresses[name=web]", map[string]any{"type": "host", "value": "192.168.1.10"})
		if err != nil {
			t.Fatal(err)
		}
		last := out.Addresses[len(out.Addresses)-1]
		if last.Name != "web" || last.Value != "192.168.1.10" {
			t.Fatalf("appended %+v", last)
		}
	})

	t.Run("map key with dots", func(t *testing.T) {
		out, err := SetPath(base, "dns.local_records.[nas.home.lan]", "192.168.1.5")
		if err != nil {
			t.Fatal(err)
		}
		if out.DNS.LocalRecords["nas.home.lan"] != "192.168.1.5" {
			t.Fatalf("records = %v", out.DNS.LocalRecords)
		}
	})

	t.Run("index", func(t *testing.T) {
		out, err := SetPath(base, "interfaces[0].mtu", 1400)
		if err != nil {
			t.Fatal(err)
		}
		if out.Interfaces[0].MTU != 1400 {
			t.Fatalf("mtu = %d", out.Interfaces[0].MTU)
		}
	})

	for _, bad := range []struct {
		path  string
		value any
		why   string
	}{
		{"system.no_such_field", "x", "unknown field"},
		{"system.admin_port", "not a number", "wrong type"},
		{"policies[id=999999].action", "deny", "missing item"},
		{"interfaces[99].mtu", 1, "index out of range"},
		{"system.hostname.deeper", "x", "scalar is not an object"},
	} {
		if _, err := SetPath(base, bad.path, bad.value); err == nil {
			t.Errorf("SetPath(%q) %s: expected error", bad.path, bad.why)
		}
	}
}

func TestDeletePath(t *testing.T) {
	base := Default()
	pid := base.Policies[0].ID
	out, err := DeletePath(base, "policies[id="+itoa(pid)+"]")
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Policies) != len(base.Policies)-1 {
		t.Fatalf("policies = %d, want %d", len(out.Policies), len(base.Policies)-1)
	}
	if _, err := DeletePath(base, "policies[id=424242]"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found, got %v", err)
	}
	if _, err := DeletePath(base, "system.domain_nope"); err == nil {
		t.Fatal("expected error deleting a missing key")
	}
}

func itoa(n uint32) string { return strconv.FormatUint(uint64(n), 10) }
