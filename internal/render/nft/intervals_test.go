package nft

import (
	"net/netip"
	"strings"
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

func TestAddrValue(t *testing.T) {
	tests := []struct {
		typ, value string
		want       string // formatted span; "" = error
	}{
		{"subnet", "10.0.0.0/8", "10.0.0.0/8"},
		{"host", "10.1.2.3", "10.1.2.3"},
		{"range", "10.0.0.1-10.0.0.9", "10.0.0.1-10.0.0.9"},
		{"range", "10.0.0.9-10.0.0.1", ""},
		{"range", "10.0.0.9", ""},
		{"host", "2001:db8::1", "2001:db8::1"},
		{"subnet", "2001:db8::/32", "2001:db8::/32"},
		{"subnet", "2001:db8::5/64", "2001:db8::/64"},
		{"range", "2001:db8::1-2001:db8::9", "2001:db8::1-2001:db8::9"},
		{"range", "2001:db8::-2001:db8::ff", "2001:db8::/120"},
		{"range", "10.0.0.1-2001:db8::9", ""},
		{"subnet", "::ffff:10.0.0.0/104", ""},
		{"host", "nope", ""},
		{"fqdn", "example.com", ""},
	}
	for _, tt := range tests {
		v4, v6, err := addrValue(tt.typ, tt.value)
		got := ""
		switch {
		case err != nil:
		case len(v4) == 1 && len(v6) == 0:
			got = formatAddrSpan(v4[0])
		case len(v6) == 1 && len(v4) == 0:
			got = formatAddrSpan6(v6[0])
		default:
			got = "?"
		}
		if got != tt.want {
			t.Errorf("addrValue(%q, %q) = %q (err %v), want %q", tt.typ, tt.value, got, err, tt.want)
		}
	}
}

func TestMergeSpans6(t *testing.T) {
	a := func(s string) netip.Addr { return netip.MustParseAddr(s) }
	got := mergeSpans6([]span6{
		{a("2001:db8::10"), a("2001:db8::20")},
		prefixSpan6(netip.MustParsePrefix("2001:db8::/124")), // ::0-::f, adjacent to ::10
		{a("2001:db8::100"), a("2001:db8::100")},
		{a("ffff:ffff:ffff:ffff:ffff:ffff:ffff:fff0"), a("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff")},
		{a("ffff:ffff:ffff:ffff:ffff:ffff:ffff:fff8"), a("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff")},
	})
	var out []string
	for _, s := range got {
		out = append(out, formatAddrSpan6(s))
	}
	want := "2001:db8::-2001:db8::20 2001:db8::100 ffff:ffff:ffff:ffff:ffff:ffff:ffff:fff0/124"
	if strings.Join(out, " ") != want {
		t.Errorf("got %v, want %s", out, want)
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
