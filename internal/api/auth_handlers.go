package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"sync"

	"github.com/networkgoblinlabs/goblin-gate/internal/auth"
	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/notify"
)

// Cookie and header names shared with the web client (web/src/api/client.ts).
const (
	SessionCookie = "gg_session"
	CSRFCookie    = "gg_csrf"
	CSRFHeader    = "X-CSRF-Token"
)

// Authenticator holds the authentication state. Set Server.Authn to turn
// authentication on; without it every route is open (dev and tests).
type Authenticator struct {
	Sessions *auth.Sessions
	Tokens   *auth.Tokens
	Limiter  *auth.Limiter
	Audit    *auth.Audit // nil = no audit log

	// InsecureCookies drops the Secure flag, for --dev over plain HTTP.
	InsecureCookies bool

	dummyOnce sync.Once
	dummy     string
}

// NewAuthenticator returns an Authenticator with fresh sessions and limiter.
func NewAuthenticator(tokensPath, auditPath string) *Authenticator {
	return &Authenticator{
		Sessions: &auth.Sessions{},
		Tokens:   &auth.Tokens{Path: tokensPath},
		Limiter:  &auth.Limiter{},
		Audit:    &auth.Audit{Path: auditPath},
	}
}

// dummyHash is verified against for unknown users so a failed login takes
// as long whether or not the user exists.
func (a *Authenticator) dummyHash() string {
	a.dummyOnce.Do(func() {
		h, err := auth.HashPassword(auth.RandomToken(24))
		if err != nil {
			panic(err)
		}
		a.dummy = h
	})
	return a.dummy
}

type identityKey struct{}
type socketKey struct{}

// WithSocketConn marks a connection's context as coming from the local
// unix socket (http.Server.ConnContext). Such requests act as "root" with
// the admin role: the socket's file permissions are the access control.
func WithSocketConn(ctx context.Context, _ net.Conn) context.Context {
	return context.WithValue(ctx, socketKey{}, true)
}

// IdentityFrom returns the authenticated identity of a request.
func IdentityFrom(r *http.Request) (auth.Identity, bool) {
	id, ok := r.Context().Value(identityKey{}).(auth.Identity)
	return id, ok
}

func clientAddr(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// currentRole returns the role of an admin with a password in the running
// config, or "" if there is no such user any more.
func (s *Server) currentRole(user string) string {
	cfg, err := s.Store.Running()
	if err != nil {
		return ""
	}
	for _, a := range cfg.Admins {
		if a.Username == user && a.PasswordHash != "" {
			return a.Role
		}
	}
	return ""
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// readOnlyPosts are POST endpoints that change nothing (they take a body
// too large or structured for a query string), so readonly users may call
// them. Sessions still need the CSRF header.
var readOnlyPosts = map[string]bool{
	"/api/v1/config/validate": true,
	"/api/v1/policy/simulate": true,
}

// authenticate is the Server.Auth middleware when Authn is set.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a := s.Authn
		var id auth.Identity
		switch {
		case r.Context().Value(socketKey{}) == true:
			id = auth.Identity{User: "root", Role: "admin", Method: "socket"}
		case r.Header.Get("Authorization") != "":
			bearer, ok := cutBearer(r.Header.Get("Authorization"))
			ti, valid := a.Tokens.Verify(bearer)
			if !ok || !valid {
				writeError(w, http.StatusUnauthorized, "unauthorized", "invalid API token", nil)
				return
			}
			role := s.currentRole(ti.User)
			if role == "" {
				writeError(w, http.StatusUnauthorized, "unauthorized", "the token's user no longer exists", nil)
				return
			}
			if ti.Role == "readonly" {
				role = "readonly" // a token never has more rights than it was made with
			}
			id = auth.Identity{User: ti.User, Role: role, Method: "token", Token: ti.ID}
		default:
			c, err := r.Cookie(SessionCookie)
			var ok bool
			if err == nil {
				id, ok = a.Sessions.Lookup(c.Value)
			}
			if !ok {
				writeError(w, http.StatusUnauthorized, "unauthorized", "log in first", nil)
				return
			}
			role := s.currentRole(id.User)
			if role == "" {
				a.Sessions.DeleteUser(id.User)
				writeError(w, http.StatusUnauthorized, "unauthorized", "the user no longer exists", nil)
				return
			}
			id.Role = role
			if !safeMethod(r.Method) && !id.CheckCSRF(r.Header.Get(CSRFHeader)) {
				writeError(w, http.StatusForbidden, "csrf_failed", "missing or wrong "+CSRFHeader+" header", nil)
				return
			}
		}
		if !safeMethod(r.Method) && !id.CanWrite() && r.URL.Path != "/api/v1/auth/logout" && r.URL.Path != "/api/v1/auth/password" &&
			!(r.Method == http.MethodPost && readOnlyPosts[r.URL.Path]) {
			writeError(w, http.StatusForbidden, "forbidden", "read-only users cannot change anything", nil)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey{}, id)))
	})
}

