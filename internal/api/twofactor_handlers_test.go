package api

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/auth"
)

func code(t *testing.T, secret string, offset time.Duration) string {
	t.Helper()
	c, err := auth.TOTPCode(secret, time.Now().Add(offset))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestTwoFactorLogin(t *testing.T) {
	a := newAuthAPI(t)
	b := a.browser()
	b.login("admin")
	if me := b.expect("GET", "/api/v1/auth/me", nil, 200); me["totp_enabled"] != false || me["user"] != "admin" {
		t.Fatalf("me before: %v", me)
	}
	b.expect("POST", "/api/v1/auth/2fa/enable", map[string]string{"code": "123456"}, 400) // no setup yet

	setup := b.expect("POST", "/api/v1/auth/2fa/setup", nil, 200)
	secret, _ := setup["secret"].(string)
	if !auth.ValidTOTPSecret(secret) || !strings.HasPrefix(setup["otpauth_url"].(string), "otpauth://totp/") || !strings.Contains(setup["qr_svg"].(string), "<svg") {
		t.Fatalf("setup: %v", setup)
	}
	if out := b.expect("POST", "/api/v1/auth/2fa/enable", map[string]string{"code": "000000"}, 400); errCode(out) != "invalid_code" {
		t.Fatalf("wrong code: %v", out)
	}
	b.expect("POST", "/api/v1/auth/2fa/enable", map[string]string{"code": code(t, secret, 0)}, 200) // still pending after a miss
	if me := b.expect("GET", "/api/v1/auth/me", nil, 200); me["totp_enabled"] != true {
		t.Fatalf("me after: %v", me)
	}
	admins := b.expect("GET", "/api/v1/admins", nil, 200)
	if !strings.Contains(jsonString(admins), `"totp_enabled":true`) || strings.Contains(jsonString(admins), secret) {
		t.Fatalf("admins: %v", admins)
	}
	run, _ := a.store.Running()
	cand, _ := a.store.Candidate()
	if adminTOTP(run, "admin") != secret || adminTOTP(cand, "admin") != secret {
		t.Fatal("secret not stored in running and candidate")
	}

	// login now needs the code
	c := a.browser()
	if out := c.expect("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": testPassword}, 401); errCode(out) != "totp_required" {
		t.Fatalf("no code: %v", out)
	}
	if out := c.expect("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": testPassword, "code": "000000"}, 401); errCode(out) != "totp_invalid" {
		t.Fatalf("bad code: %v", out)
	}
	// a wrong password with a right code is still a wrong password
	if out := c.expect("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "nope", "code": code(t, secret, 30*time.Second)}, 401); errCode(out) != "login_failed" {
		t.Fatalf("bad password: %v", out)
	}
	next := code(t, secret, 30*time.Second) // the enable code's step is used up
	c.expect("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": testPassword, "code": next}, 200)
	if out := a.browser().expect("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": testPassword, "code": next}, 401); errCode(out) != "totp_invalid" {
		t.Fatalf("replayed code: %v", out)
	}
	// the other user is unaffected
	a.browser().login("viewer")

	// read-only users never see the secret
	v := a.browser()
	v.login("viewer")
	if strings.Contains(jsonString(v.expect("GET", "/api/v1/config/running", nil, 200)), secret) {
		t.Fatal("readonly sees the two-factor secret")
	}

	if out := b.expect("POST", "/api/v1/auth/2fa/disable", map[string]string{"password": "nope"}, 403); errCode(out) != "wrong_password" {
		t.Fatalf("disable wrong password: %v", out)
	}
	b.expect("POST", "/api/v1/auth/2fa/disable", map[string]string{"password": testPassword}, 200)
	a.browser().login("admin")
}

func TestTwoFactorSocketRecovery(t *testing.T) {
	a := newAuthAPI(t)
	b := a.browser()
	b.login("admin")
	secret := b.expect("POST", "/api/v1/auth/2fa/setup", nil, 200)["secret"].(string)
	b.expect("POST", "/api/v1/auth/2fa/enable", map[string]string{"code": code(t, secret, 0)}, 200)

	s := &Server{Store: a.store, Authn: a.authn, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	req := httptest.NewRequest("POST", "/api/v1/auth/password", strings.NewReader(`{"user":"admin","new_password":"recovered password"}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(WithSocketConn(context.Background(), (net.Conn)(nil)))
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("socket reset: %d %s", rec.Code, rec.Body)
	}
	// lost phone: the console reset also turned two-factor off
	a.browser().expect("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "recovered password"}, 200)
}

func TestTwoFactorNotForSocketOrReadonlyToken(t *testing.T) {
	a := newAuthAPI(t)
	s := &Server{Store: a.store, Authn: a.authn, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	req := httptest.NewRequest("POST", "/api/v1/auth/2fa/setup", nil)
	req = req.WithContext(WithSocketConn(context.Background(), (net.Conn)(nil)))
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("socket setup: %d %s", rec.Code, rec.Body)
	}
	// read-only users can set up 2FA for themselves (it protects their login)
	v := a.browser()
	v.login("viewer")
	v.expect("POST", "/api/v1/auth/2fa/setup", nil, 200)
}
