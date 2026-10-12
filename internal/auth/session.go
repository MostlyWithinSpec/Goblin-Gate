package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"sync"
	"time"
)

// Session timeouts.
const (
	IdleTimeout     = 30 * time.Minute
	AbsoluteTimeout = 12 * time.Hour
)

// Identity is who a request acts as.
type Identity struct {
	User   string `json:"user"`
	Role   string `json:"role"`            // admin | readonly
	Method string `json:"method"`          // session | token | socket
	Token  string `json:"token,omitempty"` // token id for Method token
	CSRF   string `json:"-"`               // session CSRF token
	sessID string // hashed session id
}

// CanWrite reports whether the identity may change config.
func (id Identity) CanWrite() bool { return id.Role == "admin" }

type session struct {
	Identity
	created, seen time.Time
}

// Sessions keeps browser sessions in memory (a daemon restart logs everyone
// out). The map key is the SHA-256 of the cookie value, so a memory dump
// does not reveal usable cookies.
type Sessions struct {
	Now func() time.Time

	mu   sync.Mutex
	byID map[string]*session
}

func (s *Sessions) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// RandomToken returns n random bytes, base64url without padding.
func RandomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("auth: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashID(v string) string {
	h := sha256.Sum256([]byte(v))
	return hex.EncodeToString(h[:])
}

// Create starts a session and returns the cookie value and its identity
// (with the CSRF token the client must echo in X-CSRF-Token).
func (s *Sessions) Create(user, role string) (string, Identity) {
	cookie := RandomToken(32)
	id := Identity{User: user, Role: role, Method: "session", CSRF: RandomToken(32), sessID: hashID(cookie)}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byID == nil {
		s.byID = map[string]*session{}
	}
	s.gcLocked(now)
	s.byID[id.sessID] = &session{Identity: id, created: now, seen: now}
	return cookie, id
}

// Lookup returns the session for a cookie value and refreshes its idle
// timer; expired sessions are removed.
func (s *Sessions) Lookup(cookie string) (Identity, bool) {
	if cookie == "" {
		return Identity{}, false
	}
	key := hashID(cookie)
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	ss, ok := s.byID[key]
	if !ok {
		return Identity{}, false
	}
	if s.expired(ss, now) {
		delete(s.byID, key)
		return Identity{}, false
	}
	ss.seen = now
	return ss.Identity, true
}

// Delete ends the session of id (logout).
func (s *Sessions) Delete(id Identity) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byID, id.sessID)
}

// DeleteUser ends every session of a user (password change, user removed).
func (s *Sessions) DeleteUser(user string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, ss := range s.byID {
		if ss.User == user {
			delete(s.byID, k)
		}
	}
}

func (s *Sessions) expired(ss *session, now time.Time) bool {
	return now.Sub(ss.seen) > IdleTimeout || now.Sub(ss.created) > AbsoluteTimeout
}

func (s *Sessions) gcLocked(now time.Time) {
	for k, ss := range s.byID {
		if s.expired(ss, now) {
			delete(s.byID, k)
		}
	}
}

// CheckCSRF compares the header value with the session's token in constant
// time.
func (id Identity) CheckCSRF(header string) bool {
	return id.CSRF != "" && subtle.ConstantTimeCompare([]byte(header), []byte(id.CSRF)) == 1
}
