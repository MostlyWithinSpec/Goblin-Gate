package api

import (
	"path/filepath"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/checks"
)

func TestChecksAPI(t *testing.T) {
	a := newTestAPIWith(t, func(s *Server) {
		s.Dismissed = &checks.Dismissals{Path: filepath.Join(t.TempDir(), "dismissed.json")}
	})
	cfg, _ := a.store.Running()
	cfg.UPnP.Enabled = true
	if _, err := a.store.SaveRevision(cfg, "test", "upnp on"); err != nil {
		t.Fatal(err)
	}

	has := func(list any, key string) bool {
		for _, f := range list.([]any) {
			if f.(map[string]any)["key"] == key {
				return true
			}
		}
		return false
	}
	r := a.expect("GET", "/api/v1/checks", nil, 200)
	if !has(r["findings"], "upnp:on") || len(r["passed"].([]any)) == 0 {
		t.Fatalf("report %v", r)
	}
	score := r["score"].(float64)

	a.expect("POST", "/api/v1/checks/dismiss", map[string]any{"key": "upnp:on"}, 200)
	r = a.expect("GET", "/api/v1/checks", nil, 200)
	if has(r["findings"], "upnp:on") || !has(r["dismissed"], "upnp:on") || r["score"].(float64) <= score {
		t.Fatalf("after dismiss %v", r)
	}

	a.expect("DELETE", "/api/v1/checks/dismiss/upnp:on", nil, 200)
	r = a.expect("GET", "/api/v1/checks", nil, 200)
	if !has(r["findings"], "upnp:on") || r["score"].(float64) != score {
		t.Fatalf("after restore %v", r)
	}

	a.expect("POST", "/api/v1/checks/dismiss", map[string]any{"key": "../etc/passwd"}, 400)
	a.expect("POST", "/api/v1/checks/dismiss", map[string]any{"key": ""}, 400)
}

func TestChecksWithoutDismissals(t *testing.T) {
	a := newTestAPI(t)
	a.expect("GET", "/api/v1/checks", nil, 200)
	a.expect("POST", "/api/v1/checks/dismiss", map[string]any{"key": "upnp:on"}, 503)
}
