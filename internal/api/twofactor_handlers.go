package api

import (
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/auth"
	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/qr"
)

// Two-factor login (TOTP). A secret is created by setup and kept here,
// pending, until the user proves their app has it (enable); only then is it
// stored in the config. It takes effect at once, like a password change.

// pendingTOTPTTL is how long a set-up secret waits for its first code.
const pendingTOTPTTL = 10 * time.Minute

type pendingTOTP struct {
	secret  string
	expires time.Time
}

// totpState is the Authenticator's two-factor state: pending secrets and the
// replay guard.
type totpState struct {
	guard   auth.TOTPGuard
	mu      sync.Mutex
	pending map[string]pendingTOTP // user -> secret waiting for enable
}

func (t *totpState) setPending(user, secret string, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pending == nil {
		t.pending = map[string]pendingTOTP{}
	}
	for u, p := range t.pending {
		if now.After(p.expires) {
			delete(t.pending, u)
		}
	}
	t.pending[user] = pendingTOTP{secret: secret, expires: now.Add(pendingTOTPTTL)}
}

func (t *totpState) takePending(user string, now time.Time) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.pending[user]
	if !ok || now.After(p.expires) {
		delete(t.pending, user)
		return "", false
	}
	return p.secret, true
}

func (t *totpState) dropPending(user string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.pending, user)
}

// adminTOTP returns the user's stored two-factor secret ("" = off).
func adminTOTP(cfg *config.Config, user string) string {
	for _, a := range cfg.Admins {
		if a.Username == user {
			return a.TOTPSecret
		}
	}
	return ""
}

// twoFactorUser is the logged-in admin a 2FA request acts for: a browser
// session or token of a user in the config (not the root socket).
func (s *Server) twoFactorUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	if s.Authn == nil {
		writeError(w, http.StatusNotFound, "auth_disabled", "authentication is disabled (dev mode)", nil)
		return "", false
	}
	id, _ := IdentityFrom(r)
	if (id.Method != "session" && id.Method != "token") || s.currentRole(id.User) == "" {
		writeError(w, http.StatusForbidden, "forbidden", "log in as the admin whose two-factor login you want to change", nil)
		return "", false
	}
	return id.User, true
}

// POST /api/v1/auth/2fa/setup → {secret, otpauth_url, qr_svg}: a new secret,
// pending until enabled.
func (s *Server) twoFactorSetup(w http.ResponseWriter, r *http.Request) {
	user, ok := s.twoFactorUser(w, r)
	if !ok {
		return
	}
	secret := auth.NewTOTPSecret()
	s.Authn.totp.setPending(user, secret, time.Now())
	host, _ := os.Hostname()
	if cfg, err := s.Store.Running(); err == nil && cfg.System.Hostname != "" {
		host = cfg.System.Hostname
	}
	link := auth.TOTPURL(secret, user, host)
	out := map[string]any{"secret": secret, "otpauth_url": link}
	if code, err := qr.Encode([]byte(link)); err == nil {
		out["qr_svg"] = code.SVG(4)
	}
	writeJSON(w, http.StatusOK, out)
}

// POST /api/v1/auth/2fa/enable {code} → stores the pending secret once a
// code from it matches.
func (s *Server) twoFactorEnable(w http.ResponseWriter, r *http.Request) {
	user, ok := s.twoFactorUser(w, r)
	if !ok {
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	now := time.Now()
	secret, ok := s.Authn.totp.takePending(user, now)
	if !ok {
		writeError(w, http.StatusBadRequest, "no_setup", "start the setup again: no pending two-factor secret (they expire after 10 minutes)", nil)
		return
	}
	if !s.Authn.totp.guard.Use(user, secret, req.Code, now) {
		s.Authn.totp.setPending(user, secret, now) // let them try the next code
		writeError(w, http.StatusBadRequest, "invalid_code", "that code does not match; check the time on your phone and try the current code", nil)
		return
	}
	rev, err := s.saveAdminTOTP(r, user, secret, "two-factor login turned on for "+user)
	if err != nil {
		s.internal(w, "save two-factor secret", err)
		return
	}
	s.audit(r, "totp_enabled", user, map[string]any{"revision": rev})
	writeJSON(w, http.StatusOK, map[string]any{"status": "enabled"})
}

// POST /api/v1/auth/2fa/disable {password} → turns two-factor login off.
func (s *Server) twoFactorDisable(w http.ResponseWriter, r *http.Request) {
	user, ok := s.twoFactorUser(w, r)
	if !ok {
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	addr := clientAddr(r)
	if ok, wait := s.Authn.Limiter.Allowed(addr, user); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds()+1)))
		writeError(w, http.StatusTooManyRequests, "too_many_attempts", "too many failed attempts; try again later", nil)
		return
	}
	hash := s.Authn.dummyHash()
	if cfg, err := s.Store.Running(); err == nil {
		for _, a := range cfg.Admins {
			if a.Username == user && a.PasswordHash != "" {
				hash = a.PasswordHash
			}
		}
	}
	if auth.VerifyPassword(req.Password, hash) != nil {
		s.Authn.Limiter.Fail(addr, user)
		s.audit(r, "totp_disable_failed", user, nil)
		writeError(w, http.StatusForbidden, "wrong_password", "the password is wrong", nil)
		return
	}
	s.Authn.totp.dropPending(user)
	rev, err := s.saveAdminTOTP(r, user, "", "two-factor login turned off for "+user)
	if err != nil {
		s.internal(w, "save two-factor setting", err)
		return
	}
	s.audit(r, "totp_disabled", user, map[string]any{"revision": rev})
	writeJSON(w, http.StatusOK, map[string]any{"status": "disabled"})
}

// saveAdminTOTP sets a user's secret in the running config (a new revision,
// effective at once) and in the candidate, the way password changes do.
func (s *Server) saveAdminTOTP(r *http.Request, user, secret, comment string) (int, error) {
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	running, err := s.Store.Running()
	if err != nil {
		return 0, err
	}
	setTOTP(running, user, secret)
	cand, err := s.Store.Candidate()
	if err != nil {
		return 0, err
	}
	rev, err := s.Store.SaveRevision(running, s.user(r), comment)
	if err != nil {
		return 0, err
	}
	if setTOTP(cand, user, secret) {
		if err := s.Store.SetCandidate(cand); err != nil {
			s.Logger.Warn("2fa: could not update the candidate", "error", err)
		}
	}
	return rev, nil
}

func setTOTP(cfg *config.Config, user, secret string) bool {
	for i := range cfg.Admins {
		if cfg.Admins[i].Username == user {
			cfg.Admins[i].TOTPSecret = secret
			return true
		}
	}
	return false
}
