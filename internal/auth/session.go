package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"sort"
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
	// Scopes of a scoped token (task 102); nil = everything Role allows.
	Scopes []string `json:"scopes,omitempty"`
	CSRF   string   `json:"-"` // session CSRF token
	sessID string   // hashed session id
}

// CanWrite reports whether the identity has full admin rights: change
// anything, see secrets. A scoped token never does; its writes go through
// Has.
func (id Identity) CanWrite() bool { return id.Role == "admin" && id.Scopes == nil }

// Has reports whether the identity holds a token scope: a full admin holds
// every scope, a read-only user only ScopeRead.
func (id Identity) Has(scope string) bool {
	if id.Scopes == nil {
		return id.Role == "admin" || scope == ScopeRead
	}
	if id.Role != "admin" && scope != ScopeRead {
		return false // the user was demoted after the token was made
	}
	for _, s := range id.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// SessID returns the hashed session id of a session-method identity, or ""
// for other methods. It is what the active-sessions list keys on.
func (id Identity) SessID() string { return id.sessID }

type session struct {
	Identity
	addr, userAgent string
	created, seen   time.Time
}

// SessionInfo is the public view of a live session (task 086). The ID is
// the first 16 hex chars of the hashed cookie, never the cookie itself.
type SessionInfo struct {
	ID        string    `json:"id"`
	User      string    `json:"user"`
	Role      string    `json:"role"`
	Addr      string    `json:"addr"`
	UserAgent string    `json:"user_agent"`
	Created   time.Time `json:"created"`
	LastSeen  time.Time `json:"last_seen"`
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
	return s.CreateFrom(user, role, "", "")
}

// CreateFrom is Create with the client address and user agent recorded on
// the session for the active-sessions list. The user agent is truncated to
// 200 chars.
func (s *Sessions) CreateFrom(user, role, addr, userAgent string) (string, Identity) {
	if len(userAgent) > 200 {
		userAgent = userAgent[:200]
	}
	cookie := RandomToken(32)
	id := Identity{User: user, Role: role, Method: "session", CSRF: RandomToken(32), sessID: hashID(cookie)}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byID == nil {
		s.byID = map[string]*session{}
	}
	s.gcLocked(now)
	s.byID[id.sessID] = &session{Identity: id, addr: addr, userAgent: userAgent, created: now, seen: now}
	return cookie, id
}

// List returns the live (non-expired) sessions, newest first. Expired ones
// are removed as a side effect.
func (s *Sessions) List() []SessionInfo {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked(now)
	out := make([]SessionInfo, 0, len(s.byID))
	for k, ss := range s.byID {
		out = append(out, SessionInfo{
			ID:        k[:16],
			User:      ss.User,
			Role:      ss.Role,
			Addr:      ss.addr,
			UserAgent: ss.userAgent,
			Created:   ss.created,
			LastSeen:  ss.seen,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

// DeleteByID ends the session whose short id (the first 16 hex characters
// of its hashed id, as the active-sessions list shows it) is exactly id. It
// reports whether a session was found.
func (s *Sessions) DeleteByID(id string) bool {
	if len(id) != 16 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.byID {
		if k[:16] == id {
			delete(s.byID, k)
			return true
		}
	}
	return false
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
