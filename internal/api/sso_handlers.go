package api

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/auth/oidc"
	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/notify"
)

// Single sign-on with OpenID Connect: the authorization code flow with PKCE,
// state and nonce. The browser goes to /api/v1/auth/sso/login, which sends it
// to the provider; the provider sends it back to /api/v1/auth/sso/callback,
// which checks everything, maps the user's groups to a role and starts a
// normal session (marked via "sso"). No local account is made: the user exists
// only while the provider vouches for them, and their role is checked against
// the config on every request (sessionAccess).

const (
	ssoStateCookie  = "gg_sso_state"
	ssoCallbackPath = "/api/v1/auth/sso/callback"
	ssoPendingTTL   = 10 * time.Minute
	ssoMaxPending   = 1000
)

// ssoState is the Authenticator's single sign-on part: the logins in flight
// and the provider client (rebuilt when the config changes).
type ssoState struct {
	mu       sync.Mutex
	pending  map[string]ssoPending
	key      string
	provider *oidc.Provider
	// HTTP overrides the provider's HTTP client (tests).
	HTTP *http.Client
}

type ssoPending struct {
	nonce, verifier, redirect string
	created                   time.Time
}

// provider returns the client for the config, reusing the cached one (and its
// discovery and keys) while the config is the same.
func (st *ssoState) providerFor(c config.SSO) (*oidc.Provider, error) {
	key := strings.Join([]string{c.Issuer, c.ClientID, c.ClientSecret, strings.Join(c.Scopes, " "), c.CACertificate}, "\x00")
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.provider != nil && st.key == key {
		return st.provider, nil
	}
	client := st.HTTP
	if client == nil {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if c.CACertificate != "" && !pool.AppendCertsFromPEM([]byte(c.CACertificate)) {
			return nil, errors.New("ca_certificate is not a PEM certificate")
		}
		client = &http.Client{
			Timeout:   10 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, Proxy: http.ProxyFromEnvironment},
			// the token and key endpoints don't redirect; a redirect would be odd
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	st.provider = &oidc.Provider{Issuer: c.Issuer, ClientID: c.ClientID, ClientSecret: c.ClientSecret, Scopes: c.Scopes, HTTP: client}
	if len(c.Scopes) == 0 {
		// the defaults, as far as the provider knows them
		st.provider.OptionalScopes = []string{"profile", "email", "groups"}
	}
	st.key = key
	return st.provider, nil
}

func (st *ssoState) put(state string, p ssoPending) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.pending == nil {
		st.pending = map[string]ssoPending{}
	}
	for k, v := range st.pending {
		if time.Since(v.created) > ssoPendingTTL {
			delete(st.pending, k)
		}
	}
	if len(st.pending) >= ssoMaxPending {
		return false
	}
	st.pending[state] = p
	return true
}

// take returns and forgets a login in flight: each state works once.
func (st *ssoState) take(state string) (ssoPending, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	p, ok := st.pending[state]
	delete(st.pending, state)
	if !ok || time.Since(p.created) > ssoPendingTTL {
		return ssoPending{}, false
	}
	return p, true
}

// ssoConfig returns the running SSO config when it is on.
func (s *Server) ssoConfig() (*config.Config, bool) {
	cfg, err := s.Store.Running()
	if err != nil || !cfg.SSO.Enabled {
		return nil, false
	}
	return cfg, true
}

// GET /api/v1/auth/sso → {enabled, name}: whether the login page shows the
// single sign-on button.
func (s *Server) ssoStatus(w http.ResponseWriter, r *http.Request) {
	cfg, on := s.ssoConfig()
	if !on || s.Authn == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "name": ""})
		return
	}
	name := cfg.SSO.Name
	if name == "" {
		name = "single sign-on"
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "name": name})
}

