package auth

import (
	"net/netip"
	"sync"
	"time"
)

// Login throttling: a client address is locked out after MaxFailures failed
// logins within Window, for Lockout. A user name is locked the same way
// after MaxUserFailures from any address, so a botnet cannot guess one
// account's password from many addresses; the higher threshold makes it
// harder to lock the real admin out on purpose.
//
// An IPv6 client can take a new address for every few guesses (its /64 holds
// billions), so IPv6 failures also count per /64, with the user threshold:
// rotating addresses buys no more guesses than the user limit allows, and one
// device can't lock its whole network out with 5 failures.
//
// An address that logged in successfully within TrustFor is not held back by
// a user or /64 lock (its own address limit still applies), so nobody can keep
// the real admin out by failing logins for that user from elsewhere.
const (
	MaxFailures     = 5
	MaxUserFailures = 20
	MaxNetFailures  = 20
	Window          = 15 * time.Minute
	Lockout         = 15 * time.Minute
	TrustFor        = 24 * time.Hour
)

// Limiter counts failed logins per address and per user.
type Limiter struct {
	Now func() time.Time

	mu      sync.Mutex
	addrs   map[string]*failures
	nets    map[string]*failures // IPv6 /64s
	users   map[string]*failures
	trusted map[string]time.Time // address -> last successful login
}

// net64 returns the /64 of an IPv6 address, "" for IPv4 or a non-address.
func net64(addr string) string {
	a, err := netip.ParseAddr(addr)
	if err != nil || a.Unmap().Is4() {
		return ""
	}
	p, err := a.WithZone("").Prefix(64)
	if err != nil {
		return ""
	}
	return p.String()
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
	check := []*failures{l.addrs[addr]}
	if t, ok := l.trusted[addr]; !ok || now.Sub(t) > TrustFor {
		check = append(check, l.users[user])
		if n := net64(addr); n != "" {
			check = append(check, l.nets[n])
		}
	}
	for _, f := range check {
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
		l.addrs, l.nets, l.users = map[string]*failures{}, map[string]*failures{}, map[string]*failures{}
	}
	locked := record(l.addrs, addr, MaxFailures, now)
	if n := net64(addr); n != "" && record(l.nets, n, MaxNetFailures, now) {
		locked = true
	}
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
	if len(l.nets) > 10000 {
		prune(l.nets, now)
	}
	return locked
}

// Succeed clears an address's failures (not the user's: a successful login
// from one place must not reset an attack on the account from elsewhere) and
// trusts the address for TrustFor.
func (l *Limiter) Succeed(addr string) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.addrs, addr)
	if l.trusted == nil {
		l.trusted = map[string]time.Time{}
	}
	if len(l.trusted) > 1000 {
		for a, t := range l.trusted {
			if now.Sub(t) > TrustFor {
				delete(l.trusted, a)
			}
		}
	}
	if len(l.trusted) < 1000 {
		l.trusted[addr] = now
	}
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
