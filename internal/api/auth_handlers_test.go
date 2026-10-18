package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/auth"
	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// authAPI is a test server with authentication on and two users:
// admin (admin) and viewer (readonly), password "correct horse battery".
type authAPI struct {
	*testAPI
	authn *Authenticator
	dir   string
}

const testPassword = "correct horse battery"

func newAuthAPI(t *testing.T) *authAPI {
	t.Helper()
	dir := t.TempDir()
	authn := NewAuthenticator(filepath.Join(dir, "tokens.json"), filepath.Join(dir, "audit.log"))
	authn.InsecureCookies = true
	a := newTestAPIWith(t, func(s *Server) {
		s.Authn = authn
		s.Static = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "<html>ui</html>") })
	})
	hash, err := auth.HashPassword(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Admins = []config.Admin{{Username: "admin", PasswordHash: hash, Role: "admin"}, {Username: "viewer", PasswordHash: hash, Role: "readonly"}}
	if _, err := a.store.SaveRevision(cfg, "test", "users"); err != nil {
		t.Fatal(err)
	}
	return &authAPI{testAPI: a, authn: authn, dir: dir}
}

// browser is an HTTP client with a cookie jar that sends the CSRF header
// like the web client does.
type browser struct {
	t      *testing.T
	base   string
	client *http.Client
	csrf   string
	header map[string]string
}

func (a *authAPI) browser() *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{t: a.t, base: a.srv.URL, client: &http.Client{Jar: jar}, header: map[string]string{}}
}

func (b *browser) do(method, path string, body any) (int, map[string]any, *http.Response) {
	b.t.Helper()
	var rd io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		rd = bytes.NewReader(data)
	}
	req, _ := http.NewRequest(method, b.base+path, rd)
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if b.csrf != "" {
		req.Header.Set(CSRFHeader, b.csrf)
	}
	for k, v := range b.header {
		req.Header.Set(k, v)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out, resp
}

func (b *browser) expect(method, path string, body any, want int) map[string]any {
	b.t.Helper()
	got, out, _ := b.do(method, path, body)
	if got != want {
		b.t.Fatalf("%s %s: status %d, want %d: %v", method, path, got, want, out)
	}
	return out
}

func (b *browser) login(user string) {
	b.t.Helper()
	out := b.expect("POST", "/api/v1/auth/login", map[string]string{"username": user, "password": testPassword}, 200)
	b.csrf, _ = out["csrf_token"].(string)
}

func TestLoginSessionAndCSRF(t *testing.T) {
	a := newAuthAPI(t)
	b := a.browser()
	if out := b.expect("GET", "/api/v1/config/running", nil, 401); errCode(out) != "unauthorized" {
		t.Fatalf("anonymous: %v", out)
	}
	// static UI and login are reachable without a session
	if code, _, _ := b.do("GET", "/policy", nil); code != 200 {
		t.Fatalf("static: %d", code)
	}

	_, _, resp := b.do("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": testPassword})
	var sess, csrf *http.Cookie
	for _, c := range resp.Cookies() {
		switch c.Name {
		case SessionCookie:
			sess = c
		case CSRFCookie:
			csrf = c
		}
	}
	if sess == nil || !sess.HttpOnly || sess.SameSite != http.SameSiteStrictMode || csrf == nil || csrf.HttpOnly {
		t.Fatalf("cookies: %+v %+v", sess, csrf)
	}
	b.csrf = csrf.Value

	if me := b.expect("GET", "/api/v1/auth/me", nil, 200); me["user"] != "admin" || me["role"] != "admin" || me["method"] != "session" {
		t.Fatalf("me: %v", me)
	}
	b.expect("PATCH", "/api/v1/config/candidate", map[string]any{"path": "system.hostname", "value": "gg"}, 200)

	// a state-changing request without the CSRF header is refused
	b.csrf = ""
	if out := b.expect("PATCH", "/api/v1/config/candidate", map[string]any{"path": "system.hostname", "value": "x"}, 403); errCode(out) != "csrf_failed" {
		t.Fatalf("no csrf: %v", out)
	}
	b.csrf = "wrong"
	b.expect("POST", "/api/v1/commit/discard", nil, 403)
	b.csrf = csrf.Value

	// commits are audited with the user name
	b.expect("POST", "/api/v1/commit", map[string]any{"comment": "via session"}, 200)
	revs := b.expect("GET", "/api/v1/revisions", nil, 200)["revisions"].([]any)
	if revs[0].(map[string]any)["user"] != "admin" {
		t.Fatalf("revision user: %v", revs[0])
	}
	audit, _ := os.ReadFile(filepath.Join(a.dir, "audit.log"))
	if !strings.Contains(string(audit), `"event":"login"`) || !strings.Contains(string(audit), `"event":"commit"`) {
		t.Fatalf("audit log:\n%s", audit)
	}

	b.expect("POST", "/api/v1/auth/logout", nil, 200)
	b.expect("GET", "/api/v1/config/running", nil, 401)
}

