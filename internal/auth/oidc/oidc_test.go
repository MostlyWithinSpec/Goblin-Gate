package oidc

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

var ctx = context.Background()

func provider(f *fakeIdP, now time.Time) *Provider {
	return &Provider{Issuer: f.srv.URL, ClientID: "gate", ClientSecret: "s3cret", Scopes: []string{"profile", "groups"},
		HTTP: f.srv.Client(), Now: func() time.Time { return now }}
}

func baseClaims(f *fakeIdP, now time.Time) map[string]any {
	return map[string]any{"iss": f.srv.URL, "aud": "gate", "sub": "u1", "exp": now.Add(5 * time.Minute).Unix(),
		"iat": now.Unix(), "nonce": "n1", "preferred_username": "alice", "groups": []string{"admins", "lan"}}
}

func TestFlow(t *testing.T) {
	f := newFakeIdP(t)
	now := time.Now()
	key := rsaKey(t)
	f.addKey("k1", key)
	p := provider(f, now)

	verifier, challenge := PKCE()
	u, err := p.AuthURL(ctx, "https://gate.lan/api/v1/auth/sso/callback", "st", "n1", challenge)
	if err != nil {
		t.Fatal(err)
	}
	q, _ := url.Parse(u)
	want := url.Values{"response_type": {"code"}, "client_id": {"gate"}, "redirect_uri": {"https://gate.lan/api/v1/auth/sso/callback"},
		"scope": {"openid profile groups"}, "state": {"st"}, "nonce": {"n1"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	if q.Path != "/authorize" || q.Query().Encode() != want.Encode() {
		t.Fatalf("auth URL %s", u)
	}

	f.nextToken = sign(t, "RS256", "k1", key, baseClaims(f, now))
	raw, err := p.Exchange(ctx, "good-code", "https://gate.lan/api/v1/auth/sso/callback", verifier)
	if err != nil {
		t.Fatal(err)
	}
	if f.lastForm.Get("code_verifier") != verifier || f.lastUser != "gate" || f.lastPass != "s3cret" || f.lastForm.Get("client_id") != "" {
		t.Fatalf("token request %v %s:%s", f.lastForm, f.lastUser, f.lastPass)
	}
	c, err := p.Verify(ctx, raw, "n1")
	if err != nil {
		t.Fatal(err)
	}
	if c.String("preferred_username") != "alice" || strings.Join(c.Strings("groups"), ",") != "admins,lan" {
		t.Fatalf("claims %v", c)
	}

	if _, err := p.Exchange(ctx, "bad-code", "https://gate.lan/x", verifier); !errors.Is(err, ErrExchange) {
		t.Fatalf("bad code: %v", err)
	}
	// a public client sends its id in the form, no basic auth
	p.ClientSecret = ""
	if _, err := p.Exchange(ctx, "good-code", "https://gate.lan/x", verifier); err != nil || f.lastForm.Get("client_id") != "gate" || f.lastUser != "" {
		t.Fatalf("public client: %v %v", err, f.lastForm)
	}
}

func TestVerifyAlgorithms(t *testing.T) {
	f := newFakeIdP(t)
	now := time.Now()
	rk, ek, dk := rsaKey(t), ecKey(t), edKey(t)
	f.addKey("rsa", rk)
	f.addKey("ec", ek)
	f.addKey("ed", dk)
	p := provider(f, now)
	for _, c := range []struct {
		alg, kid string
		key      crypto.Signer
	}{{"RS256", "rsa", rk}, {"PS256", "rsa", rk}, {"ES256", "ec", ek}, {"EdDSA", "ed", dk}} {
		tok := sign(t, c.alg, c.kid, c.key, baseClaims(f, now))
		if _, err := p.Verify(ctx, tok, "n1"); err != nil {
			t.Errorf("%s: %v", c.alg, err)
		}
	}
}

func TestVerifyRejects(t *testing.T) {
	f := newFakeIdP(t)
	now := time.Now()
	key, other := rsaKey(t), rsaKey(t)
	f.addKey("k1", key)
	p := provider(f, now)
	good := baseClaims(f, now)
	with := func(k string, v any) map[string]any {
		c := map[string]any{}
		for a, b := range good {
			c[a] = b
		}
		if v == nil {
			delete(c, k)
		} else {
			c[k] = v
		}
		return c
	}
	hs := func() string { // HS256 "signed" with the public modulus: the classic key confusion
		h, _ := json.Marshal(map[string]string{"alg": "HS256", "kid": "k1"})
		c, _ := json.Marshal(good)
		s := b64(h) + "." + b64(c)
		m := hmac.New(sha256.New, key.N.Bytes())
		m.Write([]byte(s))
		return s + "." + b64(m.Sum(nil))
	}
	none := func() string {
		h, _ := json.Marshal(map[string]string{"alg": "none"})
		c, _ := json.Marshal(good)
		return b64(h) + "." + b64(c) + "."
	}
	tampered := func() string {
		tok := sign(t, "RS256", "k1", key, good)
		parts := strings.Split(tok, ".")
		c, _ := json.Marshal(with("preferred_username", "root"))
		return parts[0] + "." + b64(c) + "." + parts[2]
	}
	for name, tok := range map[string]string{
		"wrong nonce":     sign(t, "RS256", "k1", key, with("nonce", "other")),
		"no nonce":        sign(t, "RS256", "k1", key, with("nonce", nil)),
		"wrong audience":  sign(t, "RS256", "k1", key, with("aud", "someone-else")),
		"azp mismatch":    sign(t, "RS256", "k1", key, with("aud", []string{"gate", "api"})),
		"expired":         sign(t, "RS256", "k1", key, with("exp", now.Add(-2*time.Minute).Unix())),
		"no exp":          sign(t, "RS256", "k1", key, with("exp", nil)),
		"future iat":      sign(t, "RS256", "k1", key, with("iat", now.Add(10*time.Minute).Unix())),
		"wrong issuer":    sign(t, "RS256", "k1", key, with("iss", "https://evil.example")),
		"other key":       sign(t, "RS256", "k1", other, good),
		"unknown kid":     sign(t, "RS256", "k9", other, good),
		"alg none":        none(),
		"HS256 confusion": hs(),
		"tampered":        tampered(),
		"garbage":         "a.b",
	} {
		if _, err := p.Verify(ctx, tok, "n1"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// within the clock skew is fine; several audiences with the right azp too
	if _, err := p.Verify(ctx, sign(t, "RS256", "k1", key, with("exp", now.Add(-30*time.Second).Unix())), "n1"); err != nil {
		t.Errorf("skew: %v", err)
	}
	c := with("aud", []string{"gate", "api"})
	c["azp"] = "gate"
	if _, err := p.Verify(ctx, sign(t, "RS256", "k1", key, c), "n1"); err != nil {
		t.Errorf("azp: %v", err)
	}
}

func TestKeyRotation(t *testing.T) {
	f := newFakeIdP(t)
	now := time.Now()
	k1, k2 := rsaKey(t), rsaKey(t)
	f.addKey("k1", k1)
	p := provider(f, now)
	if _, err := p.Verify(ctx, sign(t, "RS256", "k1", k1, baseClaims(f, now)), "n1"); err != nil {
		t.Fatal(err)
	}
	f.addKey("k2", k2) // the provider rotates
	if _, err := p.Verify(ctx, sign(t, "RS256", "k2", k2, baseClaims(f, now)), "n1"); err != nil {
		t.Fatalf("rotated key: %v", err)
	}
	hits := f.jwksHits
	// an unknown key id right after a fetch doesn't hammer the provider
	for range 5 {
		p.Verify(ctx, sign(t, "RS256", "k9", k2, baseClaims(f, now)), "n1")
	}
	if f.jwksHits != hits {
		t.Fatalf("JWKS fetched %d times for unknown keys", f.jwksHits-hits)
	}
}

func TestDiscoveryIssuerMismatch(t *testing.T) {
	f := newFakeIdP(t)
	f.issuer = "https://evil.example"
	p := provider(f, time.Now())
	if _, err := p.Discover(ctx); !errors.Is(err, ErrDiscovery) {
		t.Fatalf("got %v", err)
	}
}

func TestOptionalScopes(t *testing.T) {
	f := newFakeIdP(t)
	f.scopes = []string{"openid", "profile", "email"} // no groups, like Keycloak
	p := &Provider{Issuer: f.srv.URL, ClientID: "gate", Scopes: []string{"offline_access"},
		OptionalScopes: []string{"profile", "email", "groups"}, HTTP: f.srv.Client()}
	u, err := p.AuthURL(ctx, "https://gate/cb", "s", "n", "c")
	if err != nil {
		t.Fatal(err)
	}
	q, _ := url.Parse(u)
	// set scopes always go; optional ones only when announced
	if got := q.Query().Get("scope"); got != "openid offline_access profile email" {
		t.Fatalf("scope %q", got)
	}
}
