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

// span6 is an inclusive range of IPv6 addresses (task 045).
type span6 struct{ lo, hi netip.Addr }

// mergeSpans6 is mergeSpans for IPv6 ranges.
func mergeSpans6(in []span6) []span6 {
	if len(in) == 0 {
		return nil
	}
	s := append([]span6(nil), in...)
	sort.Slice(s, func(i, j int) bool {
		if c := s[i].lo.Compare(s[j].lo); c != 0 {
			return c < 0
		}
		return s[i].hi.Less(s[j].hi)
	})
	out := []span6{s[0]}
	for _, v := range s[1:] {
		last := &out[len(out)-1]
		next := last.hi.Next() // invalid after the last address: everything is covered
		if !next.IsValid() || v.lo.Compare(next) <= 0 {
			if last.hi.Less(v.hi) {
				last.hi = v.hi
			}
			continue
		}
		out = append(out, v)
	}
	return out
}

// prefixSpan6 converts an IPv6 prefix to its address span.
func prefixSpan6(p netip.Prefix) span6 {
	p = p.Masked()
	hi := p.Addr().As16()
	for i := p.Bits(); i < 128; i++ {
		hi[i/8] |= 0x80 >> (i % 8)
	}
	return span6{p.Addr(), netip.AddrFrom16(hi)}
}

// formatAddrSpan6 renders an IPv6 span as a host, prefix or a-b range.
func formatAddrSpan6(s span6) string {
	if s.lo == s.hi {
		return s.lo.String()
	}
	for bits := 0; bits <= 128; bits++ {
		if p := netip.PrefixFrom(s.lo, bits); p.Masked().Addr() == s.lo && prefixSpan6(p).hi == s.hi {
			return p.String()
		}
	}
	return s.lo.String() + "-" + s.hi.String()
}

// addrValue parses an address object value of the given type (subnet, host
// or range) into an IPv4 or an IPv6 span.
func addrValue(typ, value string) ([]span, []span6, error) {
	value = strings.TrimSpace(value)
	parse := func(s string) (netip.Addr, error) {
		a, err := netip.ParseAddr(strings.TrimSpace(s))
		return a.Unmap(), err
	}
	switch typ {
	case "subnet":
		p, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, nil, err
		}
		if a := p.Addr(); a.Is4In6() {
			return nil, nil, fmt.Errorf("%s: write IPv4-mapped subnets as IPv4", value)
		} else if a.Is6() {
			return nil, []span6{prefixSpan6(p)}, nil
		}
		return []span{prefixSpan(p)}, nil, nil
	case "host":
		a, err := parse(value)
		if err != nil {
			return nil, nil, err
		}
		if a.Is6() {
			return nil, []span6{{a, a}}, nil
		}
		v := ip4ToUint(a)
		return []span{{v, v}}, nil, nil
	case "range":
		loS, hiS, ok := strings.Cut(value, "-")
		if !ok {
			return nil, nil, fmt.Errorf("range %q: want a-b", value)
		}
		lo, err := parse(loS)
		if err != nil {
			return nil, nil, err
		}
		hi, err := parse(hiS)
		if err != nil {
			return nil, nil, err
		}
		if lo.Is4() != hi.Is4() {
			return nil, nil, fmt.Errorf("range %q mixes IPv4 and IPv6", value)
		}
		if hi.Less(lo) {
			return nil, nil, fmt.Errorf("range %q: start after end", value)
		}
		if lo.Is6() {
			return nil, []span6{{lo, hi}}, nil
		}
		return []span{{ip4ToUint(lo), ip4ToUint(hi)}}, nil, nil
	}
	return nil, nil, fmt.Errorf("unsupported address type %q", typ)
}