func TestLoginFailuresAndLockout(t *testing.T) {
	a := newAuthAPI(t)
	b := a.browser()
	for _, creds := range []map[string]string{
		{"username": "admin", "password": "wrong password!"},
		{"username": "nobody", "password": testPassword},
	} {
		if out := b.expect("POST", "/api/v1/auth/login", creds, 401); errCode(out) != "login_failed" {
			t.Fatalf("%v: %v", creds, out)
		}
	}
	for i := 2; i < auth.MaxFailures; i++ {
		b.expect("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "nope nope nope"}, 401)
	}
	// locked: even the right password is refused for a while
	code, out, resp := b.do("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": testPassword})
	if code != 429 || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("lockout: %d %v", code, out)
	}
}

func TestReadonlyUser(t *testing.T) {
	a := newAuthAPI(t)
	b := a.browser()
	b.login("viewer")
	b.expect("GET", "/api/v1/config/candidate", nil, 200)
	if out := b.expect("PATCH", "/api/v1/config/candidate", map[string]any{"path": "system.hostname", "value": "x"}, 403); errCode(out) != "forbidden" {
		t.Fatalf("readonly write: %v", out)
	}
	b.expect("POST", "/api/v1/commit", nil, 403)
	b.expect("GET", "/api/v1/auth/tokens", nil, 403)
	b.expect("POST", "/api/v1/auth/logout", nil, 200)
}

func TestReadonlySeesNoSecrets(t *testing.T) {
	a := newAuthAPI(t)
	cfg, _ := a.store.Running()
	cfg.Feeds = []config.Feed{{Name: "paid", Type: "ip", URL: "https://feeds.example.net/x", Enabled: true, AuthHeader: "Authorization: Bearer feedsecret"}}
	cfg.DDNS = []config.DDNS{{Name: "home", Enabled: true, Provider: "cloudflare", Hostname: "host.example.net", Token: "ddns-secret-token", ZoneID: "abc123"}}
	cfg.LogExport = []config.LogExport{
		{Name: "syslog-hq", Enabled: true, Type: "syslog", Address: "10.0.0.2:514"},
		{Name: "loki-home", Enabled: true, Type: "loki", URL: "https://loki.example.net/loki/api/v1/push", Username: "pusher", Password: "loki-secret-password", Token: "loki-secret-token"},
	}
	if _, err := a.store.SaveRevision(cfg, "test", "feed"); err != nil {
		t.Fatal(err)
	}
	// secrets: password hashes, the feed's auth header, the DDNS token and the log export credentials
	hashes := func(cfg map[string]any) (n int) {
		for _, ad := range cfg["admins"].([]any) {
			if h, _ := ad.(map[string]any)["password_hash"].(string); h != "" {
				n++
			}
		}
		if strings.Contains(jsonString(cfg), "feedsecret") {
			n++
		}
		if strings.Contains(jsonString(cfg), "ddns-secret-token") {
			n++
		}
		if strings.Contains(jsonString(cfg), "loki-secret-password") {
			n++
		}
		if strings.Contains(jsonString(cfg), "loki-secret-token") {
			n++
		}
		return n
	}
	viewer := a.browser()
	viewer.login("viewer")
	for _, p := range []string{"/api/v1/config/running", "/api/v1/config/candidate"} {
		if n := hashes(viewer.expect("GET", p, nil, 200)); n != 0 {
			t.Fatalf("readonly %s: %d secrets", p, n)
		}
	}
	viewer.expect("GET", "/api/v1/system/backup", nil, 403)
	_, _, resp := viewer.do("GET", "/api/v1/config/running", nil)
	if resp.Header.Get("X-Frame-Options") != "SAMEORIGIN" || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("security headers missing: %v", resp.Header)
	}

	admin := a.browser()
	admin.login("admin")
	if n := hashes(admin.expect("GET", "/api/v1/config/running", nil, 200)); n != 6 {
		t.Fatalf("admin running: %d secrets, want 6 (2 hashes + feed header + ddns token + loki password + loki token)", n)
	}
	if code, _, _ := admin.do("GET", "/api/v1/system/backup", nil); code != 200 {
		t.Fatalf("admin backup: %d", code)
	}
}

