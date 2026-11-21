package oidc

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"math/big"
	"strings"
)

type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

// splitJWT decodes a compact JWS: header, payload, the signed part and the
// signature.
func splitJWT(raw string) (jwtHeader, []byte, []byte, []byte, error) {
	var h jwtHeader
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return h, nil, nil, nil, errors.New("not a signed JWT")
	}
	hb, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return h, nil, nil, nil, errors.New("header is not base64url")
	}
	if err := json.Unmarshal(hb, &h); err != nil {
		return h, nil, nil, nil, errors.New("header is not JSON")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return h, nil, nil, nil, errors.New("payload is not base64url")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return h, nil, nil, nil, errors.New("signature is not base64url")
	}
	return h, payload, []byte(parts[0] + "." + parts[1]), sig, nil
}

func digest(h func() hash.Hash, data []byte) []byte {
	d := h()
	d.Write(data)
	return d.Sum(nil)
}

// verifySignature checks sig over signed with key for alg. "none" and the
// HMAC algorithms are refused: an ID token is checked with the provider's
// public key only.
func verifySignature(alg string, key crypto.PublicKey, signed, sig []byte) error {
	var (
		hf  func() hash.Hash
		hid crypto.Hash
	)
	switch alg[len(alg)-min(3, len(alg)):] {
	case "256":
		hf, hid = sha256.New, crypto.SHA256
	case "384":
		hf, hid = sha512.New384, crypto.SHA384
	case "512":
		hf, hid = sha512.New, crypto.SHA512
	}
	switch {
	case alg == "EdDSA":
		k, ok := key.(ed25519.PublicKey)
		if !ok || !ed25519.Verify(k, signed, sig) {
			return errors.New("bad signature")
		}
		return nil
	case hf == nil:
		return fmt.Errorf("algorithm %q is not supported", alg)
	case strings.HasPrefix(alg, "RS"):
		k, ok := key.(*rsa.PublicKey)
		if !ok || rsa.VerifyPKCS1v15(k, hid, digest(hf, signed), sig) != nil {
			return errors.New("bad signature")
		}
		return nil
	case strings.HasPrefix(alg, "PS"):
		k, ok := key.(*rsa.PublicKey)
		if !ok || rsa.VerifyPSS(k, hid, digest(hf, signed), sig, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash}) != nil {
			return errors.New("bad signature")
		}
		return nil
	case strings.HasPrefix(alg, "ES"):
		k, ok := key.(*ecdsa.PublicKey)
		if !ok {
			return errors.New("bad signature")
		}
		size := (k.Curve.Params().BitSize + 7) / 8
		want := map[string]int{"ES256": 32, "ES384": 48, "ES512": 66}[alg]
		if size != want || len(sig) != 2*size {
			return errors.New("bad signature")
		}
		r, s := new(big.Int).SetBytes(sig[:size]), new(big.Int).SetBytes(sig[size:])
		if !ecdsa.Verify(k, digest(hf, signed), r, s) {
			return errors.New("bad signature")
		}
		return nil
	}
	return fmt.Errorf("algorithm %q is not supported", alg)
}

// parseJWK reads one JSON Web Key (RFC 7517/7518/8037): RSA, EC P-256/384/521
// and OKP Ed25519 signing keys. Other keys return (id, nil, nil).
func parseJWK(raw []byte) (string, crypto.PublicKey, error) {
	var k struct {
		Kty, Kid, Use, Crv, N, E, X, Y string
	}
	if err := json.Unmarshal(raw, &k); err != nil {
		return "", nil, err
	}
	if k.Use != "" && k.Use != "sig" {
		return k.Kid, nil, nil
	}
	b := func(s string) *big.Int {
		v, err := base64.RawURLEncoding.DecodeString(s)
		if err != nil || len(v) == 0 {
			return nil
		}
		return new(big.Int).SetBytes(v)
	}
	switch k.Kty {
	case "RSA":
		n, e := b(k.N), b(k.E)
		if n == nil || e == nil || !e.IsInt64() || n.BitLen() < 2048 {
			return k.Kid, nil, errors.New("bad RSA key (needs 2048 bits or more)")
		}
		return k.Kid, &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
	case "EC":
		curve := map[string]elliptic.Curve{"P-256": elliptic.P256(), "P-384": elliptic.P384(), "P-521": elliptic.P521()}[k.Crv]
		x, y := b(k.X), b(k.Y)
		if curve == nil || x == nil || y == nil {
			return k.Kid, nil, errors.New("bad EC key")
		}
		pub := &ecdsa.PublicKey{Curve: curve, X: x, Y: y}
		if _, err := pub.ECDH(); err != nil { // rejects points not on the curve
			return k.Kid, nil, errors.New("EC point not on the curve")
		}
		return k.Kid, pub, nil
	case "OKP":
		x, err := base64.RawURLEncoding.DecodeString(k.X)
		if k.Crv != "Ed25519" || err != nil || len(x) != ed25519.PublicKeySize {
			return k.Kid, nil, errors.New("bad OKP key")
		}
		return k.Kid, ed25519.PublicKey(x), nil
	}
	return k.Kid, nil, nil
}