func cutBearer(h string) (string, bool) {
	const p = "Bearer "
	if len(h) > len(p) && (h[:len(p)] == p || h[:len(p)] == "bearer ") {
		return h[len(p):], true
	}
	return "", false
}

func (s *Server) audit(r *http.Request, event, user string, detail map[string]any) {
	if s.Authn == nil || s.Authn.Audit == nil {
		return
	}
	if user == "" {
		user = s.user(r)
	}
	if err := s.Authn.Audit.Log(event, user, clientAddr(r), detail); err != nil {
		s.Logger.Warn("audit log write failed", "event", event, "error", err)
	}
}

func (s *Server) setCookie(w http.ResponseWriter, name, value string, httpOnly bool, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: httpOnly, Secure: !s.Authn.InsecureCookies, SameSite: http.SameSiteStrictMode,
	})
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// POST /api/v1/auth/login {username, password} → {user, role, csrf_token}
// and the session + CSRF cookies.
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if s.Authn == nil {
		writeError(w, http.StatusNotFound, "auth_disabled", "authentication is disabled (dev mode)", nil)
		return
	}
	a := s.Authn
	var req loginRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	addr := clientAddr(r)
	if ok, wait := a.Limiter.Allowed(addr, req.Username); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds()+1)))
		s.audit(r, "login_blocked", req.Username, nil)
		writeError(w, http.StatusTooManyRequests, "too_many_attempts", "too many failed logins; try again later", nil)
		return
	}
	hash, role := a.dummyHash(), ""
	if cfg, err := s.Store.Running(); err == nil {
		for _, ad := range cfg.Admins {
			if ad.Username == req.Username && ad.PasswordHash != "" {
				hash, role = ad.PasswordHash, ad.Role
			}
		}
	}
	err := auth.VerifyPassword(req.Password, hash)
	if err != nil || role == "" {
		if err != nil && !errors.Is(err, auth.ErrMismatch) {
			s.Logger.Error("login: stored password hash is unusable", "user", req.Username, "error", err)
		}
		locked := a.Limiter.Fail(addr, req.Username)
		s.audit(r, "login_failed", req.Username, map[string]any{"locked": locked})
		if locked {
			s.Notify.Publish(notify.Event{Kind: "login_failed", Key: req.Username, Title: "Login locked out",
				Message: "Too many wrong passwords for user \"" + req.Username + "\" from " + addr + "; logins are paused for a while.",
				Fields:  map[string]string{"user": req.Username, "client": addr}})
		}
		writeError(w, http.StatusUnauthorized, "login_failed", "wrong user name or password", nil)
		return
	}
	a.Limiter.Succeed(addr)
	cookie, id := a.Sessions.Create(req.Username, role)
	s.setCookie(w, SessionCookie, cookie, true, 0)
	s.setCookie(w, CSRFCookie, id.CSRF, false, 0)
	s.audit(r, "login", req.Username, nil)
	writeJSON(w, http.StatusOK, map[string]any{"user": id.User, "role": id.Role, "csrf_token": id.CSRF})
}

// POST /api/v1/auth/logout ends the session.
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r)
	if s.Authn == nil || !ok {
		writeJSON(w, http.StatusOK, map[string]any{"status": "logged_out"})
		return
	}
	if id.Method == "session" {
		s.Authn.Sessions.Delete(id)
		s.setCookie(w, SessionCookie, "", true, -1)
		s.setCookie(w, CSRFCookie, "", false, -1)
		s.audit(r, "logout", id.User, nil)
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "logged_out"})
}

// GET /api/v1/auth/me → the identity ({user, role, method}); in dev mode
// (no Authn) a stand-in admin.
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r)
	if !ok {
		id = auth.Identity{User: "dev", Role: "admin", Method: "none"}
	}
	writeJSON(w, http.StatusOK, id)
}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) (auth.Identity, bool) {
	if s.Authn == nil {
		writeError(w, http.StatusNotFound, "auth_disabled", "authentication is disabled (dev mode)", nil)
		return auth.Identity{}, false
	}
	id, _ := IdentityFrom(r)
	if !id.CanWrite() {
		writeError(w, http.StatusForbidden, "forbidden", "admins only", nil)
		return id, false
	}
	return id, true
}

