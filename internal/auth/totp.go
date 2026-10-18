package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Time-based one-time passwords (RFC 6238) as authenticator apps make them:
// HMAC-SHA1, 6 digits, 30-second steps.
const (
	TOTPDigits = 6
	TOTPPeriod = 30 * time.Second
	// TOTPSkew is how many steps before and after now are accepted, for
	// clocks that are a little off.
	TOTPSkew = 1
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns a random 160-bit secret, base32 without padding
// (32 characters), the form authenticator apps take.
func NewTOTPSecret() string {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		panic("auth: crypto/rand failed: " + err.Error())
	}
	return b32.EncodeToString(b)
}

// ValidTOTPSecret reports whether s is a secret NewTOTPSecret could have made.
func ValidTOTPSecret(s string) bool {
	b, err := b32.DecodeString(s)
	return err == nil && len(b) == 20
}

// TOTPCode returns the code for the step that contains t.
func TOTPCode(secret string, t time.Time) (string, error) {
	return totpAt(secret, step(t))
}

func step(t time.Time) int64 { return t.Unix() / int64(TOTPPeriod/time.Second) }

func totpAt(secret string, counter int64) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(secret))
	if err != nil || len(key) == 0 {
		return "", fmt.Errorf("auth: bad TOTP secret")
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(counter))
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", v%1_000_000), nil
}

// MatchTOTP reports which step within ±TOTPSkew of now code belongs to.
func MatchTOTP(secret, code string, now time.Time) (int64, bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != TOTPDigits {
		return 0, false
	}
	cur := step(now)
	for d := int64(-TOTPSkew); d <= TOTPSkew; d++ {
		want, err := totpAt(secret, cur+d)
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return cur + d, true
		}
	}
	return 0, false
}

// TOTPURL is the otpauth:// link an authenticator app reads from a QR code.
func TOTPURL(secret, user, host string) string {
	label := url.PathEscape("Goblin Gate:" + user + "@" + host)
	q := url.Values{"secret": {secret}, "issuer": {"Goblin Gate"}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// TOTPGuard remembers the last step each user logged in with, so a code
// that was seen (shoulder-surfed, phished in real time) cannot be used a
// second time.
type TOTPGuard struct {
	mu   sync.Mutex
	last map[string]int64
}

// Use accepts code for user when it matches and its step is newer than the
// last one used.
func (g *TOTPGuard) Use(user, secret, code string, now time.Time) bool {
	st, ok := MatchTOTP(secret, code, now)
	if !ok {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.last == nil {
		g.last = map[string]int64{}
	}
	if prev, seen := g.last[user]; seen && st <= prev {
		return false
	}
	g.last[user] = st
	return true
}
