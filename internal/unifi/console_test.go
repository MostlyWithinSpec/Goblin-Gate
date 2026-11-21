package unifi

import (
	"os"
	"strings"
	"testing"
)

// TestConsole reads a real UniFi console: GG_UNIFI_URL, GG_UNIFI_KEY_FILE (a
// file holding only the API key) and GG_UNIFI_FINGERPRINT. It prints counts
// only, never the clients themselves. Skipped without GG_UNIFI_URL.
func TestConsole(t *testing.T) {
	u := os.Getenv("GG_UNIFI_URL")
	if u == "" {
		t.Skip("GG_UNIFI_URL not set")
	}
	key, err := os.ReadFile(os.Getenv("GG_UNIFI_KEY_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := SettingsFrom(map[string]any{"url": u, "api_key": strings.TrimSpace(string(key)), "fingerprint": os.Getenv("GG_UNIFI_FINGERPRINT")})
	if err != nil {
		t.Fatal(err)
	}
	cl := &Client{Settings: s}
	st, err := cl.Stations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	devs, err := cl.Devices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ann := Annotations(st, devs)
	wired, ports, aps, ssids := 0, 0, 0, 0
	for _, f := range ann {
		if f["connection"] == "wired" {
			wired++
		}
		if f["port"] != "" {
			ports++
		}
		if f["ap"] != "" {
			aps++
		}
		if f["ssid"] != "" {
			ssids++
		}
		if len(f) > 8 {
			t.Errorf("%d fields", len(f))
		}
	}
	t.Logf("prefix %q: %d clients, %d devices, %d annotated (%d wired, %d with a port, %d with an AP, %d with an SSID)",
		cl.prefix, len(st), len(devs), len(ann), wired, ports, aps, ssids)
	if len(st) == 0 || len(ann) != len(st) {
		t.Errorf("clients %d, annotated %d", len(st), len(ann))
	}
}
