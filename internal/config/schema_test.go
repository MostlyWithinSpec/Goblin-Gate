package config

import (
	"bytes"
	"net/netip"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// cmpOpts handles netip types, which carry unexported fields that go-cmp
// cannot walk; both are comparable so == is sufficient.
var cmpOpts = cmp.Options{cmpopts.EquateComparable(netip.Addr{}, netip.Prefix{})}

func TestDefaultRoundTrip(t *testing.T) {
	cfg := Default()
	data, err := cfg.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got, err := Load(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if diff := cmp.Diff(cfg, got, cmpOpts); diff != "" {
		t.Errorf("round trip mismatch (-want +got):\n%s", diff)
	}
}

func TestMarshalStable(t *testing.T) {
	cfg := Default()
	a, err := cfg.Marshal()
	if err != nil {
		t.Fatalf("Marshal 1: %v", err)
	}
	b, err := cfg.Marshal()
	if err != nil {
		t.Fatalf("Marshal 2: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Error("Marshal output is not stable across calls")
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	_, err := Load(strings.NewReader(`{"schema_version":1,"bogus":true}`))
	if err == nil {
		t.Fatal("Load accepted an unknown field, want error")
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("error %q does not mention the unknown field", err)
	}
}

func TestLoadRejectsInvalidJSON(t *testing.T) {
	if _, err := Load(strings.NewReader(`{nope`)); err == nil {
		t.Fatal("Load accepted invalid JSON, want error")
	}
}
