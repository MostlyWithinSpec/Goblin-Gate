package nft

import (
	"bytes"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// RuntimeSet is a set in table inet goblin whose members partly come at
// runtime: the set of a country or live address object, of a group with such
// members, or a policy's union set that includes one. The ruleset declares it
// with only its static part (the other members), and a ruleset load leaves it
// so; RuntimeBatch refills it with the static part, the countries' ranges and
// the live objects' members, all in one transaction, so one refill never
// drops what another source put in.
type RuntimeSet struct {
	Name      string
	IPv6      bool
	Countries []string // ISO codes
	Live      []string // live address objects (by name)
	spans     []span   // static IPv4 members
	spans6    []span6  // static IPv6 members
}

// CountrySet is the name RuntimeSet had when only countries were filled at
// runtime.
type CountrySet = RuntimeSet

// Range is an inclusive address range of one country.
type Range struct{ Lo, Hi netip.Addr }

// RuntimeSets lists the sets of cfg's ruleset that are refilled at runtime.
func RuntimeSets(cfg *config.Config) ([]RuntimeSet, error) {
	r, err := prepare(cfg)
	if err != nil {
		return nil, err
	}
	return r.objs.runtimeSets, nil
}

// CountrySets lists the runtime sets that hold country ranges.
func CountrySets(cfg *config.Config) ([]CountrySet, error) {
	all, err := RuntimeSets(cfg)
	if err != nil {
		return nil, err
	}
	var out []CountrySet
	for _, s := range all {
		if len(s.Countries) > 0 {
			out = append(out, s)
		}
	}
	return out, nil
}

// UsedCountries returns the country codes cfg's ruleset uses, sorted.
func UsedCountries(sets []CountrySet) []string {
	var out []string
	for _, s := range sets {
		for _, c := range s.Countries {
			if !slices.Contains(out, c) {
				out = append(out, c)
			}
		}
	}
	slices.Sort(out)
	return out
}

// CountryBatch is RuntimeBatch without live members.
func CountryBatch(sets []CountrySet, ranges func(country string) []Range) []byte {
	return RuntimeBatch(sets, ranges, nil)
}

// RuntimeBatch is an nft script that refills each set with its static
// members, its countries' ranges (from ranges, both families mixed; nil = no
// GeoIP data, countries add nothing) and its live objects' members (from live;
// nil = none), merged so no two elements overlap. One script is one
// transaction.
func RuntimeBatch(sets []RuntimeSet, ranges func(country string) []Range, live func(object string) []netip.Prefix) []byte {
	var b bytes.Buffer
	for _, s := range sets {
		var elems []string
		if s.IPv6 {
			sp := append([]span6(nil), s.spans6...)
			s.each(ranges, live, func(lo, hi netip.Addr) {
				if lo.Is6() && hi.Is6() && !lo.Is4In6() {
					sp = append(sp, span6{lo, hi})
				}
			})
			for _, x := range mergeSpans6(sp) {
				elems = append(elems, formatAddrSpan6(x))
			}
		} else {
			sp := append([]span(nil), s.spans...)
			s.each(ranges, live, func(lo, hi netip.Addr) {
				if lo.Is4() && hi.Is4() {
					sp = append(sp, span{ip4ToUint(lo), ip4ToUint(hi)})
				}
			})
			for _, x := range mergeSpans(sp) {
				elems = append(elems, formatAddrSpan(x))
			}
		}
		fmt.Fprintf(&b, "flush set inet %s %s\n", FilterTable, s.Name)
		for len(elems) > 0 {
			n := min(len(elems), feedChunk)
			fmt.Fprintf(&b, "add element inet %s %s { %s }\n", FilterTable, s.Name, strings.Join(elems[:n], ", "))
			elems = elems[n:]
		}
	}
	return b.Bytes()
}

// each calls add with every runtime member range of the set.
func (s RuntimeSet) each(ranges func(string) []Range, live func(string) []netip.Prefix, add func(lo, hi netip.Addr)) {
	if ranges != nil {
		for _, c := range s.Countries {
			for _, r := range ranges(c) {
				add(r.Lo, r.Hi)
			}
		}
	}
	if live != nil {
		for _, o := range s.Live {
			for _, p := range live(o) {
				lo, hi := prefixRange(p)
				add(lo, hi)
			}
		}
	}
}

// prefixRange is the first and last address of p (IPv4-mapped prefixes as
// plain IPv4).
func prefixRange(p netip.Prefix) (netip.Addr, netip.Addr) {
	a, bits := p.Addr(), p.Bits()
	if a.Is4In6() {
		a, bits = a.Unmap(), max(bits-96, 0)
	}
	lo := netip.PrefixFrom(a, bits).Masked().Addr()
	b := lo.AsSlice()
	for i := bits; i < len(b)*8; i++ {
		b[i/8] |= 1 << (7 - i%8)
	}
	hi, _ := netip.AddrFromSlice(b)
	return lo, hi
}
