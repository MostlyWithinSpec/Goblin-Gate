package api

import (
	"strings"
	"testing"
)

func TestDashboardsStore(t *testing.T) {
	a := newTestAPI(t)
	a.expect("GET", "/api/v1/ui/dashboards", nil, 503)

	dir := t.TempDir()
	a = newTestAPIWith(t, func(s *Server) { s.UI = &UIStore{Dir: dir} })
	out := a.expect("GET", "/api/v1/ui/dashboards", nil, 200)
	if d, _ := out["dashboards"].([]any); d == nil || len(d) != 0 {
		t.Fatalf("empty: %v", out)
	}
	doc := map[string]any{"version": 1, "dashboards": []any{map[string]any{"name": "Main", "widgets": []any{}}}}
	a.expect("PUT", "/api/v1/ui/dashboards", doc, 200)
	out = a.expect("GET", "/api/v1/ui/dashboards", nil, 200)
	if out["version"] != float64(1) || len(out["dashboards"].([]any)) != 1 {
		t.Fatalf("round trip: %v", out)
	}
	a.expect("PUT", "/api/v1/ui/dashboards", `[1,2]`, 400)
	a.expect("PUT", "/api/v1/ui/dashboards", `{"x":"`+strings.Repeat("a", MaxUIBlob)+`"}`, 413)
}

func TestPrefsStore(t *testing.T) {
	a := newTestAPIWith(t, func(s *Server) { s.UI = &UIStore{Dir: t.TempDir()} })
	if out := a.expect("GET", "/api/v1/ui/prefs", nil, 200); len(out) != 0 {
		t.Fatalf("empty prefs: %v", out)
	}
	a.expect("PUT", "/api/v1/ui/prefs", map[string]any{"language": "de"}, 200)
	if out := a.expect("GET", "/api/v1/ui/prefs", nil, 200); out["language"] != "de" {
		t.Fatalf("round trip: %v", out)
	}
	// a separate document: dashboards are untouched
	if out := a.expect("GET", "/api/v1/ui/dashboards", nil, 200); out["language"] != nil {
		t.Fatalf("prefs leaked into dashboards: %v", out)
	}
}
