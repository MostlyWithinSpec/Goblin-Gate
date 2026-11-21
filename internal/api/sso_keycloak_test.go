package api

import (
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// TestSSOKeycloak signs in through a real Keycloak (scripts/sso-e2e.ps1 starts
// one and sets GG_SSO_KEYCLOAK to its realm's issuer): the gate's redirect,
// Keycloak's own login form, the code exchange, Keycloak's signed ID token and
// its groups claim. Skipped without GG_SSO_KEYCLOAK.
func TestSSOKeycloak(t *testing.T) {
	issuer := os.Getenv("GG_SSO_KEYCLOAK")
	if issuer == "" {
		t.Skip("GG_SSO_KEYCLOAK not set (run scripts/sso-e2e.ps1)")
	}
	a := newAuthAPI(t)
	cfg, _ := a.store.Running()
	cfg.Roles = []config.Role{{Name: "helpdesk", Scopes: []string{"actions"}}}
	cfg.SSO = config.SSO{Enabled: true, Name: "Keycloak", Issuer: issuer, ClientID: "gate", ClientSecret: "gate-secret",
		Roles: []config.SSORoleMap{{Group: "gg-admins", Role: "admin"}, {Group: "gg-help", Role: "helpdesk"}}}
	if errs := config.Validate(cfg); len(errs) > 0 {
		t.Fatalf("config: %v", errs)
	}
	if _, err := a.store.SaveRevision(cfg, "test", "sso"); err != nil {
		t.Fatal(err)
	}

	formAction := regexp.MustCompile(`<form[^>]+id="kc-form-login"[^>]+action="([^"]+)"`)
	signIn := func(user, password string) (*ssoBrowser, string) {
		t.Helper()
		b := a.ssoBrowser()
		resp := b.get("/api/v1/auth/sso/login")
		authURL := resp.Header.Get("Location")
		if !strings.HasPrefix(authURL, issuer) {
			t.Fatalf("login went to %q", authURL)
		}
		// Keycloak's login page, then its form, with the same cookie jar
		page, err := b.client.Get(authURL)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(page.Body)
		page.Body.Close()
		m := formAction.FindSubmatch(body)
		if m == nil {
			t.Fatalf("no login form (%s, location %q):\n%.500s", page.Status, page.Header.Get("Location"), body)
		}
		post, err := b.client.PostForm(html.UnescapeString(string(m[1])), url.Values{"username": {user}, "password": {password}, "credentialId": {""}})
		if err != nil {
			t.Fatal(err)
		}
		post.Body.Close()
		back, _ := url.Parse(post.Header.Get("Location"))
		if post.StatusCode != http.StatusFound || back.Path != ssoCallbackPath {
			t.Fatalf("Keycloak answered %s, location %q", post.Status, back)
		}
		return b, b.callback(back.RawQuery)
	}

	b, code := signIn("alice", "pw-alice")
	if code != "" {
		t.Fatalf("alice refused: %s", code)
	}
	if st, me := b.me(); st != 200 || me["user"] != "alice" || me["role"] != "admin" || me["via"] != "sso" {
		t.Fatalf("alice: %d %v", st, me)
	}
	s, code := signIn("sam", "pw-sam")
	if code != "" {
		t.Fatalf("sam refused: %s", code)
	}
	if _, me := s.me(); me["role"] != "helpdesk" || jsonString(me["permissions"]) != `["read","actions"]` {
		t.Fatalf("sam: %v", me)
	}

	// a member of no mapped group gets no session
	cfg, _ = a.store.Running()
	cfg.SSO.Roles = cfg.SSO.Roles[:1]
	a.store.SaveRevision(cfg, "test", "sso: admins only")
	if _, code := signIn("sam", "pw-sam"); code != "no_role" {
		t.Fatalf("sam without a mapped group: %q", code)
	}
}
