package sentry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandler(t *testing.T) {
	e, g, _ := newEngine(t, ModeApprove)
	e.Ingest(context.Background(), []Alert{alert("198.51.100.7", "192.168.1.10", "ET EXPLOIT", 1, 0)})
	srv := httptest.NewServer(Handler(e))
	defer srv.Close()
	do := func(method, path, body string, want int) map[string]any {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("X-Goblin-User", "alice")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("%s %s: content type %q", method, path, resp.Header.Get("Content-Type"))
		}
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		if resp.StatusCode != want {
			t.Fatalf("%s %s: %d, want %d: %v", method, path, resp.StatusCode, want, out)
		}
		return out
	}
	st := do("GET", "/status", "", 200)
	if st["mode"] != "approve" || st["counts"].(map[string]any)["pending"] != 1.0 {
		t.Fatalf("status: %v", st)
	}
	list := do("GET", "/incidents?status=pending", "", 200)["incidents"].([]any)
	id := int64(list[0].(map[string]any)["id"].(float64))
	detail := do("GET", fmt.Sprintf("/incidents/%d", id), "", 200)
	if len(detail["decisions"].([]any)) != 1 {
		t.Fatalf("detail: %v", detail)
	}
	do("GET", "/incidents/999", "", 404)
	do("POST", fmt.Sprintf("/incidents/%d/answer", id), `{"answer":"nope"}`, 400)
	out := do("POST", fmt.Sprintf("/incidents/%d/answer", id), `{"answer":"block","minutes":15}`, 200)
	if out["incident"].(map[string]any)["answered_by"] != "alice" || g.blocks["198.51.100.7"] != 15 {
		t.Fatalf("answer: %v %v", out, g.blocks)
	}
	if n := len(do("GET", "/decisions?limit=1", "", 200)["decisions"].([]any)); n != 1 {
		t.Fatalf("decisions: %d", n)
	}
	rules := do("GET", "/rules", "", 200)["rules"].([]any)
	if len(rules) != len(DefaultRules()) {
		t.Fatalf("rules: %d", len(rules))
	}
	bad := do("PUT", "/rules", `{"rules":[{"id":1,"name":"x","action":{"tool":"shell"}}]}`, 400)
	if len(bad["problems"].([]any)) != 1 {
		t.Fatalf("problems: %v", bad)
	}
	do("PUT", "/rules", `{"rules":[{"id":1,"name":"x","enabled":true,"action":{"tool":"add_watch"},"bogus":1}]}`, 400)
	do("PUT", "/rules", `{"rules":[{"id":1,"name":"x","enabled":true,"action":{"tool":"add_watch"}}]}`, 200)
	do("GET", "/nope", "", 404)
}