func TestAPITokens(t *testing.T) {
	a := newAuthAPI(t)
	b := a.browser()
	b.login("admin")
	out := b.expect("POST", "/api/v1/auth/tokens", map[string]any{"name": "backup"}, 201)
	tok := out["token"].(string)
	ro := b.expect("POST", "/api/v1/auth/tokens", map[string]any{"name": "grafana", "readonly": true}, 201)["token"].(string)
	list := b.expect("GET", "/api/v1/auth/tokens", nil, 200)["tokens"].([]any)
	if len(list) != 2 || strings.Contains(strings.ToLower(jsonString(list)), "hash") {
		t.Fatalf("token list: %v", list)
	}

	// a token works without cookies or CSRF
	script := a.browser()
	script.header["Authorization"] = "Bearer " + tok
	script.expect("PATCH", "/api/v1/config/candidate", map[string]any{"path": "system.hostname", "value": "scripted"}, 200)
	if me := script.expect("GET", "/api/v1/auth/me", nil, 200); me["method"] != "token" || me["user"] != "admin" {
		t.Fatalf("token me: %v", me)
	}
	roClient := a.browser()
	roClient.header["Authorization"] = "Bearer " + ro
	roClient.expect("GET", "/api/v1/config/running", nil, 200)
	roClient.expect("POST", "/api/v1/commit", nil, 403)

	bad := a.browser()
	bad.header["Authorization"] = "Bearer gg_nope_nope"
	bad.expect("GET", "/api/v1/config/running", nil, 401)

	id := out["info"].(map[string]any)["id"].(string)
	b.expect("DELETE", "/api/v1/auth/tokens/"+id, nil, 200)
	script.expect("GET", "/api/v1/config/running", nil, 401)
	b.expect("DELETE", "/api/v1/auth/tokens/"+id, nil, 404)
}

func TestUserRemovedEndsAccess(t *testing.T) {
	a := newAuthAPI(t)
	b := a.browser()
	b.login("viewer")
	cfg, _ := a.store.Running()
	cfg.Admins = cfg.Admins[:1]
	if _, err := a.store.SaveRevision(cfg, "test", "remove viewer"); err != nil {
		t.Fatal(err)
	}
	b.expect("GET", "/api/v1/config/running", nil, 401)
}

