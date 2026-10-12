package api

import (
	"bytes"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/webfilter"
)

func TestWebFilterEndpoints(t *testing.T) {
	a := newTestAPI(t)
	a.expect("GET", "/api/v1/webfilter/categories", nil, 503)

	f := &webfilter.Filter{}
	b := webfilter.NewBuilder()
	b.Add("gambling", "casino.example")
	var buf bytes.Buffer
	b.WriteTo(&buf)
	db, _ := webfilter.Read(&buf)
	f.SetDB(db)
	a = newTestAPIWith(t, func(s *Server) { s.WebFilter = f })
	cfg, _ := a.store.Running()
	cfg.Profiles.WebFilter = []config.WebFilterProfile{{Name: "kids", Categories: map[string]string{"gambling": "block"}}}
	a.store.SaveRevision(cfg, "test", "wf")

	out := a.expect("GET", "/api/v1/webfilter/categories", nil, 200)
	if cats := out["categories"].([]any); len(cats) != 1 || out["entries"] != float64(1) {
		t.Fatalf("categories: %v", out)
	}
	out = a.expect("GET", "/api/v1/webfilter/lookup?q=WWW.casino.example", nil, 200)
	kids := out["profiles"].(map[string]any)["kids"].(map[string]any)
	if out["domain"] != "www.casino.example" || len(out["categories"].([]any)) != 1 || kids["action"] != "block" || kids["match"] != "casino.example" {
		t.Fatalf("lookup: %v", out)
	}
	a.expect("GET", "/api/v1/webfilter/lookup?q=", nil, 400)
}
