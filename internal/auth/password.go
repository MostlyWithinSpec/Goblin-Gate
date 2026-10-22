// Package auth holds management-plane authentication helpers. Task 022 added
// password hashing for the first-boot wizard; task 007 adds sessions and the
// API middleware on top of it.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Password length limits, in bytes. The upper bound keeps hashing cheap to
// call with hostile input.
const (
	MinPasswordLen = 10
	MaxPasswordLen = 256
)

// argon2id parameters (OWASP minimum: 19 MiB, 2 passes, 1 lane). Stored in
// each hash, so they can be raised later without breaking old hashes.
const (
	argonTime    = 2
	argonMemory  = 19 * 1024 // KiB
	argonThreads = 1
	argonKeyLen  = 32
	argonSaltLen = 16

	maxArgonMemory  = 64 * 1024 // KiB
	maxArgonTime    = 8
	maxArgonThreads = 8
)

// ErrMismatch is returned by VerifyPassword for a wrong password.
var ErrMismatch = errors.New("auth: password does not match")

var b64 = base64.RawStdEncoding

// CheckPassword reports why a new password is unacceptable, or nil.
func CheckPassword(pw string) error {
	switch {
	case len(pw) < MinPasswordLen:
		return fmt.Errorf("password must be at least %d characters", MinPasswordLen)
	case len(pw) > MaxPasswordLen:
		return fmt.Errorf("password must be at most %d characters", MaxPasswordLen)
	}
	return nil
}

// HashPassword returns an argon2id hash in PHC string form:
// $argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash> (unpadded base64).
func HashPassword(pw string) (string, error) {
	if err := CheckPassword(pw); err != nil {
		return "", err
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: read salt: %w", err)
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword checks pw against a hash from HashPassword. It returns
// ErrMismatch for a wrong password and another error for a malformed hash.
func VerifyPassword(pw, hash string) error {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return errors.New("auth: not an argon2id hash")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return errors.New("auth: unsupported argon2 version")
	}
	var mem, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &time, &threads); err != nil {
		return errors.New("auth: bad argon2 parameters")
	}
	// Stored hashes come only from admins, but a login must not cost
	// whatever a hash asks for: at most 64 MiB, 8 passes, 8 lanes (new
	// hashes use 19 MiB, 2 passes, 1 lane).
	if mem == 0 || mem > maxArgonMemory || time == 0 || time > maxArgonTime || threads == 0 || threads > maxArgonThreads {
		return errors.New("auth: argon2 parameters out of range")
	}
	salt, err := b64.DecodeString(parts[4])
	if err != nil || len(salt) < 8 {
		return errors.New("auth: bad salt")
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) < 16 || len(want) > 64 {
		return errors.New("auth: bad hash")
	}
	if len(pw) > MaxPasswordLen {
		return ErrMismatch
	}
	got := argon2.IDKey([]byte(pw), salt, time, mem, threads, uint32(len(want)))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrMismatch
	}
	return nil
}
