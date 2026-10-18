package api

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/devices"
)

// newDeviceStore returns a store with a fixed clock so tests are deterministic.
func newDeviceStore(t *testing.T) *devices.Store {
	t.Helper()
	s := devices.New(filepath.Join(t.TempDir(), "devices.json"))
	s.Now = func() time.Time { return time.Unix(1_760_000_000, 0) }
	return s
}

// seedLease writes a Kea memfile CSV with one active lease and points the
// store at it so Refresh() (public API only) can populate the inventory.
func seedLease(t *testing.T, store *devices.Store, ip, mac string) {
	t.Helper()
	f := filepath.Join(t.TempDir(), "leases.csv")
	// expire far in the future relative to the fixed clock (1_760_000_000)
	csv := "address,hwaddr,client_id,valid_lifetime,expire,subnet_id,fqdn_fwd,fqdn_rev,hostname,state,user_context\n" +
		ip + "," + mac + ",,3600,2000000000,1,0,0,,0,\n"
	if err := os.WriteFile(f, []byte(csv), 0o640); err != nil {
		t.Fatal(err)
	}
	store.LeaseFile = f
	if err := store.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestDevicesUnavailable(t *testing.T) {
	a := newTestAPI(t) // no Devices store wired
	out := a.expect("GET", "/api/v1/devices", nil, 503)
	if errCode(out) != "devices_unavailable" {
		t.Fatalf("error code = %q", errCode(out))
	}
	a.expect("PATCH", `/api/v1/devices/aabbccddee00`, map[string]string{"name": "x"}, 503)
	a.expect("DELETE", `/api/v1/devices/aabbccddee00`, nil, 503)
}

func TestDevicesListPatchDelete(t *testing.T) {
	store := newDeviceStore(t)
	if err := store.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	a := newTestAPIWith(t, func(s *Server) { s.Devices = store })

	out := a.expect("GET", "/api/v1/devices", nil, 200)
	list, ok := out["devices"].([]any)
	if !ok || len(list) != 0 {
		t.Fatalf("devices: %v", out["devices"])
	}

	// Rename an unknown MAC → 404.
	out = a.expect("PATCH", `/api/v1/devices/aabbccddee99`, map[string]string{"name": "x"}, 404)
	if errCode(out) != "not_found" {
		t.Fatalf("error code = %q", errCode(out))
	}

	// A name over 63 characters is rejected.
	long := make([]byte, 64)
	for i := range long {
		long[i] = 'a'
	}
	out = a.expect("PATCH", `/api/v1/devices/aabbccddee99`, map[string]string{"name": string(long)}, 400)
	if errCode(out) != "invalid_request" {
		t.Fatalf("error code = %q", errCode(out))
	}

	// DELETE of an unknown MAC → 404.
	a.expect("DELETE", `/api/v1/devices/aabbccddee99`, nil, 404)
}

func TestDevicesListFilter(t *testing.T) {
	store := newDeviceStore(t)
	// Seed two devices on different interfaces via the neighbour table.
	store.Runner = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return []byte(`[
			{"dst":"192.168.1.10","dev":"eth0","lladdr":"aa:bb:cc:dd:ee:01","state":"REACHABLE"},
			{"dst":"10.0.0.5","dev":"eth1","lladdr":"aa:bb:cc:dd:ee:02","state":"STALE"}
		]`), nil
	}
	store.Interfaces = func() map[string]string {
		return map[string]string{"eth0": "lan", "eth1": "wan"}
	}
	if err := store.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	a := newTestAPIWith(t, func(s *Server) { s.Devices = store })

	out := a.expect("GET", "/api/v1/devices?interface=lan", nil, 200)
	list := out["devices"].([]any)
	if len(list) != 1 {
		t.Fatalf("filtered: %v", list)
	}
	d := list[0].(map[string]any)
	if d["mac"] != "aabbccddee01" || d["interface"] != "lan" {
		t.Fatalf("device: %v", d)
	}

	// A filter that matches nothing returns an empty list, not null.
	out = a.expect("GET", "/api/v1/devices?interface=none", nil, 200)
	if len(out["devices"].([]any)) != 0 {
		t.Fatalf("empty filter: %v", out["devices"])
	}
}

// TestDevicesPatchDeleteRoundTrip exercises the happy path against a real
// store through the HTTP layer.
func TestDevicesPatchDeleteRoundTrip(t *testing.T) {
	store := newDeviceStore(t)
	seedLease(t, store, "192.168.1.50", "aabbccddee01")

	a := newTestAPIWith(t, func(s *Server) { s.Devices = store })

	out := a.expect("PATCH", `/api/v1/devices/aabbccddee01`, map[string]string{"name": "Office Printer"}, 200)
	if out["name"] != "Office Printer" || out["mac"] != "aabbccddee01" {
		t.Fatalf("patch: %v", out)
	}

	// DELETE returns 204 with no JSON body.
	req, _ := http.NewRequest("DELETE", a.srv.URL+"/api/v1/devices/aabbccddee01", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 204 || len(body) != 0 {
		t.Fatalf("delete: status %d body %q", resp.StatusCode, body)
	}

	out = a.expect("GET", "/api/v1/devices", nil, 200)
	if len(out["devices"].([]any)) != 0 {
		t.Fatalf("after delete: %v", out["devices"])
	}
}
