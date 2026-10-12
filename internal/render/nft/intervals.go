package nft

import (
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

// span is an inclusive [lo, hi] range of uint32 values: IPv4 addresses or ports.
type span struct{ lo, hi uint32 }

// mergeSpans sorts spans and merges overlapping or adjacent ones. nft rejects
// overlapping elements in interval sets, so every set is normalised first.
func mergeSpans(in []span) []span {
	if len(in) == 0 {
		return nil
	}
	s := append([]span(nil), in...)
	sort.Slice(s, func(i, j int) bool {
		if s[i].lo != s[j].lo {
			return s[i].lo < s[j].lo
		}
		return s[i].hi < s[j].hi
	})
	out := []span{s[0]}
	for _, v := range s[1:] {
		last := &out[len(out)-1]
		if last.hi == ^uint32(0) || v.lo <= last.hi+1 {
			if v.hi > last.hi {
				last.hi = v.hi
			}
			continue
		}
		out = append(out, v)
	}
	return out
}

func ip4ToUint(a netip.Addr) uint32 {
	b := a.As4()
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func uintToIP4(v uint32) netip.Addr {
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

// prefixSpan converts an IPv4 prefix to its address span.
func prefixSpan(p netip.Prefix) span {
	p = p.Masked()
	lo := ip4ToUint(p.Addr())
	hostBits := 32 - p.Bits()
	var hi uint32
	if hostBits == 32 {
		hi = ^uint32(0)
	} else {
		hi = lo | (uint32(1)<<hostBits - 1)
	}
	return span{lo, hi}
}

// formatAddrSpan renders an address span as a host, prefix or a-b range.
func formatAddrSpan(s span) string {
	if s.lo == s.hi {
		return uintToIP4(s.lo).String()
	}
	// Exact CIDR block?
	size := uint64(s.hi) - uint64(s.lo) + 1
	if size&(size-1) == 0 {
		bits := 0
		for n := size; n > 1; n >>= 1 {
			bits++
		}
		if s.lo&uint32(size-1) == 0 {
			return netip.PrefixFrom(uintToIP4(s.lo), 32-bits).String()
		}
	}
	return uintToIP4(s.lo).String() + "-" + uintToIP4(s.hi).String()
}

// parseIPv4 parses an IPv4 address, rejecting IPv6 with a clear message.
func parseIPv4(s string) (netip.Addr, error) {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return netip.Addr{}, err
	}
	a = a.Unmap()
	if !a.Is4() {
		return netip.Addr{}, fmt.Errorf("%s: IPv6 is not supported yet", s)
	}
	return a, nil
}

// addrValueSpan parses an address object value of the given type
// (subnet, host or range) into a span.
func addrValueSpan(typ, value string) (span, error) {
	value = strings.TrimSpace(value)
	switch typ {
	case "subnet":
		p, err := netip.ParsePrefix(value)
		if err != nil {
			return span{}, err
		}
		if !p.Addr().Unmap().Is4() {
			return span{}, fmt.Errorf("%s: IPv6 is not supported yet", value)
		}
		return prefixSpan(netip.PrefixFrom(p.Addr().Unmap(), p.Bits())), nil
	case "host":
		a, err := parseIPv4(value)
		if err != nil {
			return span{}, err
		}
		v := ip4ToUint(a)
		return span{v, v}, nil
	case "range":
		lo, hi, ok := strings.Cut(value, "-")
		if !ok {
			return span{}, fmt.Errorf("range %q: want a-b", value)
		}
		a, err := parseIPv4(lo)
		if err != nil {
			return span{}, err
		}
		b, err := parseIPv4(hi)
		if err != nil {
			return span{}, err
		}
		if ip4ToUint(a) > ip4ToUint(b) {
			return span{}, fmt.Errorf("range %q: start after end", value)
		}
		return span{ip4ToUint(a), ip4ToUint(b)}, nil
	}
	return span{}, fmt.Errorf("unsupported address type %q", typ)
}

// parsePorts parses "80,443,8000-8100" into merged port spans.
// An empty string yields no spans (meaning "all ports").
func parsePorts(s string) ([]span, error) {
	var out []span
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		loS, hiS, isRange := strings.Cut(part, "-")
		lo, err := parsePort(loS)
		if err != nil {
			return nil, err
		}
		hi := lo
		if isRange {
			if hi, err = parsePort(hiS); err != nil {
				return nil, err
			}
			if lo > hi {
				return nil, fmt.Errorf("port range %q: start after end", part)
			}
		}
		out = append(out, span{lo, hi})
	}
	return mergeSpans(out), nil
}

func parsePort(s string) (uint32, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 || n > 65535 {
		return 0, fmt.Errorf("invalid port %q", s)
	}
	return uint32(n), nil
}

func formatPortSpan(s span) string {
	if s.lo == s.hi {
		return strconv.FormatUint(uint64(s.lo), 10)
	}
	return fmt.Sprintf("%d-%d", s.lo, s.hi)
}
