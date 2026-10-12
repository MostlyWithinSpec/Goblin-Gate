package api

import (
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/tailscale"
)

func fakeTailscaled(t *testing.T) *tailscale.Client {
	sock := filepath.Join(t.TempDir(), "ts.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skip("unix sockets unavailable:", err)
	}
	authURL := ""
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /localapi/v0/status":
			json.NewEncoder(w).Encode(map[string]any{"BackendState": "NeedsLogin", "AuthURL": authURL, "TailscaleIPs": []string{},
				"Peer": map[string]any{"k": map[string]any{"HostName": "phone", "DNSName": "phone.tail.ts.net.", "TailscaleIPs": []string{"100.64.0.9"}, "Online": true}}})
		case "GET /localapi/v0/prefs":
			json.NewEncoder(w).Encode(map[string]any{"AdvertiseRoutes": []string{"192.168.1.0/24"}})
		case "POST /localapi/v0/login-interactive":
			authURL = "https://login.tailscale.com/a/abc"
		case "POST /localapi/v0/start", "POST /localapi/v0/logout":
		default:
			http.NotFound(w, r)
		}
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return &tailscale.Client{Socket: sock}
}

func TestTailscaleEndpoints(t *testing.T) {
	a := newTestAPI(t)
	if out := a.expect("GET", "/api/v1/vpn/tailscale/status", nil, 200); out["available"] != false {
		t.Fatalf("no daemon: %v", out)
	}
	a.expect("POST", "/api/v1/vpn/tailscale/login", nil, 503)

	c := fakeTailscaled(t)
	a = newTestAPIWith(t, func(s *Server) { s.Tailscale = c })
	out := a.expect("GET", "/api/v1/vpn/tailscale/status", nil, 200)
	peers, _ := out["peers"].([]any)
	if out["available"] != true || out["backend_state"] != "NeedsLogin" || len(peers) != 1 || peers[0].(map[string]any)["dns_name"] != "phone.tail.ts.net" {
		t.Fatalf("status: %v", out)
	}
	if out := a.expect("POST", "/api/v1/vpn/tailscale/login", nil, 200); out["auth_url"] != "https://login.tailscale.com/a/abc" {
		t.Fatalf("login: %v", out)
	}
	a.expect("POST", "/api/v1/vpn/tailscale/login", map[string]string{"auth_key": "nope"}, 400)
	a.expect("POST", "/api/v1/vpn/tailscale/login", map[string]string{"auth_key": "tskey-auth-abc"}, 200)
	a.expect("POST", "/api/v1/vpn/tailscale/logout", nil, 200)

	// settings are edited like any config section
	out = a.expect("PATCH", "/api/v1/config/candidate", map[string]any{"path": "vpn.tailscale", "value": map[string]any{"enabled": true, "advertise_routes": []string{"lan"}}}, 200)
	if errs, _ := out["validation_errors"].([]any); len(errs) != 0 {
		t.Fatalf("patch: %v", errs)
	}
	cand, _ := a.store.Candidate()
	if !cand.VPN.Tailscale.Enabled || cand.VPN.Tailscale.AdvertiseRoutes[0] != "lan" {
		t.Fatalf("candidate %+v", cand.VPN)
	}
}
