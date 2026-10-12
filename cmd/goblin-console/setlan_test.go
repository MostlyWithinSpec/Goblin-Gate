package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"
)

func TestSuggestRange(t *testing.T) {
	for in, want := range map[string]string{
		"10.10.10.1/24":   "10.10.10.100-10.10.10.199",
		"10.10.10.150/24": "10.10.10.1-10.10.10.127",
		"172.16.0.1/16":   "172.16.0.100-172.16.0.199",
		"192.168.5.1/26":  "192.168.5.32-192.168.5.62",
		"192.168.5.40/26": "192.168.5.1-192.168.5.31",
	} {
		lo, hi := suggestRange(netip.MustParsePrefix(in))
		if got := lo.String() + "-" + hi.String(); got != want {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
}

func TestParseLAN(t *testing.T) {
	if _, err := parseLAN("10.10.10.1/24"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"10.10.10.1", "10.10.10.0/24", "10.10.10.1/31", "fd00::1/64", "nope"} {
		if _, err := parseLAN(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestSetLAN(t *testing.T) {
	var patches []map[string]any
	committed := false
	c, out := testConsole(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1/config/candidate":
			w.Write([]byte(`{"dhcp":[
				{"interface":"lan","range_start":"192.168.1.100","range_end":"192.168.1.199","lease_seconds":3600,"static_leases":[{"mac":"aa:bb:cc:dd:ee:ff","ip":"192.168.1.5"}]},
				{"interface":"guest","range_start":"192.168.20.100","range_end":"192.168.20.199"}]}`))
		case r.Method == "PATCH":
			var p map[string]any
			b, _ := io.ReadAll(r.Body)
			json.Unmarshal(b, &p)
			patches = append(patches, p)
			w.Write([]byte(`{"status":"ok"}`))
		case r.URL.Path == "/api/v1/commit":
			committed = true
			w.Write([]byte(`{"revision":2}`))
		}
	}, "10.10.10.1/24\n\ny\n\n")
	cfg := runningConfig{Interfaces: []iface{{Name: "lan", Address: "192.168.1.1/24"}}}
	if err := c.setLAN(cfg, true); err != nil {
		t.Fatal(err)
	}
	if !committed || len(patches) != 3 {
		t.Fatalf("committed=%v patches=%v", committed, patches)
	}
	if patches[1]["path"] != "interfaces[name=lan].address" || patches[1]["value"] != "10.10.10.1/24" {
		t.Errorf("address patch: %v", patches[1])
	}
	got, _ := json.Marshal(patches[2]["value"])
	want := `[{"dns":["10.10.10.1"],"gateway":"10.10.10.1","interface":"lan","lease_seconds":3600,"range_end":"10.10.10.199","range_start":"10.10.10.100"},` +
		`{"interface":"guest","range_end":"192.168.20.199","range_start":"192.168.20.100"}]`
	if string(got) != want {
		t.Errorf("dhcp:\n got %s\nwant %s", got, want)
	}
	for _, s := range []string{"static DHCP lease", "https://10.10.10.1/", "setup wizard"} {
		if !strings.Contains(out.String(), s) {
			t.Errorf("missing %q in %s", s, out)
		}
	}
}

func TestSetLANCancel(t *testing.T) {
	c, _ := testConsole(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		w.Write([]byte(`{"dhcp":[]}`))
	}, "10.10.10.1/24\nnone\nn\n")
	if err := c.setLAN(runningConfig{}, false); err != nil {
		t.Fatal(err)
	}
}
