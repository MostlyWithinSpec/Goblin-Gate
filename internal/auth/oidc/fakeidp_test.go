package oidc

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
)

// fakeIdP is an OpenID provider for tests: discovery, JWKS, and a token
// endpoint that answers with whatever ID token the test queued.
type fakeIdP struct {
	srv       *httptest.Server
	mu        sync.Mutex
	keys      map[string]crypto.Signer
	jwksHits  int
	nextToken string
	lastForm  url.Values
	lastUser  string
	lastPass  string
	issuer    string   // override of the discovery issuer
	scopes    []string // scopes_supported, if any
}

func newFakeIdP(t *testing.T) *fakeIdP {
	f := &fakeIdP{keys: map[string]crypto.Signer{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		iss := f.srv.URL
		if f.issuer != "" {
			iss = f.issuer
		}
		d := map[string]any{
			"issuer": iss, "authorization_endpoint": f.srv.URL + "/authorize",
			"token_endpoint": f.srv.URL + "/token", "jwks_uri": f.srv.URL + "/jwks",
		}
		if f.scopes != nil {
			d["scopes_supported"] = f.scopes
		}
		json.NewEncoder(w).Encode(d)
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.jwksHits++
		keys := []any{}
		for kid, k := range f.keys {
			keys = append(keys, jwk(kid, k.Public()))
		}
		json.NewEncoder(w).Encode(map[string]any{"keys": keys})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		f.mu.Lock()
		f.lastForm = r.PostForm
		f.lastUser, f.lastPass, _ = r.BasicAuth()
		tok := f.nextToken
		f.mu.Unlock()
		if r.PostForm.Get("code") != "good-code" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"id_token": tok, "access_token": "x", "token_type": "Bearer"})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIdP) addKey(kid string, k crypto.Signer) {
	f.mu.Lock()
	f.keys[kid] = k
	f.mu.Unlock()
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func jwk(kid string, pub crypto.PublicKey) map[string]string {
	switch k := pub.(type) {
	case *rsa.PublicKey:
		return map[string]string{"kty": "RSA", "kid": kid, "use": "sig", "n": b64(k.N.Bytes()), "e": b64(big.NewInt(int64(k.E)).Bytes())}
	case *ecdsa.PublicKey:
		size := (k.Curve.Params().BitSize + 7) / 8
		return map[string]string{"kty": "EC", "kid": kid, "crv": k.Curve.Params().Name, "x": b64(k.X.FillBytes(make([]byte, size))), "y": b64(k.Y.FillBytes(make([]byte, size)))}
	case ed25519.PublicKey:
		return map[string]string{"kty": "OKP", "kid": kid, "crv": "Ed25519", "x": b64(k)}
	}
	panic("key type")
}

// sign makes a compact JWS with alg over claims.
func sign(t *testing.T, alg, kid string, key crypto.Signer, claims map[string]any) string {
	t.Helper()
	h, _ := json.Marshal(map[string]string{"alg": alg, "kid": kid, "typ": "JWT"})
	c, _ := json.Marshal(claims)
	signed := b64(h) + "." + b64(c)
	sum := sha256.Sum256([]byte(signed))
	var sig []byte
	var err error
	switch k := key.(type) {
	case *rsa.PrivateKey:
		if alg == "PS256" {
			sig, err = rsa.SignPSS(rand.Reader, k, crypto.SHA256, sum[:], &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
		} else {
			sig, err = rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, sum[:])
		}
	case *ecdsa.PrivateKey:
		var r, s *big.Int
		r, s, err = ecdsa.Sign(rand.Reader, k, sum[:])
		sig = append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...)
	case ed25519.PrivateKey:
		sig = ed25519.Sign(k, []byte(signed))
	}
	if err != nil {
		t.Fatal(err)
	}
	return signed + "." + b64(sig)
}

func rsaKey(t *testing.T) *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func ecKey(t *testing.T) *ecdsa.PrivateKey {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func edKey(t *testing.T) ed25519.PrivateKey {
	_, k, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}