func (s *Server) ssoRedirectURL(r *http.Request, c config.SSO) string {
	if c.RedirectURL != "" {
		return c.RedirectURL
	}
	scheme := "https"
	if s.Authn.InsecureCookies {
		scheme = "http" // --dev over plain HTTP
	}
	// the Host header only picks which registered URL we name; the provider
	// refuses any redirect URL that is not registered for the client
	return scheme + "://" + r.Host + ssoCallbackPath
}

// ssoFail sends the browser back to the login page with a reason code the
// web turns into a message.
func (s *Server) ssoFail(w http.ResponseWriter, r *http.Request, code, user string, detail map[string]any) {
	if detail == nil {
		detail = map[string]any{}
	}
	detail["reason"] = code
	detail["via"] = "sso"
	s.audit(r, "login_failed", user, detail)
	s.clearSSOState(w)
	http.Redirect(w, r, "/login?sso_error="+url.QueryEscape(code), http.StatusSeeOther)
}

func (s *Server) clearSSOState(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: ssoStateCookie, Value: "", Path: "/api/v1/auth/sso/", MaxAge: -1,
		HttpOnly: true, Secure: !s.Authn.InsecureCookies, SameSite: http.SameSiteLaxMode})
}

// GET /api/v1/auth/sso/login → 303 to the provider.
func (s *Server) ssoLogin(w http.ResponseWriter, r *http.Request) {
	if s.Authn == nil {
		writeError(w, http.StatusNotFound, "auth_disabled", "authentication is disabled (dev mode)", nil)
		return
	}
	cfg, on := s.ssoConfig()
	if !on {
		http.Redirect(w, r, "/login?sso_error=disabled", http.StatusSeeOther)
		return
	}
	if ok, _ := s.Authn.Limiter.Allowed(clientAddr(r), ssoLimitKey(clientAddr(r))); !ok {
		s.ssoFail(w, r, "too_many_attempts", "", nil)
		return
	}
	p, err := s.Authn.sso.providerFor(cfg.SSO)
	if err != nil {
		s.ssoFail(w, r, "provider", "", map[string]any{"error": err.Error()})
		return
	}
	state, nonce := oidc.Random(32), oidc.Random(32)
	verifier, challenge := oidc.PKCE()
	redirect := s.ssoRedirectURL(r, cfg.SSO)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	u, err := p.AuthURL(ctx, redirect, state, nonce, challenge)
	if err != nil {
		s.Logger.Warn("sso: provider unreachable", "error", err)
		s.ssoFail(w, r, "provider", "", map[string]any{"error": err.Error()})
		return
	}
	if !s.Authn.sso.put(state, ssoPending{nonce: nonce, verifier: verifier, redirect: redirect, created: time.Now()}) {
		s.ssoFail(w, r, "busy", "", nil)
		return
	}
	// binds the login to this browser: the callback must come back with it.
	// Lax, because the provider's redirect back is a cross-site navigation.
	http.SetCookie(w, &http.Cookie{Name: ssoStateCookie, Value: state, Path: "/api/v1/auth/sso/", MaxAge: int(ssoPendingTTL.Seconds()),
		HttpOnly: true, Secure: !s.Authn.InsecureCookies, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, u, http.StatusSeeOther)
}

// ssoUsernameRe: what an SSO user name may look like (email addresses too).
var ssoUsernameRe = regexp.MustCompile(`^[A-Za-z0-9._@+-]{1,128}$`)

// ssoRole maps the user's groups to a role: the first mapping whose group
// they are in, else the default role ("" = refuse).
func ssoRole(c config.SSO, groups []string) string {
	for _, m := range c.Roles {
		if slices.Contains(groups, m.Group) {
			return m.Role
		}
	}
	return c.DefaultRole
}

var ssoDone = template.Must(template.New("sso").Parse(`<!doctype html><meta charset="utf-8"><meta http-equiv="refresh" content="0;url=/"><title>Signed in</title><a href="/">Continue</a>`))

// GET /api/v1/auth/sso/callback?code=&state= → the session cookies and a page
// that goes on to the web UI, or back to /login?sso_error=<code>.
func (s *Server) ssoCallback(w http.ResponseWriter, r *http.Request) {
	if s.Authn == nil {
		writeError(w, http.StatusNotFound, "auth_disabled", "authentication is disabled (dev mode)", nil)
		return
	}
	a := s.Authn
	addr := clientAddr(r)
	cfg, on := s.ssoConfig()
	if !on {
		s.ssoFail(w, r, "disabled", "", nil)
		return
	}
	if ok, _ := a.Limiter.Allowed(addr, ssoLimitKey(addr)); !ok {
		s.ssoFail(w, r, "too_many_attempts", "", nil)
		return
	}
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		// the provider said no (the user cancelled, or is not allowed in)
		s.ssoFail(w, r, "refused", "", map[string]any{"provider_error": truncate(e, 100), "description": truncate(q.Get("error_description"), 300)})
		return
	}
	state := q.Get("state")
	c, err := r.Cookie(ssoStateCookie)
	if state == "" || err != nil || c.Value != state {
		a.Limiter.Fail(addr, ssoLimitKey(addr))
		s.ssoFail(w, r, "state", "", nil)
		return
	}
	pend, ok := a.sso.take(state)
	if !ok {
		s.ssoFail(w, r, "state", "", nil)
		return
	}
	p, err := a.sso.providerFor(cfg.SSO)
	if err != nil {
		s.ssoFail(w, r, "provider", "", map[string]any{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	raw, err := p.Exchange(ctx, q.Get("code"), pend.redirect, pend.verifier)
	if err != nil {
		s.Logger.Warn("sso: code exchange failed", "error", err)
		a.Limiter.Fail(addr, ssoLimitKey(addr))
		s.ssoFail(w, r, "exchange", "", map[string]any{"error": truncate(err.Error(), 300)})
		return
	}
	claims, err := p.Verify(ctx, raw, pend.nonce)
	if err != nil {
		s.Logger.Warn("sso: ID token refused", "error", err)
		a.Limiter.Fail(addr, ssoLimitKey(addr))
		s.ssoFail(w, r, "token", "", map[string]any{"error": truncate(err.Error(), 300)})
		return
	}

	user := ""
	if cl := cfg.SSO.UsernameClaim; cl != "" {
		user = claims.String(cl)
	} else if user = claims.String("preferred_username"); user == "" {
		user = claims.String("email")
	}
	if !ssoUsernameRe.MatchString(user) {
		s.ssoFail(w, r, "no_username", "", map[string]any{"sub": truncate(claims.String("sub"), 100)})
		return
	}
	if adminIndex(cfg.Admins, user) >= 0 {
		// a local account with this name: never let the provider speak for it
		s.ssoFail(w, r, "local_user", user, nil)
		return
	}
	groupsClaim := cfg.SSO.GroupsClaim
	if groupsClaim == "" {
		groupsClaim = "groups"
	}
	role := ssoRole(cfg.SSO, claims.Strings(groupsClaim))
	if role == "" || !cfg.HasRole(role) {
		s.ssoFail(w, r, "no_role", user, map[string]any{"groups": claims.Strings(groupsClaim)})
		return
	}

	a.Limiter.Succeed(addr)
	s.clearSSOState(w)
	cookie, id := a.Sessions.CreateSSO(user, role, addr, r.Header.Get("User-Agent"))
	s.setCookie(w, SessionCookie, cookie, true, 0)
	s.setCookie(w, CSRFCookie, id.CSRF, false, 0)
	s.audit(r, "login", user, map[string]any{"via": "sso", "role": role})
	s.Notify.Publish(notify.Event{Kind: "login", Key: user, Title: "Login: " + user,
		Message: user + " signed in with single sign-on from " + addr, Fields: map[string]string{"user": user, "addr": addr, "via": "sso"}})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	ssoDone.Execute(w, nil)
}

// ssoLimitKey is the limiter's "user" for SSO attempts: per address, so
// failures from one client never lock single sign-on for everyone (the
// provider limits its own password guessing).
func ssoLimitKey(addr string) string { return "sso:" + addr }

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
