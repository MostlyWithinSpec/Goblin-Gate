package tailscale

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// FakeDaemon is a minimal tailscaled LocalAPI on a unix socket.
type fakeDaemon struct {
	mu      sync.Mutex
	prefs   map[string]any
	headers []string
	state   string
	authURL string
}

func startFake(t *testing.T) (*Client, *fakeDaemon) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "ts.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skip("unix sockets unavailable:", err)
	}
	f := &fakeDaemon{prefs: map[string]any{}, state: "NeedsLogin"}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method != http.MethodGet {
			f.headers = append(f.headers, r.Header.Get("Sec-Tailscale"))
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /localapi/v0/status":
			json.NewEncoder(w).Encode(map[string]any{"BackendState": f.state, "AuthURL": f.authURL,
				"Self": map[string]any{"HostName": "gate", "PrimaryRoutes": []string{"192.168.1.0/24"}},
				"Peer": map[string]any{"k1": map[string]any{"HostName": "laptop", "DNSName": "laptop.tail.ts.net.", "OS": "linux", "TailscaleIPs": []string{"100.64.0.2"}, "Online": true}}})
		case "GET /localapi/v0/prefs":
			json.NewEncoder(w).Encode(f.prefs)
		case "PATCH /localapi/v0/prefs":
			b, _ := io.ReadAll(r.Body)
			var m map[string]any
			json.Unmarshal(b, &m)
			for k, v := range m {
				f.prefs[k] = v
			}
			json.NewEncoder(w).Encode(f.prefs)
		case "POST /localapi/v0/login-interactive":
			f.authURL = "https://login.tailscale.com/a/xyz"
		case "POST /localapi/v0/start":
			f.state = "Starting"
		case "POST /localapi/v0/logout":
			f.state = "NeedsLogin"
		default:
			http.NotFound(w, r)
		}
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return &Client{Socket: sock}, f
}

func TestApply(t *testing.T) {
	c, f := startFake(t)
	ctx := context.Background()
	d := Desired{Enabled: true, Hostname: "gate", AdvertiseRoutes: []string{"192.168.1.0/24"}, ExitNode: true, AcceptRoutes: true}
	if err := Apply(ctx, c, d); err != nil {
		t.Fatal(err)
	}
	p, err := c.GetPrefs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !p.WantRunning || p.NetfilterMode != 0 || !p.NoSNAT || !p.RouteAll || p.CorpDNS || p.ControlURL != DefaultControlURL || len(p.AdvertiseRoutes) != 3 {
		t.Fatalf("prefs %+v", *p)
	}
	if f.prefs["NetfilterModeSet"] != true || f.headers[0] != "localapi" {
		t.Fatalf("masked prefs %v headers %v", f.prefs, f.headers)
	}
	if err := Apply(ctx, c, Desired{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if p, _ := c.GetPrefs(ctx); p.WantRunning {
		t.Fatal("still running after disable")
	}

	// no daemon: disabling is fine, enabling fails
	none := &Client{Socket: filepath.Join(t.TempDir(), "missing.sock")}
	if err := Apply(ctx, none, Desired{}); err != nil {
		t.Fatalf("disable without daemon: %v", err)
	}
	if err := Apply(ctx, none, d); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("enable without daemon: %v", err)
	}
}

func TestApplyFile(t *testing.T) {
	c, f := startFake(t)
	path := filepath.Join(t.TempDir(), "prefs.json")
	os.WriteFile(path, []byte(`{"enabled":true,"hostname":"x","login_server":"https://hs.example"}`), 0o644)
	if err := ApplyFile(context.Background(), c, path); err != nil {
		t.Fatal(err)
	}
	if f.prefs["ControlURL"] != "https://hs.example" || f.prefs["Hostname"] != "x" {
		t.Fatalf("prefs %v", f.prefs)
	}
	os.WriteFile(path, []byte(`{bad`), 0o644)
	if err := ApplyFile(context.Background(), c, path); err == nil {
		t.Fatal("bad json accepted")
	}
}
