package api

import (
	"io"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/portal"
)

func TestPortalAPI(t *testing.T) {
	m := &portal.Manager{Path: filepath.Join(t.TempDir(), "state.json")}
	a := newTestAPIWith(t, func(s *Server) { s.Portal = m })
	cfg, err := a.store.Candidate()
	if err != nil {
		t.Fatal(err)
	}
	cfg.CaptivePortals = []config.CaptivePortal{{Interface: "lan", Enabled: true, Mode: "voucher", Title: "Hello guests"}}
	if err := a.store.SetCandidate(cfg); err != nil {
		t.Fatal(err)
	}

	a.expect("POST", "/api/v1/portal/vouchers", map[string]any{"count": 0, "minutes": 60}, 400)
	a.expect("POST", "/api/v1/portal/vouchers", map[string]any{"count": 2, "minutes": 60, "interface": "nope"}, 400)
	b := a.expect("POST", "/api/v1/portal/vouchers", map[string]any{"count": 2, "minutes": 60, "uses": 1, "note": "party", "interface": "lan"}, 201)
	codes := b["codes"].([]any)
	if len(codes) != 2 || b["counts"].(map[string]any)["unused"].(float64) != 2 || b["note"] != "party" {
		t.Fatalf("batch %v", b)
	}
	id := b["id"].(string)
	code := codes[0].(map[string]any)["code"].(string)

	if _, err := m.Login("lan", cfg.CaptivePortals[0], netip.MustParseAddr("192.168.1.50"), net.HardwareAddr{2, 0, 0, 0, 0, 1}, false, "", code); err != nil {
		t.Fatal(err)
	}
	cl := a.expect("GET", "/api/v1/portal/clients", nil, 200)["clients"].([]any)
	if len(cl) != 1 || cl[0].(map[string]any)["voucher"] != code || cl[0].(map[string]any)["seconds_left"].(float64) < 3500 {
		t.Fatalf("clients %v", cl)
	}
	list := a.expect("GET", "/api/v1/portal/vouchers", nil, 200)["batches"].([]any)
	first := list[0].(map[string]any)["codes"].([]any)[0].(map[string]any)
	if first["state"] != "used_up" || first["used"].(float64) != 1 {
		t.Errorf("code state %v", first)
	}

	a.expect("DELETE", "/api/v1/portal/clients/02:00:00:00:00:01", nil, 200)
	a.expect("DELETE", "/api/v1/portal/clients/02:00:00:00:00:01", nil, 404)
	a.expect("POST", "/api/v1/portal/vouchers/"+id+"/revoke", map[string]any{"code": codes[1].(map[string]any)["code"]}, 200)
	a.expect("POST", "/api/v1/portal/vouchers/"+id+"/revoke", nil, 200)
	a.expect("POST", "/api/v1/portal/vouchers/nope/revoke", nil, 404)
	list = a.expect("GET", "/api/v1/portal/vouchers", nil, 200)["batches"].([]any)
	if list[0].(map[string]any)["revoked"] != true {
		t.Errorf("batch not revoked: %v", list[0])
	}
	a.expect("DELETE", "/api/v1/portal/vouchers/"+id, nil, 200)
	a.expect("DELETE", "/api/v1/portal/vouchers/"+id, nil, 404)

	// the preview is the candidate's page
	resp, err := http.Get(a.srv.URL + "/api/v1/portal/preview/lan")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "Hello guests") || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Errorf("preview: %d %s", resp.StatusCode, body)
	}
	a.expect("GET", "/api/v1/portal/preview/wan1", nil, 404)
}

func TestPortalUnavailable(t *testing.T) {
	a := newTestAPIWith(t, func(s *Server) {})
	a.expect("GET", "/api/v1/portal/clients", nil, 503)
}
