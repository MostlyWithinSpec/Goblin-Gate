package auth

import (
	"sync"
	"time"
)

// Login throttling: a client address is locked out after MaxFailures failed
// logins within Window, for Lockout. A user name is locked the same way
// after MaxUserFailures from any address, so a botnet cannot guess one
// account's password from many addresses; the higher threshold makes it
// harder to lock the real admin out on purpose.
const (
	MaxFailures     = 5
	MaxUserFailures = 20
	Window          = 15 * time.Minute
	Lockout         = 15 * time.Minute
)

// Limiter counts failed logins per address and per user.
type Limiter struct {
	Now func() time.Time

	mu    sync.Mutex
	addrs map[string]*failures
	users map[string]*failures
}

type failures struct {
	times       []time.Time
	lockedUntil time.Time
}

func (l *Limiter) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// Allowed reports whether a login attempt may proceed; when it may not, it
// returns how long until it may.
func (l *Limiter) Allowed(addr, user string) (bool, time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	wait := time.Duration(0)
	for _, f := range []*failures{l.addrs[addr], l.users[user]} {
		if f != nil && now.Before(f.lockedUntil) {
			wait = max(wait, f.lockedUntil.Sub(now))
		}
	}
	return wait == 0, wait
}

// Fail records a failed login and reports whether it caused a lockout.
func (l *Limiter) Fail(addr, user string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.addrs == nil {
		l.addrs, l.users = map[string]*failures{}, map[string]*failures{}
	}
	locked := record(l.addrs, addr, MaxFailures, now)
	if record(l.users, user, MaxUserFailures, now) {
		locked = true
	}
	// keep the maps small
	if len(l.addrs) > 10000 {
		prune(l.addrs, now)
	}
	if len(l.users) > 10000 {
		prune(l.users, now)
	}
	return locked
}

// Succeed clears an address's failures (not the user's: a successful login
// from one place must not reset an attack on the account from elsewhere).
func (l *Limiter) Succeed(addr string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.addrs, addr)
}

func record(m map[string]*failures, key string, limit int, now time.Time) bool {
	f := m[key]
	if f == nil {
		f = &failures{}
		m[key] = f
	}
	recent := f.times[:0]
	for _, t := range f.times {
		if now.Sub(t) < Window {
			recent = append(recent, t)
		}
	}
	f.times = append(recent, now)
	if len(f.times) >= limit {
		f.lockedUntil = now.Add(Lockout)
		f.times = nil
		return true
	}
	return false
}

func prune(m map[string]*failures, now time.Time) {
	for k, f := range m {
		if now.After(f.lockedUntil) && (len(f.times) == 0 || now.Sub(f.times[len(f.times)-1]) > Window) {
			delete(m, k)
		}
	}
}
