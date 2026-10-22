package api

import (
	"bufio"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/auth"
	"github.com/networkgoblinlabs/goblin-gate/internal/checks"
	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/notify"
)

// TestRouteScopes walks the route table: every route that is not a GET
// needs an entry in routeScopes, and every entry names a real route. A new
// write endpoint without a decided scope fails here.
func TestRouteScopes(t *testing.T) {
	s := &Server{}
	registered := map[string]bool{}
	s.register(func(pattern string, _ http.HandlerFunc) { registered[pattern] = true })
	for p := range registered {
		if method, _, hasMethod := strings.Cut(p, " "); hasMethod && method == http.MethodGet {
			continue
		}
		if _, ok := routeScopes[p]; !ok {
			t.Errorf("route %q has no entry in routeScopes", p)
		}
	}
	for p := range routeScopes {
		if !registered[p] {
			t.Errorf("routeScopes names %q, which is not registered", p)
		}
	}
}

// TestSectionScopes: every top-level config section has a PATCH scope.
func TestSectionScopes(t *testing.T) {
	ty := reflect.TypeOf(config.Config{})
	for i := 0; i < ty.NumField(); i++ {
		name, _, _ := strings.Cut(ty.Field(i).Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		if _, ok := sectionScopes[name]; !ok {
			t.Errorf("config section %q has no entry in sectionScopes", name)
		}
	}
	for p, want := range map[string]string{
		"policies":                   auth.ScopeWriteFirewall,
		"policies[id=3].enabled":     auth.ScopeWriteFirewall,
		"nat.port_forwards":          auth.ScopeWriteFirewall,
		"interfaces[name=wan1].ipv6": auth.ScopeWriteNetwork,
		"system.hostname":            auth.ScopeWriteSystem,
		"system":                     scopeAdmin,
		"system.ssh_keys":            scopeAdmin,
		"system.update_url":          scopeAdmin,
		"admins[username=x].role":    scopeAdmin,
		"git_backup.remote":          scopeAdmin,
		"plugins":                    scopeAdmin,
		"no_such_section":            scopeAdmin,
	} {
		if got := configScope(p); got != want {
			t.Errorf("configScope(%q) = %q, want %q", p, got, want)
		}
	}
}

func TestScopedTokens(t *testing.T) {
	a := newAuthAPI(t)
	admin := a.browser()
	admin.login("admin")
	cfg, _ := a.store.Running()
	cfg.DDNS = []config.DDNS{{Name: "home", Provider: "duckdns", Hostname: "x.duckdns.org", Token: "duck-secret", Enabled: true}}
	if _, err := a.store.SaveRevision(cfg, "test", "ddns"); err != nil {
		t.Fatal(err)
	}
	token := func(body map[string]any) *browser {
		t.Helper()
		out := admin.expect("POST", "/api/v1/auth/tokens", body, 201)
		c := a.browser()
		c.header["Authorization"] = "Bearer " + out["token"].(string)
		return c
	}
	admin.expect("POST", "/api/v1/auth/tokens", map[string]any{"name": "x", "scopes": []string{}}, 400)
	admin.expect("POST", "/api/v1/auth/tokens", map[string]any{"name": "x", "scopes": []string{"root"}}, 400)
	admin.expect("POST", "/api/v1/auth/tokens", map[string]any{"name": "x", "readonly": true, "scopes": []string{"commit"}}, 400)
	admin.expect("POST", "/api/v1/auth/tokens", map[string]any{"name": "x", "expires": time.Now().Add(-time.Hour)}, 400)

	// home automation: toggle policies and commit, nothing else
	ha := token(map[string]any{"name": "ha", "scopes": []string{"read", "write:firewall", "commit"}})
	ha.expect("GET", "/api/v1/config/running", nil, 200)
	ha.expect("PATCH", "/api/v1/config/candidate", map[string]any{"path": "schedules", "value": []any{}}, 200)
	ha.expect("PATCH", "/api/v1/config/candidate", map[string]any{"path": "interfaces[name=lan].mtu", "value": 1400}, 403)
	ha.expect("PATCH", "/api/v1/config/candidate", map[string]any{"path": "system.ssh_keys", "value": []any{}}, 403)
	ha.expect("PUT", "/api/v1/config/candidate", map[string]any{}, 403)
	ha.expect("POST", "/api/v1/auth/tokens", map[string]any{"name": "escalate"}, 403)
	ha.expect("POST", "/api/v1/plugins/sentry/enable", nil, 403)
	ha.expect("POST", "/api/v1/diag/ping", map[string]any{"host": "192.0.2.1"}, 403) // no actions scope
	ha.expect("POST", "/api/v1/config/validate", nil, 200)                           // read

	// a scoped token never sees secrets, and writing back what it read keeps them
	net := token(map[string]any{"name": "net", "scopes": []string{"read", "write:network"}})
	got := net.expect("GET", "/api/v1/config/candidate", nil, 200)
	if strings.Contains(jsonString(got), "duck-secret") {
		t.Fatal("scoped token sees the DDNS token")
	}
	ddns := got["ddns"]
	if ddns == nil {
		t.Fatalf("candidate shape: %v", got)
	}
	net.expect("PATCH", "/api/v1/config/candidate", map[string]any{"path": "ddns", "value": ddns}, 200)
	cand, _ := a.store.Candidate()
	if len(cand.DDNS) != 1 || cand.DDNS[0].Token != "duck-secret" {
		t.Fatalf("secret lost on write-back: %+v", cand.DDNS)
	}

	// a token from the wrong network is refused (the test client is 127.0.0.1)
	far := token(map[string]any{"name": "far", "allowed_from": []string{"10.0.0.0/8"}})
	far.expect("GET", "/api/v1/config/running", nil, 403)
	near := token(map[string]any{"name": "near", "allowed_from": []string{"127.0.0.0/8"}})
	near.expect("GET", "/api/v1/config/running", nil, 200)

	// templates: anyone who reads may preview; applying is for full admins
	tpl := map[string]any{"values": map[string]string{"devices": "192.168.1.50"}}
	ha.expect("POST", "/api/v1/templates/bedtime/preview", tpl, 200)
	ha.expect("POST", "/api/v1/templates/bedtime/apply", tpl, 403)

	// Goblin Checks: read lists, write:system dismisses
	a.server.Dismissed = &checks.Dismissals{Path: filepath.Join(t.TempDir(), "dismissed.json")}
	ha.expect("GET", "/api/v1/checks", nil, 200)
	ha.expect("POST", "/api/v1/checks/dismiss", map[string]any{"key": "upnp:on"}, 403)
	sys := token(map[string]any{"name": "sys", "scopes": []string{"read", "write:system"}})
	sys.expect("POST", "/api/v1/checks/dismiss", map[string]any{"key": "upnp:on"}, 200)
	sys.expect("DELETE", "/api/v1/checks/dismiss/upnp:on", nil, 200)

	// a token without read cannot even look
	commitOnly := token(map[string]any{"name": "c", "scopes": []string{"commit"}})
	commitOnly.expect("GET", "/api/v1/config/running", nil, 403)
}

func TestTokenExpiry(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	tk := &auth.Tokens{Path: t.TempDir() + "/tokens.json", Now: func() time.Time { return now }}
	tok, info, err := tk.CreateWith("x", "admin", "admin", auth.TokenOptions{Expires: now.Add(time.Hour), Scopes: []string{"read", "read"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Scopes) != 1 {
		t.Fatalf("scopes not deduplicated: %v", info.Scopes)
	}
	if _, ok := tk.Verify(tok); !ok {
		t.Fatal("fresh token refused")
	}
	now = now.Add(2 * time.Hour)
	if _, ok := tk.Verify(tok); ok {
		t.Fatal("expired token accepted")
	}
}

func TestEventStream(t *testing.T) {
	d := &notify.Dispatcher{}
	a := newTestAPIWith(t, func(s *Server) { s.Notify = d })
	req, _ := http.NewRequest("GET", a.srv.URL+"/api/v1/events?types=commit,wan_down", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("content type %q", resp.Header.Get("Content-Type"))
	}
	rd := bufio.NewReader(resp.Body)
	if l, _ := rd.ReadString('\n'); l != ": connected\n" {
		t.Fatalf("first line %q", l)
	}
	d.Publish(notify.Event{Kind: "plugin", Title: "filtered out"})
	d.Publish(notify.Event{Kind: "wan_down", Key: "wan1", Title: "Gateway wan1 is down"})
	var lines []string
	for len(lines) < 3 {
		l, err := rd.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
		if len(lines) == 2 {
			break
		}
	}
	if lines[0] != "event: wan_down" || !strings.Contains(lines[1], `"title":"Gateway wan1 is down"`) {
		t.Fatalf("stream: %q", lines)
	}
}

// A stream outlives nothing: revoking its token ends it at the next heartbeat.
func TestEventStreamEndsWhenTokenRevoked(t *testing.T) {
	old := eventHeartbeat
	eventHeartbeat = 50 * time.Millisecond
	t.Cleanup(func() { eventHeartbeat = old })
	a := newAuthAPI(t)
	a.server.Notify = &notify.Dispatcher{}
	admin := a.browser()
	admin.login("admin")
	out := admin.expect("POST", "/api/v1/auth/tokens", map[string]any{"name": "ha", "scopes": []string{"read"}}, 201)
	token := out["token"].(string)
	id := out["info"].(map[string]any)["id"].(string)

	req, _ := http.NewRequest("GET", a.srv.URL+"/api/v1/events", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("stream: %v %v", resp, err)
	}
	defer resp.Body.Close()
	rd := bufio.NewReader(resp.Body)
	rd.ReadString('\n') // ": connected"

	admin.expect("DELETE", "/api/v1/auth/tokens/"+id, nil, 200)
	done := make(chan error, 1)
	go func() {
		for {
			if _, err := rd.ReadString('\n'); err != nil {
				done <- err
				return
			}
		}
	}()
	select {
	case <-done: // the server closed the stream
	case <-time.After(3 * time.Second):
		t.Fatal("the stream outlived its revoked token")
	}
}

// A kind that could break an SSE frame is never written.
func TestEventStreamSkipsBadKinds(t *testing.T) {
	d := &notify.Dispatcher{}
	a := newTestAPIWith(t, func(s *Server) { s.Notify = d })
	req, _ := http.NewRequest("GET", a.srv.URL+"/api/v1/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	rd := bufio.NewReader(resp.Body)
	rd.ReadString('\n') // ": connected"
	d.Publish(notify.Event{Kind: "test\ndata: forged", Title: "bad"})
	d.Publish(notify.Event{Kind: "commit", Title: "good"})
	for {
		l, err := rd.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(l, "forged") {
			t.Fatalf("bad kind written: %q", l)
		}
		if strings.HasPrefix(l, "event: commit") {
			return
		}
	}
}
