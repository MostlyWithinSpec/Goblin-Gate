package api

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUPnPMappings(t *testing.T) {
	leases := filepath.Join(t.TempDir(), "leases")
	a := newTestAPIWith(t, func(s *Server) { s.UPnPLeaseFile = leases })
	out := a.expect("GET", "/api/v1/upnp/mappings", nil, 200)
	if out["enabled"] != false || len(out["mappings"].([]any)) != 0 {
		t.Fatalf("no leases yet: %v", out)
	}
	os.WriteFile(leases, []byte("UDP:3074:192.168.1.50:3074:1792036000:Xbox\n"), 0o644)
	out = a.expect("GET", "/api/v1/upnp/mappings", nil, 200)
	ms := out["mappings"].([]any)
	if len(ms) != 1 {
		t.Fatalf("mappings %v", out)
	}
	m := ms[0].(map[string]any)
	if m["protocol"] != "udp" || m["external_port"] != float64(3074) || m["internal_ip"] != "192.168.1.50" || m["expires"] != "2026-10-15T03:46:40Z" {
		t.Errorf("mapping %v", m)
	}
}