func TestSocketIdentity(t *testing.T) {
	a := newAuthAPI(t)
	s := &Server{Store: a.store, Authn: a.authn, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	h := s.Routes()
	req := httptest.NewRequest("GET", "/api/v1/auth/me", nil)
	req = req.WithContext(WithSocketConn(context.Background(), (net.Conn)(nil)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"method":"socket"`) {
		t.Fatalf("socket: %d %s", rec.Code, rec.Body)
	}
}

func jsonString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestChangePassword(t *testing.T) {
	a := newAuthAPI(t)
	b := a.browser()
	b.login("viewer") // readonly users may change their own password
	// a pending candidate edit by someone else must not undo the change
	cand, _ := a.store.Candidate()
	cand.System.Hostname = "pending-edit"
	a.store.SetCandidate(cand)

	b.expect("POST", "/api/v1/auth/password", map[string]any{"current_password": "nope nope nope", "new_password": "a brand new secret"}, 403)
	b.expect("POST", "/api/v1/auth/password", map[string]any{"current_password": testPassword, "new_password": "short"}, 422)
	b.expect("POST", "/api/v1/auth/password", map[string]any{"user": "admin", "current_password": testPassword, "new_password": "a brand new secret"}, 403)
	b.expect("POST", "/api/v1/auth/password", map[string]any{"current_password": testPassword, "new_password": "a brand new secret"}, 200)

	// sessions end; the new password works, the old one does not
	b.expect("GET", "/api/v1/config/running", nil, 401)
	b.expect("POST", "/api/v1/auth/login", map[string]string{"username": "viewer", "password": testPassword}, 401)
	b.expect("POST", "/api/v1/auth/login", map[string]string{"username": "viewer", "password": "a brand new secret"}, 200)
	cand, _ = a.store.Candidate()
	run, _ := a.store.Running()
	if cand.System.Hostname != "pending-edit" || cand.Admins[1].PasswordHash != run.Admins[1].PasswordHash {
		t.Fatalf("candidate not kept in step: %q", cand.System.Hostname)
	}
}

func TestSocketResetsPassword(t *testing.T) {
	a := newAuthAPI(t)
	s := &Server{Store: a.store, Authn: a.authn, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	body := strings.NewReader(`{"user":"admin","new_password":"recovered password"}`)
	req := httptest.NewRequest("POST", "/api/v1/auth/password", body)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(WithSocketConn(context.Background(), (net.Conn)(nil)))
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("socket reset: %d %s", rec.Code, rec.Body)
	}
	a.browser().expect("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "recovered password"}, 200)
}

func TestActiveSessions(t *testing.T) {
	a := newAuthAPI(t)

	admin := a.browser()
	admin.login("admin")
	viewer := a.browser()
	viewer.login("viewer")

	// Admin sees both sessions; own is marked current.
	out := admin.expect("GET", "/api/v1/auth/sessions", nil, 200)
	sess := out["sessions"].([]any)
	if len(sess) != 2 {
		t.Fatalf("admin session list: %d entries, want 2: %v", len(sess), sess)
	}
	var adminID, viewerID string
	currentCount := 0
	for _, s := range sess {
		m := s.(map[string]any)
		if m["user"] == "admin" {
			adminID = m["id"].(string)
			if m["current"] != true {
				t.Errorf("admin session not marked current: %v", m)
			}
			currentCount++
		}
		if m["user"] == "viewer" {
			viewerID = m["id"].(string)
			if m["current"] != false {
				t.Errorf("viewer session marked current for admin caller: %v", m)
			}
		}
	}
	if currentCount != 1 {
		t.Errorf("current count = %d, want 1", currentCount)
	}

	// The raw cookie value must never appear in the response body.
	cookieVal := ""
	for _, c := range admin.client.Jar.Cookies(mustURL(t, admin.base)) {
		if c.Name == SessionCookie {
			cookieVal = c.Value
		}
	}
	if cookieVal != "" && strings.Contains(jsonString(out), cookieVal) {
		t.Fatal("session cookie value leaked in API response")
	}

	// Readonly user sees only their own session.
	vout := viewer.expect("GET", "/api/v1/auth/sessions", nil, 200)
	vsess := vout["sessions"].([]any)
	if len(vsess) != 1 {
		t.Fatalf("viewer session list: %d entries, want 1: %v", len(vsess), vsess)
	}
	vm := vsess[0].(map[string]any)
	if vm["user"] != "viewer" || vm["current"] != true {
		t.Errorf("viewer sees %+v, want own session marked current", vm)
	}

	// Readonly cannot delete admin's session.
	if out := viewer.expect("DELETE", "/api/v1/auth/sessions/"+adminID, nil, 403); errCode(out) != "forbidden" {
		t.Fatalf("viewer delete admin: %v", out)
	}

	// Unknown id → 404 not_found.
	if out := admin.expect("DELETE", "/api/v1/auth/sessions/ffffffffffffffff", nil, 404); errCode(out) != "not_found" {
		t.Fatalf("unknown session: %v", out)
	}

	// Admin can delete viewer's session.
	admin.expect("DELETE", "/api/v1/auth/sessions/"+viewerID, nil, 204)
	// Viewer is now logged out.
	viewer.expect("GET", "/api/v1/config/running", nil, 401)

	// Deleting the same id again → 404.
	admin.expect("DELETE", "/api/v1/auth/sessions/"+viewerID, nil, 404)
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
