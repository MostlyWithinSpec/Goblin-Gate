package nft

import (
	"net/netip"
	"testing"
)

func TestMergeSpans(t *testing.T) {
	tests := []struct {
		name string
		in   []span
		want []span
	}{
		{"empty", nil, nil},
		{"disjoint sorted", []span{{5, 6}, {1, 2}}, []span{{1, 2}, {5, 6}}},
		{"overlap", []span{{1, 10}, {5, 20}}, []span{{1, 20}}},
		{"adjacent", []span{{1, 4}, {5, 9}}, []span{{1, 9}}},
		{"contained", []span{{1, 100}, {10, 20}}, []span{{1, 100}}},
		{"max value", []span{{^uint32(0) - 1, ^uint32(0)}, {0, ^uint32(0)}}, []span{{0, ^uint32(0)}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeSpans(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestFormatAddrSpan(t *testing.T) {
	sp := func(p string) span { return prefixSpan(netip.MustParsePrefix(p)) }
	tests := []struct {
		in   span
		want string
	}{
		{sp("192.168.1.0/24"), "192.168.1.0/24"},
		{sp("10.0.0.5/32"), "10.0.0.5"},
		{sp("0.0.0.0/0"), "0.0.0.0/0"},
		{sp("192.168.1.77/24"), "192.168.1.0/24"},
		{span{ip4ToUint(netip.MustParseAddr("10.0.0.1")), ip4ToUint(netip.MustParseAddr("10.0.0.4"))}, "10.0.0.1-10.0.0.4"},
		{span{ip4ToUint(netip.MustParseAddr("10.0.0.2")), ip4ToUint(netip.MustParseAddr("10.0.0.3"))}, "10.0.0.2/31"},
	}
	for _, tt := range tests {
		if got := formatAddrSpan(tt.in); got != tt.want {
			t.Errorf("formatAddrSpan(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestAddrValueSpan(t *testing.T) {
	tests := []struct {
		typ, value string
		wantErr    bool
	}{
		{"subnet", "10.0.0.0/8", false},
		{"host", "10.1.2.3", false},
		{"range", "10.0.0.1-10.0.0.9", false},
		{"range", "10.0.0.9-10.0.0.1", true},
		{"range", "10.0.0.9", true},
		{"host", "2001:db8::1", true},
		{"subnet", "2001:db8::/32", true},
		{"host", "nope", true},
		{"fqdn", "example.com", true},
	}
	for _, tt := range tests {
		_, err := addrValueSpan(tt.typ, tt.value)
		if (err != nil) != tt.wantErr {
			t.Errorf("addrValueSpan(%q, %q) err = %v, wantErr %v", tt.typ, tt.value, err, tt.wantErr)
		}
	}
}

func TestParsePorts(t *testing.T) {
	got, err := parsePorts("443, 80,8000-8100,81")
	if err != nil {
		t.Fatal(err)
	}
	want := []span{{80, 81}, {443, 443}, {8000, 8100}}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	for _, bad := range []string{"0", "65536", "90-80", "http", "1-"} {
		if _, err := parsePorts(bad); err == nil {
			t.Errorf("parsePorts(%q): want error", bad)
		}
	}
	if s, err := parsePorts(""); err != nil || s != nil {
		t.Errorf("parsePorts(\"\") = %v, %v; want nil, nil", s, err)
	}
}
