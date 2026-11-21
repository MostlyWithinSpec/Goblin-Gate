package api

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// ssoIdP is a minimal OpenID provider: discovery, one RSA key, and a token
// endpoint answering with the ID token the test prepared.
type ssoIdP struct {
	srv   *httptest.Server
	key   *rsa.PrivateKey
	mu    sync.Mutex
	token string
}

func newSSOIdP(t *testing.T) *ssoIdP {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &ssoIdP{key: key}
	enc := base64.RawURLEncoding.EncodeToString
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"issuer": f.srv.URL, "authorization_endpoint": f.srv.URL + "/authorize",
			"token_endpoint": f.srv.URL + "/token", "jwks_uri": f.srv.URL + "/jwks"})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "k1",
			"n": enc(key.N.Bytes()), "e": enc(big.NewInt(int64(key.E)).Bytes())}}})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.PostForm.Get("code") != "good" || r.PostForm.Get("code_verifier") == "" {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":"invalid_grant"}`)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]string{"id_token": f.token})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// issue prepares the next ID token for user with groups and nonce.
func (f *ssoIdP) issue(t *testing.T, user, nonce string, groups ...string) {
	enc := base64.RawURLEncoding.EncodeToString
	h, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1"})
	c, _ := json.Marshal(map[string]any{"iss": f.srv.URL, "aud": "gate", "sub": "id-" + user, "preferred_username": user,
		"groups": groups, "nonce": nonce, "iat": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix()})
	signed := enc(h) + "." + enc(c)
	sum := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.token = signed + "." + enc(sig)
	f.mu.Unlock()
}

// ssoBrowser follows no redirects, so the test sees each hop.
type ssoBrowser struct {
	t      *testing.T
	base   string
	client *http.Client
}

func (a *authAPI) ssoBrowser() *ssoBrowser {
	jar, _ := cookiejar.New(nil)
	return &ssoBrowser{t: a.t, base: a.srv.URL, client: &http.Client{Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (b *ssoBrowser) get(path string) *http.Response {
	b.t.Helper()
	resp, err := b.client.Get(b.base + path)
	if err != nil {
		b.t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp
}

// start begins a login and returns the state and nonce the gate sent to the
// provider.
func (b *ssoBrowser) start() (state, nonce string) {
	b.t.Helper()
	resp := b.get("/api/v1/auth/sso/login")
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != http.StatusSeeOther || loc.Path != "/authorize" {
		b.t.Fatalf("login: %d %s", resp.StatusCode, loc)
	}
	q := loc.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("redirect_uri") != b.base+"/api/v1/auth/sso/callback" {
		b.t.Fatalf("auth request %v", q)
	}
	return q.Get("state"), q.Get("nonce")
}

// callback finishes a login; it returns the sso_error code ("" = signed in).
func (b *ssoBrowser) callback(query string) string {
	b.t.Helper()
	resp := b.get("/api/v1/auth/sso/callback?" + query)
	if resp.StatusCode == http.StatusOK {
		return ""
	}
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != http.StatusSeeOther || loc.Path != "/login" {
		b.t.Fatalf("callback: %d %s", resp.StatusCode, loc)
	}
	return loc.Query().Get("sso_error")
}

func (b *ssoBrowser) me() (int, map[string]any) {
	b.t.Helper()
	resp, err := b.client.Get(b.base + "/api/v1/auth/me")
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestSSO(t *testing.T) {
	a := newAuthAPI(t)
	idp := newSSOIdP(t)
	a.authn.sso.HTTP = idp.srv.Client()

	b := a.ssoBrowser()
	if resp := b.get("/api/v1/auth/sso"); resp.StatusCode != 200 {
		t.Fatal(resp.Status)
	}
	if code := b.callback("code=good&state=x"); code != "disabled" {
		t.Fatalf("disabled: %q", code)
	}

	cfg, _ := a.store.Running()
	cfg.Roles = []config.Role{{Name: "helpdesk", Scopes: []string{"actions"}}}
	cfg.SSO = config.SSO{Enabled: true, Name: "Authentik", Issuer: idp.srv.URL, ClientID: "gate", ClientSecret: "s",
		Roles: []config.SSORoleMap{{Group: "gg-admins", Role: "admin"}, {Group: "gg-help", Role: "helpdesk"}}}
	if errs := config.Validate(cfg); len(errs) > 0 {
		t.Fatalf("config: %v", errs)
	}
	if _, err := a.store.SaveRevision(cfg, "test", "sso"); err != nil {
		t.Fatal(err)
	}

	// an admin signs in
	state, nonce := b.start()
	idp.issue(t, "alice", nonce, "staff", "gg-admins")
	if code := b.callback("code=good&state=" + state); code != "" {
		t.Fatalf("sign-in refused: %s", code)
	}
	st, me := b.me()
	if st != 200 || me["user"] != "alice" || me["role"] != "admin" || me["via"] != "sso" || me["full_admin"] != true {
		t.Fatalf("me %d %v", st, me)
	}
	// the active sessions list carries via too, so the web can badge it
	var sess struct {
		Sessions []map[string]any `json:"sessions"`
	}
	if resp, err := b.client.Get(b.base + "/api/v1/auth/sessions"); err != nil {
		t.Fatal(err)
	} else if err := json.NewDecoder(resp.Body).Decode(&sess); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
	}
	if len(sess.Sessions) != 1 {
		t.Fatalf("sessions: %v", sess.Sessions)
	}
	if sm := sess.Sessions[0]; sm["user"] != "alice" || sm["via"] != "sso" {
		t.Fatalf("session entry %v want via sso", sess.Sessions[0])
	}
	// the state works once
	if code := b.callback("code=good&state=" + state); code != "state" {
		t.Fatalf("replayed state: %q", code)
	}

	// a helpdesk member gets the custom role
	h := a.ssoBrowser()
	state, nonce = h.start()
	idp.issue(t, "sam@example.com", nonce, "gg-help")
	if code := h.callback("code=good&state=" + state); code != "" {
		t.Fatalf("helpdesk: %s", code)
	}
	if _, me := h.me(); me["role"] != "helpdesk" || jsonString(me["permissions"]) != `["read","actions"]` {
		t.Fatalf("helpdesk me %v", me)
	}

	for name, c := range map[string]struct {
		user, want string
		groups     []string
		state      func(state string) string
	}{
		"no matching group":  {"bob", "no_role", []string{"staff"}, nil},
		"local account name": {"admin", "local_user", []string{"gg-admins"}, nil},
		"bad user name":      {"bob smith", "no_username", []string{"gg-admins"}, nil},
		"state of another":   {"bob", "state", []string{"gg-admins"}, func(string) string { return "forged" }},
		"provider said no":   {"bob", "refused", nil, func(s string) string { return s + "&error=access_denied" }},
	} {
		x := a.ssoBrowser()
		state, nonce := x.start()
		idp.issue(t, c.user, nonce, c.groups...)
		q := "code=good&state=" + state
		if c.state != nil {
			q = "code=good&state=" + c.state(state)
		}
		if code := x.callback(q); code != c.want {
			t.Errorf("%s: got %q, want %q", name, code, c.want)
		}
		if st, _ := x.me(); st != 401 {
			t.Errorf("%s: has a session", name)
		}
	}

	// a token for another login's nonce is refused
	x := a.ssoBrowser()
	state, _ = x.start()
	idp.issue(t, "bob", "someone-elses-nonce", "gg-admins")
	if code := x.callback("code=good&state=" + state); code != "token" {
		t.Fatalf("nonce: %q", code)
	}
	// the callback without the browser's state cookie is refused
	y := a.ssoBrowser()
	state, nonce = x.start()
	idp.issue(t, "bob", nonce, "gg-admins")
	if code := y.callback("code=good&state=" + state); code != "state" {
		t.Fatalf("no cookie: %q", code)
	}

	// SSO turned off: SSO sessions end, local logins stay
	cfg, _ = a.store.Running()
	cfg.SSO.Enabled = false
	a.store.SaveRevision(cfg, "test", "sso off")
	if st, _ := b.me(); st != 401 {
		t.Fatalf("SSO session survived SSO off: %d", st)
	}
	local := a.browser()
	local.login("admin")

	// the audit log has the refusals with their reason
	raw, _ := os.ReadFile(filepath.Join(a.dir, "audit.log"))
	data := string(raw)
	if !strings.Contains(data, `"reason":"local_user"`) || !strings.Contains(data, `"via":"sso"`) {
		t.Fatalf("audit:\n%s", data)
	}
}
