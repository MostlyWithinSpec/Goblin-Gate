// Package oidc is a small OpenID Connect relying party: discovery, the
// authorization code flow with PKCE, and ID token verification (RS256/384/512,
// PS256/384/512, ES256/384/512, EdDSA). Standard library only.
package oidc

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Provider talks to one identity provider. It caches the discovery document
// and the signing keys; a token signed with a key it doesn't know makes it
// fetch the keys again (key rotation), at most every MinRefresh.
type Provider struct {
	Issuer       string
	ClientID     string
	ClientSecret string // empty for a public client
	Scopes       []string
	// OptionalScopes are asked for only when the provider announces them in
	// scopes_supported (some refuse a login that asks for an unknown scope;
	// Keycloak has no "groups" scope, for one).
	OptionalScopes []string
	HTTP           *http.Client     // nil = a client with a 10 s timeout
	Now            func() time.Time // nil = time.Now
	MinRefresh     time.Duration    // 0 = 30 s
	CacheFor       time.Duration    // discovery and keys; 0 = 1 h

	mu     sync.Mutex
	disc   *Discovery
	discAt time.Time
	keys   map[string]crypto.PublicKey
	keyAt  time.Time
	tried  time.Time
}

// Discovery is the part of the provider's metadata the flow needs.
type Discovery struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	JWKSURI               string   `json:"jwks_uri"`
	SigningAlgs           []string `json:"id_token_signing_alg_values_supported"`
	ScopesSupported       []string `json:"scopes_supported"`
}

// Error kinds the caller may want to tell apart.
var (
	ErrDiscovery = errors.New("oidc: discovery failed")
	ErrExchange  = errors.New("oidc: code exchange failed")
	ErrToken     = errors.New("oidc: ID token invalid")
)

const maxBody = 1 << 20

func (p *Provider) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Provider) client() *http.Client {
	if p.HTTP != nil {
		return p.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func (p *Provider) cacheFor() time.Duration {
	if p.CacheFor > 0 {
		return p.CacheFor
	}
	return time.Hour
}

func (p *Provider) getJSON(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := p.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(out)
}

// Discover returns the provider's metadata (cached). The issuer in it must be
// exactly the configured one (OpenID Connect Discovery 1.0, section 4.3).
func (p *Provider) Discover(ctx context.Context) (*Discovery, error) {
	p.mu.Lock()
	if p.disc != nil && p.now().Sub(p.discAt) < p.cacheFor() {
		d := p.disc
		p.mu.Unlock()
		return d, nil
	}
	p.mu.Unlock()
	var d Discovery
	if err := p.getJSON(ctx, strings.TrimRight(p.Issuer, "/")+"/.well-known/openid-configuration", &d); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDiscovery, err)
	}
	if d.Issuer != p.Issuer && d.Issuer != strings.TrimRight(p.Issuer, "/") {
		return nil, fmt.Errorf("%w: the provider says its issuer is %q, not %q", ErrDiscovery, d.Issuer, p.Issuer)
	}
	for _, e := range []string{d.AuthorizationEndpoint, d.TokenEndpoint, d.JWKSURI} {
		if u, err := url.Parse(e); err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
			return nil, fmt.Errorf("%w: endpoint %q is not a URL", ErrDiscovery, e)
		}
	}
	p.mu.Lock()
	p.disc, p.discAt = &d, p.now()
	p.mu.Unlock()
	return &d, nil
}

