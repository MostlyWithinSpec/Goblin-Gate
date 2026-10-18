package config

import (
	"bytes"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

var nameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,63}$`)

// ValidName reports whether s is a valid object or user name (nameRe).
func ValidName(s string) bool { return nameRe.MatchString(s) }

// validName reports whether s is a legal object name: 1-63 characters of
// [A-Za-z0-9_.-] (see docs/dev/conventions.md).
func validName(s string) bool { return nameRe.MatchString(s) }

// isWildcard reports whether s is one of the match-everything references.
func isWildcard(s string) bool {
	s = strings.ToLower(s)
	return s == "all" || s == "any"
}

func oneOf(s string, options ...string) bool {
	for _, o := range options {
		if s == o {
			return true
		}
	}
	return false
}

var hostLabelRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?$`)

// validHostname reports whether s is a legal RFC 1123 hostname.
func validHostname(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if !hostLabelRe.MatchString(label) {
			return false
		}
	}
	return true
}

// splitPortRange parses a single port or "lo-hi" range. ok is false when the
// text does not parse or a value falls outside 1-65535.
func splitPortRange(part string) (lo, hi int, ok bool) {
	part = strings.TrimSpace(part)
	if i := strings.IndexByte(part, '-'); i >= 0 {
		a, errA := strconv.Atoi(part[:i])
		b, errB := strconv.Atoi(part[i+1:])
		if errA != nil || errB != nil || a < 1 || a > 65535 || b < 1 || b > 65535 {
			return 0, 0, false
		}
		return a, b, true
	}
	a, err := strconv.Atoi(part)
	if err != nil || a < 1 || a > 65535 {
		return 0, 0, false
	}
	return a, a, true
}

// portSpecOK reports whether s is a comma-separated list of ports or ranges,
// each within 1-65535 with lo <= hi, e.g. "80" or "443,8000-8100".
func portSpecOK(s string) bool {
	if s == "" {
		return false
	}
	for _, part := range strings.Split(s, ",") {
		lo, hi, ok := splitPortRange(part)
		if !ok || lo > hi {
			return false
		}
	}
	return true
}

// portOrRange parses exactly one port or one "lo-hi" range.
func portOrRange(s string) (lo, hi int, ok bool) {
	if strings.Contains(s, ",") {
		return 0, 0, false
	}
	lo, hi, ok = splitPortRange(s)
	return lo, hi, ok && lo <= hi
}

// addrCmp compares two addresses numerically. ok is false when the families
// differ and no ordering exists.
func addrCmp(a, b netip.Addr) (int, bool) {
	a, b = a.Unmap(), b.Unmap()
	if a.Is4() != b.Is4() {
		return 0, false
	}
	return a.Compare(b), true
}

// prefixBounds returns the first and last address of p.
func prefixBounds(p netip.Prefix) (lo, hi [16]byte) {
	a := p.Masked().Addr().Unmap()
	lo = a.As16()
	hi = lo
	bits := 32
	if a.Is6() {
		bits = 128
	}
	for i := 0; i < bits-p.Bits(); i++ {
		hi[15-i/8] |= 1 << (i % 8)
	}
	return lo, hi
}

// prefixesOverlap reports whether a and b contain any common address.
func prefixesOverlap(a, b netip.Prefix) bool {
	if a.Addr().Unmap().Is4() != b.Addr().Unmap().Is4() {
		return false
	}
	aLo, aHi := prefixBounds(a)
	bLo, bHi := prefixBounds(b)
	return bytes.Compare(aLo[:], bHi[:]) <= 0 && bytes.Compare(bLo[:], aHi[:]) <= 0
}

// selfIncluding returns the set of group indices that include themselves,
// directly or through other groups. names and members are parallel slices.
func selfIncluding(names []string, members [][]string) map[int]bool {
	byName := make(map[string]int, len(names))
	for i, n := range names {
		byName[n] = i
	}
	cyclic := make(map[int]bool, len(names))
	for start := range names {
		visited := map[int]bool{}
		var stack []int
		for _, m := range members[start] {
			if j, ok := byName[m]; ok {
				stack = append(stack, j)
			}
		}
		for len(stack) > 0 {
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if n == start {
				cyclic[start] = true
				break
			}
			if visited[n] {
				continue
			}
			visited[n] = true
			for _, m := range members[n] {
				if j, ok := byName[m]; ok {
					stack = append(stack, j)
				}
			}
		}
	}
	return cyclic
}

// nameList extracts one string field from a slice of structs.
func nameList[T any](items []T, get func(T) string) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = get(it)
	}
	return out
}

// toSet converts slices of names to one membership set.
func toSet(lists ...[]string) map[string]bool {
	m := map[string]bool{}
	for _, l := range lists {
		for _, s := range l {
			m[s] = true
		}
	}
	return m
}

// validHost accepts a DNS name or an IP address (as a WireGuard endpoint
// host): nothing that could carry spaces, newlines or other syntax into the
// files it is written to.
func validHost(s string) bool {
	if _, err := netip.ParseAddr(s); err == nil {
		return true
	}
	return validHostname(strings.TrimSuffix(s, "."))
}

// validHostPort accepts "host:port" or "[v6]:port" with a valid host and a
// port 1-65535.
func validHostPort(s string) bool {
	host, port, ok := strings.Cut(s, ":")
	if strings.HasPrefix(s, "[") {
		end := strings.Index(s, "]:")
		if end < 0 {
			return false
		}
		host, port, ok = s[1:end], s[end+2:], true
		if a, err := netip.ParseAddr(host); err != nil || !a.Is6() {
			return false
		}
	}
	n, err := strconv.Atoi(port)
	return ok && err == nil && n >= 1 && n <= 65535 && strconv.Itoa(n) == port && validHost(host)
}
