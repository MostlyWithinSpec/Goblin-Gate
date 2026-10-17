package api

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/plugins"
)

type fakeUnits struct {
	mu     sync.Mutex
	active map[string]bool
}

func (f *fakeUnits) Restart(_ context.Context, unit string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.active[unit] = true
	return nil
}

func (f *fakeUnits) Stop(_ context.Context, unit string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.active, unit)
	return nil
}

func (f *fakeUnits) State(_ context.Context, unit string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.active[unit] {
		return "active"
	}
	return "inactive"
}

const testPluginManifest = `{
  "name": "hello",
  "version": "1.0.0",
  "capabilities": ["events:read", "config:read", "notify:send"],
  "settings_schema": {"type": "object", "properties": {
    "greeting": {"type": "string", "default": "hi", "maxLength": 16}
  }},
  "ui": {"pages": [{"path": "status", "title": "Status"}]}
}`

type pluginEnv struct {
	*authAPI
	mgr *plugins.Manager
}

func newPluginEnv(t *testing.T) *pluginEnv {
	t.Helper()
	a := newAuthAPI(t)
	dir := t.TempDir()
	mgr := &plugins.Manager{ManifestDir: filepath.Join(dir, "share"), ProgramDir: filepath.Join(dir, "lib"),
		Systemd: &fakeUnits{active: map[string]bool{}}}
	for path, data := range map[string]string{
		"share/hello/plugin.json":    testPluginManifest,
		"share/hello/ui/status.html": "<p>status</p>",
		"share/secret.txt":           "outside ui",
		"lib/hello/run":              "#!/bin/sh\n",
	} {
		p := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a.server.Plugins = mgr
	config.SetPluginCatalog(mgr.Lookup)
	t.Cleanup(func() { config.SetPluginCatalog(nil) })
	return &pluginEnv{authAPI: a, mgr: mgr}
}

// socketAs serves the plugin routes as the plugin socket does, with every
// connection coming from the unit of plugin name ("" = not a plugin unit),
// and returns its URL.
func (e *pluginEnv) socketAs(name string) string {
	e.t.Helper()
	sock := httptest.NewUnstartedServer(e.server.PluginRoutes())
	sock.Config.ConnContext = PluginConnContext(func(net.Conn) (string, bool) { return name, name != "" })
	sock.Start()
	e.t.Cleanup(sock.Close)
	return sock.URL
}

// sync starts the plugins the running config enables, like goblind after a
// commit.
func (e *pluginEnv) sync() {
	e.t.Helper()
	cfg, err := e.store.Running()
	if err != nil {
		e.t.Fatal(err)
	}
	e.mgr.Sync(context.Background(), cfg)
}

func call(t *testing.T, base, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	}
	req, err := http.NewRequest(method, base+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestPluginAdminAPI(t *testing.T) {
	e := newPluginEnv(t)
	b := e.browser()
	b.login("admin")

	list := b.expect("GET", "/api/v1/plugins", nil, 200)["plugins"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["state"] != "stopped" {
		t.Fatalf("list: %v", list)
	}
	b.expect("POST", "/api/v1/plugins/nope/enable", nil, 404)
	out := b.expect("POST", "/api/v1/plugins/hello/enable", map[string]any{"capabilities": []string{"stats:read"}}, 400)
	if errCode(out) != "validation_failed" {
		t.Fatalf("unrequested capability: %v", out)
	}
	b.expect("POST", "/api/v1/plugins/hello/enable", map[string]any{"capabilities": []string{"events:read"}}, 200)
	out = b.expect("PUT", "/api/v1/plugins/hello/settings", map[string]any{"settings": map[string]any{"greeting": 5}}, 400)
	if errCode(out) != "validation_failed" {
		t.Fatalf("bad settings: %v", out)
	}
	b.expect("PUT", "/api/v1/plugins/hello/settings", map[string]any{"settings": map[string]any{"greeting": "hey"}}, 200)
	cand, _ := e.store.Candidate()
	if len(cand.Plugins) != 1 || !cand.Plugins[0].Enabled || cand.Plugins[0].Settings["greeting"] != "hey" {
		t.Fatalf("candidate: %+v", cand.Plugins)
	}
	b.expect("POST", "/api/v1/commit", map[string]string{"comment": "plugin"}, 200)

	viewer := e.browser()
	viewer.login("viewer")
	viewer.expect("POST", "/api/v1/plugins/hello/disable", nil, 403)

	audit, _ := os.ReadFile(filepath.Join(e.dir, "audit.log"))
	if !strings.Contains(string(audit), `"event":"plugin_change"`) {
		t.Fatalf("audit:\n%s", audit)
	}
}

func TestPluginSocketScopes(t *testing.T) {
	e := newPluginEnv(t)
	run, _ := e.store.Running()
	run.Plugins = []config.Plugin{{Name: "hello", Enabled: true, Capabilities: []string{"events:read"}}}
	if _, err := e.store.SaveRevision(run, "test", "plugin"); err != nil {
		t.Fatal(err)
	}
	e.sync()
	sock := e.socketAs("hello")

	code, self := call(t, sock, "GET", "/api/v1/plugin/self", nil)
	if code != 200 || self["name"] != "hello" || self["settings"].(map[string]any)["greeting"] != "hi" {
		t.Fatalf("self: %d %v", code, self)
	}
	// approved capability: passes the scope check (no log store here)
	if code, out := call(t, sock, "GET", "/api/v1/logs", nil); code == 401 || code == 403 {
		t.Fatalf("logs: %d %v", code, out)
	}
	// listed route, capability not approved
	code, out := call(t, sock, "GET", "/api/v1/config/running", nil)
	if code != 403 || errCode(out) != "scope_denied" {
		t.Fatalf("config/running: %d %v", code, out)
	}
	if code, _ := call(t, sock, "POST", "/api/v1/plugin/notify", map[string]string{"title": "x"}); code != 403 {
		t.Fatalf("notify: %d", code)
	}
	// routes plugins never get
	for _, r := range [][2]string{{"GET", "/api/v1/config/candidate"}, {"POST", "/api/v1/commit"}, {"GET", "/api/v1/auth/tokens"}, {"GET", "/api/v1/plugins"}, {"GET", "/api/v1/nope"}} {
		if code, out := call(t, sock, r[0], r[1], nil); code != 403 || errCode(out) != "scope_denied" {
			t.Fatalf("%s %s: %d %v", r[0], r[1], code, out)
		}
	}
	// not a plugin unit, or a plugin goblind did not start
	for _, who := range []string{"", "other"} {
		if code, out := call(t, e.socketAs(who), "GET", "/api/v1/plugin/self", nil); code != 401 {
			t.Fatalf("peer %q: %d %v", who, code, out)
		}
	}
	// the plugin routes do not answer off the plugin socket
	plain := httptest.NewServer(e.server.PluginRoutes())
	defer plain.Close()
	if code, _ := call(t, plain.URL, "GET", "/api/v1/plugin/self", nil); code != 404 {
		t.Fatalf("plugin routes off the socket: %d", code)
	}

	audit, _ := os.ReadFile(filepath.Join(e.dir, "audit.log"))
	for _, want := range []string{`"event":"plugin_call"`, `"user":"plugin:hello"`, `"result":"allowed"`, `"result":"denied"`} {
		if !strings.Contains(string(audit), want) {
			t.Fatalf("audit lacks %s:\n%s", want, audit)
		}
	}
}

func TestPluginConfigReadIsRedacted(t *testing.T) {
	e := newPluginEnv(t)
	run, _ := e.store.Running()
	run.Plugins = []config.Plugin{{Name: "hello", Enabled: true, Capabilities: []string{"config:read"}}}
	run.Notifications.Channels = []config.NotifyChannel{{Name: "hook", Type: "webhook", URL: "https://example.com/secret-path"}}
	if _, err := e.store.SaveRevision(run, "test", "plugin"); err != nil {
		t.Fatal(err)
	}
	e.sync()
	resp, err := http.Get(e.socketAs("hello") + "/api/v1/config/running")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"username":"admin"`) {
		t.Fatalf("config/running: %d %s", resp.StatusCode, body)
	}
	if hash := run.Admins[0].PasswordHash; hash == "" || strings.Contains(string(body), hash) || strings.Contains(string(body), "secret-path") {
		t.Fatalf("secrets reached the plugin: %s", body)
	}
	// admins still see everything
	b := e.browser()
	b.login("admin")
	_, _, adminResp := b.do("GET", "/api/v1/config/running", nil)
	if adminResp.StatusCode != 200 {
		t.Fatalf("admin: %d", adminResp.StatusCode)
	}
}

func TestPluginUI(t *testing.T) {
	e := newPluginEnv(t)
	e.mgr.Rescan()
	resp, err := http.Get(e.srv.URL + "/plugin-ui/hello/status.html")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "<p>status</p>" {
		t.Fatalf("page: %d %q", resp.StatusCode, body)
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.HasPrefix(csp, "sandbox allow-scripts allow-forms;") || !strings.Contains(csp, "connect-src 'none'") {
		t.Fatalf("CSP: %q", csp)
	}
	for _, path := range []string{"/plugin-ui/hello/..%2fplugin.json", "/plugin-ui/hello/..%2f..%2fsecret.txt", "/plugin-ui/nope/status.html", "/plugin-ui/hello/missing.html"} {
		resp, err := http.Get(e.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Fatalf("%s: %d", path, resp.StatusCode)
		}
	}
}

func TestPluginAdminAPIProxy(t *testing.T) {
	e := newPluginEnv(t)
	e.mgr.RuntimeDir = t.TempDir()
	run, _ := e.store.Running()
	run.Plugins = []config.Plugin{{Name: "hello", Enabled: true}}
	if _, err := e.store.SaveRevision(run, "test", "plugin"); err != nil {
		t.Fatal(err)
	}
	b := e.browser()
	b.login("admin")
	// not running yet
	if out := b.expect("GET", "/api/v1/plugins/hello/api/status", nil, 503); errCode(out) != "plugin_not_running" {
		t.Fatalf("not running: %v", out)
	}
	e.sync()
	if out := b.expect("GET", "/api/v1/plugins/hello/api/status", nil, 502); errCode(out) != "plugin_error" {
		t.Fatalf("no socket: %v", out)
	}

	sock := e.mgr.AdminSocket("hello")
	if err := os.MkdirAll(filepath.Dir(sock), 0o755); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	type seen struct{ method, path, query, user, role, cookie, auth, csrf, body string }
	got := make(chan seen, 10)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got <- seen{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("X-Goblin-User"), r.Header.Get("X-Goblin-Role"),
			r.Header.Get("Cookie"), r.Header.Get("Authorization"), r.Header.Get(CSRFHeader), string(body)}
		if r.URL.Path == "/html" {
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, "<script>alert(1)</script>")
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "evil", Value: "1"})
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	code, out, resp := b.do("POST", "/api/v1/plugins/hello/api/incidents/7/answer?x=1", map[string]string{"answer": "block"})
	if code != 200 || out["ok"] != true {
		t.Fatalf("proxied POST: %d %v", code, out)
	}
	if resp.Header.Get("Set-Cookie") != "" || resp.Header.Get("Content-Security-Policy") == "" {
		t.Fatalf("response headers: %v", resp.Header)
	}
	s := <-got
	if s.method != "POST" || s.path != "/incidents/7/answer" || s.query != "x=1" || s.user != "admin" || s.role != "admin" ||
		s.cookie != "" || s.auth != "" || s.csrf != "" || s.body != `{"answer":"block"}` {
		t.Fatalf("plugin saw %+v", s)
	}
	// non-JSON answers are refused
	if out := b.expect("GET", "/api/v1/plugins/hello/api/html", nil, 502); errCode(out) != "plugin_error" {
		t.Fatalf("html: %v", out)
	}
	<-got
	// read-only admins get safe methods only
	viewer := e.browser()
	viewer.login("viewer")
	viewer.expect("GET", "/api/v1/plugins/hello/api/status", nil, 200)
	if s := <-got; s.role != "readonly" || s.user != "viewer" {
		t.Fatalf("viewer: %+v", s)
	}
	viewer.expect("POST", "/api/v1/plugins/hello/api/rules", map[string]string{}, 403)

	audit, _ := os.ReadFile(filepath.Join(e.dir, "audit.log"))
	if !strings.Contains(string(audit), `"event":"plugin_admin_call"`) || !strings.Contains(string(audit), `"path":"/incidents/7/answer"`) {
		t.Fatalf("audit:\n%s", audit)
	}
}