// Random returns n random bytes, base64url without padding.
func Random(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// PKCE returns a code verifier and its S256 challenge (RFC 7636).
func PKCE() (verifier, challenge string) {
	verifier = Random(32)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

// AuthURL is where to send the browser to sign in.
func (p *Provider) AuthURL(ctx context.Context, redirect, state, nonce, challenge string) (string, error) {
	d, err := p.Discover(ctx)
	if err != nil {
		return "", err
	}
	scopes := append([]string{"openid"}, p.Scopes...)
	for _, sc := range p.OptionalScopes {
		if len(d.ScopesSupported) == 0 || contains(d.ScopesSupported, sc) {
			scopes = append(scopes, sc)
		}
	}
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {p.ClientID},
		"redirect_uri":          {redirect},
		"scope":                 {strings.Join(scopes, " ")},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	sep := "?"
	if strings.Contains(d.AuthorizationEndpoint, "?") {
		sep = "&"
	}
	return d.AuthorizationEndpoint + sep + q.Encode(), nil
}

// Exchange trades the authorization code for the raw ID token. A confidential
// client authenticates with HTTP Basic (client_secret_basic).
func (p *Provider) Exchange(ctx context.Context, code, redirect, verifier string) (string, error) {
	d, err := p.Discover(ctx)
	if err != nil {
		return "", err
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirect},
		"code_verifier": {verifier},
	}
	if p.ClientSecret == "" {
		form.Set("client_id", p.ClientID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrExchange, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if p.ClientSecret != "" {
		req.SetBasicAuth(url.QueryEscape(p.ClientID), url.QueryEscape(p.ClientSecret))
	}
	resp, err := p.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrExchange, err)
	}
	defer resp.Body.Close()
	var out struct {
		IDToken   string `json:"id_token"`
		Error     string `json:"error"`
		ErrorDesc string `json:"error_description"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&out); err != nil {
		return "", fmt.Errorf("%w: %s: %v", ErrExchange, resp.Status, err)
	}
	if resp.StatusCode != http.StatusOK || out.Error != "" {
		return "", fmt.Errorf("%w: %s %s %s", ErrExchange, resp.Status, out.Error, out.ErrorDesc)
	}
	if out.IDToken == "" {
		return "", fmt.Errorf("%w: no id_token in the answer (is the openid scope allowed?)", ErrExchange)
	}
	return out.IDToken, nil
}

// key returns the signing key with id kid ("" = the only key), fetching the
// key set again when it is unknown.
func (p *Provider) key(ctx context.Context, kid string) (crypto.PublicKey, error) {
	pick := func() (crypto.PublicKey, bool) {
		if kid == "" && len(p.keys) == 1 {
			for _, k := range p.keys {
				return k, true
			}
		}
		k, ok := p.keys[kid]
		return k, ok
	}
	p.mu.Lock()
	fresh := p.keys != nil && p.now().Sub(p.keyAt) < p.cacheFor()
	if k, ok := pick(); ok && fresh {
		p.mu.Unlock()
		return k, nil
	}
	minRefresh := p.MinRefresh
	if minRefresh == 0 {
		minRefresh = 30 * time.Second
	}
	if fresh {
		// an unknown key in a fresh set: maybe the provider rotated, but
		// don't let made-up key ids make us fetch on every request
		if p.now().Sub(p.tried) < minRefresh {
			p.mu.Unlock()
			return nil, fmt.Errorf("%w: unknown signing key %q", ErrToken, kid)
		}
		p.tried = p.now()
	}
	p.mu.Unlock()

	d, err := p.Discover(ctx)
	if err != nil {
		return nil, err
	}
	var set struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := p.getJSON(ctx, d.JWKSURI, &set); err != nil {
		return nil, fmt.Errorf("%w: signing keys: %v", ErrToken, err)
	}
	keys := map[string]crypto.PublicKey{}
	for _, raw := range set.Keys {
		id, k, err := parseJWK(raw)
		if err == nil && k != nil {
			keys[id] = k
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.keys, p.keyAt = keys, p.now()
	if k, ok := pick(); ok {
		return k, nil
	}
	return nil, fmt.Errorf("%w: unknown signing key %q", ErrToken, kid)
}

// Claims are an ID token's claims.
type Claims map[string]any

// String returns a string claim, or "".
func (c Claims) String(name string) string {
	s, _ := c[name].(string)
	return s
}

// Strings returns a claim that is a list of strings, or one string as a
// list of one.
func (c Claims) Strings(name string) []string {
	switch v := c[name].(type) {
	case string:
		return []string{v}
	case []any:
		out := []string{}
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// skew is the clock difference tolerated for exp and iat.
const skew = time.Minute

// Verify checks the ID token's signature and claims (iss, aud, azp, exp,
// iat, nonce) and returns its claims.
func (p *Provider) Verify(ctx context.Context, raw, nonce string) (Claims, error) {
	d, err := p.Discover(ctx)
	if err != nil {
		return nil, err
	}
	hdr, claims, signed, sig, err := splitJWT(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrToken, err)
	}
	if len(d.SigningAlgs) > 0 && !contains(d.SigningAlgs, hdr.Alg) {
		return nil, fmt.Errorf("%w: algorithm %q is not one the provider announces", ErrToken, hdr.Alg)
	}
	key, err := p.key(ctx, hdr.Kid)
	if err != nil {
		return nil, err
	}
	if err := verifySignature(hdr.Alg, key, signed, sig); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrToken, err)
	}
	var c Claims
	dec := json.NewDecoder(strings.NewReader(string(claims)))
	dec.UseNumber()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%w: claims: %v", ErrToken, err)
	}
	if c.String("iss") != d.Issuer {
		return nil, fmt.Errorf("%w: issued by %q, not %q", ErrToken, c.String("iss"), d.Issuer)
	}
	aud := c.Strings("aud")
	if !contains(aud, p.ClientID) {
		return nil, fmt.Errorf("%w: not issued for this client", ErrToken)
	}
	if azp := c.String("azp"); (len(aud) > 1 || azp != "") && azp != p.ClientID {
		return nil, fmt.Errorf("%w: authorized party is %q", ErrToken, azp)
	}
	now := p.now()
	exp, ok := numericDate(c["exp"])
	if !ok || !now.Before(exp.Add(skew)) {
		return nil, fmt.Errorf("%w: expired", ErrToken)
	}
	if iat, ok := numericDate(c["iat"]); ok && iat.After(now.Add(skew)) {
		return nil, fmt.Errorf("%w: issued in the future (check the clocks)", ErrToken)
	}
	if nonce == "" || c.String("nonce") != nonce {
		return nil, fmt.Errorf("%w: nonce does not match", ErrToken)
	}
	return c, nil
}

func numericDate(v any) (time.Time, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return time.Time{}, false
	}
	f, err := n.Float64()
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(int64(f), 0), true
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
