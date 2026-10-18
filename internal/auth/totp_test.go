package auth

import (
	"encoding/base32"
	"strings"
	"testing"
	"time"
)

// RFC 6238 appendix B, SHA-1 vectors (8 digits there; the last 6 here).
func TestTOTPVectors(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	for unix, want8 := range map[int64]string{
		59: "94287082", 1111111109: "07081804", 1111111111: "14050471", 1234567890: "89005924", 2000000000: "69279037",
	} {
		got, err := TOTPCode(secret, time.Unix(unix, 0))
		if err != nil || got != want8[2:] {
			t.Errorf("T=%d: %q %v, want %q", unix, got, err, want8[2:])
		}
	}
}

func TestMatchTOTPWindow(t *testing.T) {
	secret := NewTOTPSecret()
	if !ValidTOTPSecret(secret) || len(secret) != 32 {
		t.Fatalf("secret %q", secret)
	}
	now := time.Unix(1_800_000_000, 0)
	for _, d := range []time.Duration{-30 * time.Second, 0, 30 * time.Second} {
		code, _ := TOTPCode(secret, now.Add(d))
		if _, ok := MatchTOTP(secret, code, now); !ok {
			t.Errorf("offset %v not accepted", d)
		}
	}
	code, _ := TOTPCode(secret, now.Add(-90*time.Second))
	if _, ok := MatchTOTP(secret, code, now); ok {
		t.Error("a code three steps old was accepted")
	}
	for _, bad := range []string{"", "12345", "1234567", "abcdef"} {
		if _, ok := MatchTOTP(secret, bad, now); ok {
			t.Errorf("%q accepted", bad)
		}
	}
	if ValidTOTPSecret("not base32!") || ValidTOTPSecret("MZXW6") {
		t.Error("bad secrets pass ValidTOTPSecret")
	}
}

func TestTOTPGuardNoReuse(t *testing.T) {
	var g TOTPGuard
	secret := NewTOTPSecret()
	now := time.Unix(1_800_000_000, 0)
	code, _ := TOTPCode(secret, now)
	if !g.Use("admin", secret, code, now) {
		t.Fatal("first use refused")
	}
	if g.Use("admin", secret, code, now.Add(5*time.Second)) {
		t.Fatal("the same code was accepted twice")
	}
	prev, _ := TOTPCode(secret, now.Add(-30*time.Second))
	if g.Use("admin", secret, prev, now) {
		t.Fatal("an older step was accepted after a newer one")
	}
	next, _ := TOTPCode(secret, now.Add(30*time.Second))
	if !g.Use("admin", secret, next, now.Add(30*time.Second)) {
		t.Fatal("the next code was refused")
	}
	if !g.Use("other", secret, code, now) {
		t.Fatal("users share the replay window")
	}
}

func TestTOTPURL(t *testing.T) {
	u := TOTPURL("JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP", "admin", "goblin-gate")
	if !strings.HasPrefix(u, "otpauth://totp/Goblin%20Gate:admin@goblin-gate?") || !strings.Contains(u, "secret=JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP") || !strings.Contains(u, "issuer=Goblin+Gate") {
		t.Fatal(u)
	}
}
