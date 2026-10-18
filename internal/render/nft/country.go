package nft

import (
	"bytes"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// CountrySet is a set in table inet goblin that the GeoIP syncer fills: the
// set of a country address object, of a group with country members, or a
// policy's union set that includes one. The ruleset declares it with only
// its static part (the non-country members); a ruleset load leaves it so.
type CountrySet struct {
	Name      string
	IPv6      bool
	Countries []string // ISO codes
	spans     []span   // static IPv4 members
	spans6    []span6  // static IPv6 members
}

// Range is an inclusive address range of one country.
type Range struct{ Lo, Hi netip.Addr }

// CountrySets lists the sets of cfg's ruleset that hold country ranges.
func CountrySets(cfg *config.Config) ([]CountrySet, error) {
	r, err := prepare(cfg)
	if err != nil {
		return nil, err
	}
	return r.objs.countrySets, nil
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

// CountryBatch is an nft script that refills each set with its static
// members plus its countries' ranges (from ranges, both families mixed),
// merged so no two elements overlap. One script is one transaction.
func CountryBatch(sets []CountrySet, ranges func(country string) []Range) []byte {
	var b bytes.Buffer
	for _, s := range sets {
		var elems []string
		if s.IPv6 {
			sp := append([]span6(nil), s.spans6...)
			for _, c := range s.Countries {
				for _, r := range ranges(c) {
					if r.Lo.Is6() && r.Hi.Is6() {
						sp = append(sp, span6{r.Lo, r.Hi})
					}
				}
			}
			for _, x := range mergeSpans6(sp) {
				elems = append(elems, formatAddrSpan6(x))
			}
		} else {
			sp := append([]span(nil), s.spans...)
			for _, c := range s.Countries {
				for _, r := range ranges(c) {
					if r.Lo.Is4() && r.Hi.Is4() {
						sp = append(sp, span{ip4ToUint(r.Lo), ip4ToUint(r.Hi)})
					}
				}
			}
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