// GET /api/v1/auth/tokens → {tokens: [TokenInfo without hash]}
func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	list, err := s.Authn.Tokens.List()
	if err != nil {
		s.internal(w, "list tokens", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": list})
}

type tokenRequest struct {
	Name     string `json:"name"`
	ReadOnly bool   `json:"readonly,omitempty"`
}

// POST /api/v1/auth/tokens {name, readonly?} → {token (shown once), info}
func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	var req tokenRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	role := "admin"
	if req.ReadOnly {
		role = "readonly"
	}
	tok, info, err := s.Authn.Tokens.Create(req.Name, id.User, role)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	s.audit(r, "token_created", id.User, map[string]any{"token": info.ID, "name": info.Name, "role": role})
	writeJSON(w, http.StatusCreated, map[string]any{"token": tok, "info": info})
}

// DELETE /api/v1/auth/tokens/{id}
func (s *Server) deleteToken(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	tid := r.PathValue("id")
	if err := s.Authn.Tokens.Delete(tid); errors.Is(err, auth.ErrNoToken) {
		writeError(w, http.StatusNotFound, "token_not_found", "no such token", nil)
		return
	} else if err != nil {
		s.internal(w, "delete token", err)
		return
	}
	s.audit(r, "token_deleted", id.User, map[string]any{"token": tid})
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted"})
}

type passwordRequest struct {
	User            string `json:"user,omitempty"` // default: yourself; others only over the local socket
	CurrentPassword string `json:"current_password,omitempty"`
	NewPassword     string `json:"new_password"`
}

// POST /api/v1/auth/password changes a password. Users change their own
// and must give the current one; root on the local socket may set any
// user's password without it (the console recovery path). The hash is
// saved as a new revision directly (it changes no rendered file) and copied
// into the candidate so a later commit does not undo it. Every session of
// the user ends.
func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	if s.Authn == nil {
		writeError(w, http.StatusNotFound, "auth_disabled", "authentication is disabled (dev mode)", nil)
		return
	}
	id, _ := IdentityFrom(r)
	var req passwordRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	target := req.User
	if target == "" {
		target = id.User
	}
	if id.Method != "socket" {
		if target != id.User {
			writeError(w, http.StatusForbidden, "forbidden", "you can only change your own password", nil)
			return
		}
		addr := clientAddr(r)
		if ok, wait := s.Authn.Limiter.Allowed(addr, target); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds()+1)))
			writeError(w, http.StatusTooManyRequests, "too_many_attempts", "too many failed attempts; try again later", nil)
			return
		}
		hash := s.Authn.dummyHash()
		if cfg, err := s.Store.Running(); err == nil {
			for _, a := range cfg.Admins {
				if a.Username == target && a.PasswordHash != "" {
					hash = a.PasswordHash
				}
			}
		}
		if auth.VerifyPassword(req.CurrentPassword, hash) != nil {
			s.Authn.Limiter.Fail(addr, target)
			s.audit(r, "password_change_failed", target, nil)
			writeError(w, http.StatusForbidden, "wrong_password", "the current password is wrong", nil)
			return
		}
	}
	if err := auth.CheckPassword(req.NewPassword); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", err.Error(), []map[string]string{{"path": "new_password", "message": err.Error()}})
		return
	}
	newHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		s.internal(w, "hash password", err)
		return
	}

	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	running, err := s.Store.Running()
	if err != nil {
		s.internal(w, "read running config", err)
		return
	}
	if !setHash(running, target, newHash) {
		writeError(w, http.StatusNotFound, "user_not_found", "no such user", nil)
		return
	}
	cand, err := s.Store.Candidate()
	if err != nil {
		s.internal(w, "read candidate config", err)
		return
	}
	rev, err := s.Store.SaveRevision(running, s.user(r), "password changed for "+target)
	if err != nil {
		s.internal(w, "save password", err)
		return
	}
	if setHash(cand, target, newHash) {
		if err := s.Store.SetCandidate(cand); err != nil {
			s.Logger.Warn("password: could not update the candidate", "error", err)
		}
	}
	s.Authn.Sessions.DeleteUser(target)
	s.audit(r, "password_changed", target, map[string]any{"by": id.User, "revision": rev})
	writeJSON(w, http.StatusOK, map[string]any{"status": "changed", "revision": rev})
}

func setHash(cfg *config.Config, user, hash string) bool {
	for i := range cfg.Admins {
		if cfg.Admins[i].Username == user {
			cfg.Admins[i].PasswordHash = hash
			return true
		}
	}
	return false
}
