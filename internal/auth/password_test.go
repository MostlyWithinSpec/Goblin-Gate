package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("unexpected hash format %q", h)
	}
	if err := VerifyPassword("correct horse battery", h); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := VerifyPassword("wrong horse battery", h); !errors.Is(err, ErrMismatch) {
		t.Fatalf("wrong password: %v", err)
	}
	h2, _ := HashPassword("correct horse battery")
	if h == h2 {
		t.Fatal("two hashes of the same password must differ (salt)")
	}
}

func TestCheckPassword(t *testing.T) {
	if CheckPassword("short") == nil {
		t.Fatal("short password accepted")
	}
	if CheckPassword(strings.Repeat("x", MaxPasswordLen+1)) == nil {
		t.Fatal("long password accepted")
	}
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("HashPassword accepted a short password")
	}
}

func TestVerifyMalformed(t *testing.T) {
	for _, h := range []string{
		"", "plain", "$argon2i$v=19$m=19456,t=2,p=1$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=18$m=19456,t=2,p=1$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=19$m=999999999,t=2,p=1$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"$argon2id$v=19$m=19456,t=2,p=1$!!$aGFzaGhhc2hoYXNoaGFzaA",
	} {
		if err := VerifyPassword("whatever-password", h); err == nil || errors.Is(err, ErrMismatch) {
			t.Errorf("hash %q: got %v, want a format error", h, err)
		}
	}
}

// A stored hash can't make a login arbitrarily expensive.
func TestVerifyRefusesCostlyParameters(t *testing.T) {
	for _, params := range []string{"m=1048576,t=2,p=1", "m=19456,t=16,p=1", "m=19456,t=2,p=64"} {
		hash := "$argon2id$v=19$" + params + "$c2FsdHNhbHRzYWx0$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaGhhc2g"
		if err := VerifyPassword("whatever-password", hash); err == nil || err == ErrMismatch {
			t.Errorf("%s: %v, want out of range", params, err)
		}
	}
}
