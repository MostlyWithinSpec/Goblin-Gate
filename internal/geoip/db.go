// Package geoip keeps the country database behind "country" address objects
// (DB-IP IP to Country Lite, CC BY 4.0): it downloads and refreshes it,
// answers lookups, and fills the country sets of the nftables ruleset.
package geoip

import (
	"bufio"
	"fmt"
	"io"
	"net/netip"
	"sort"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/nft"
)

// MaxRows bounds a database (DB-IP Lite has about 600k rows).
const MaxRows = 3_000_000

type range4 struct {
	lo, hi uint32
	cc     uint16
}

type range6 struct {
	lo, hi netip.Addr
	cc     uint16
}

// DB is a parsed country database: sorted, non-overlapping ranges.
type DB struct {
	codes []string // index -> ISO code
	v4    []range4
	v6    []range6
}

// Rows is the number of ranges.
func (d *DB) Rows() int { return len(d.v4) + len(d.v6) }

// Parse reads "start,end,country" lines (DB-IP's CSV; quotes allowed).
// Rows with an unknown country (DB-IP marks reserved space ZZ) are skipped;
// a malformed row fails the whole file, so a broken download never
// replaces a good database.
func Parse(r io.Reader) (*DB, error) {
	d := &DB{}
	idx := map[string]uint16{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 4096), 4096)
	line := 0
	for sc.Scan() {
		line++
		t := strings.TrimSpace(sc.Text())
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		f := strings.Split(t, ",")
		if len(f) < 3 {
			return nil, fmt.Errorf("line %d: want start,end,country", line)
		}
		for i := range f {
			f[i] = strings.Trim(strings.TrimSpace(f[i]), `"`)
		}
		lo, err1 := netip.ParseAddr(f[0])
		hi, err2 := netip.ParseAddr(f[1])
		if err1 != nil || err2 != nil || lo.Is4() != hi.Is4() || hi.Less(lo) {
			return nil, fmt.Errorf("line %d: bad range %q-%q", line, f[0], f[1])
		}
		cc := strings.ToUpper(f[2])
		if _, ok := config.Countries[cc]; !ok {
			continue
		}
		i, ok := idx[cc]
		if !ok {
			i = uint16(len(d.codes))
			idx[cc] = i
			d.codes = append(d.codes, cc)
		}
		if lo.Is4() {
			d.v4 = append(d.v4, range4{u32(lo), u32(hi), i})
		} else {
			d.v6 = append(d.v6, range6{lo, hi, i})
		}
		if d.Rows() > MaxRows {
			return nil, fmt.Errorf("more than %d rows", MaxRows)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("line %d: %w", line+1, err)
	}
	sort.Slice(d.v4, func(i, j int) bool { return d.v4[i].lo < d.v4[j].lo })
	sort.Slice(d.v6, func(i, j int) bool { return d.v6[i].lo.Less(d.v6[j].lo) })
	for i := 1; i < len(d.v4); i++ {
		if d.v4[i].lo <= d.v4[i-1].hi {
			return nil, fmt.Errorf("overlapping ranges at %s", a4(d.v4[i].lo))
		}
	}
	for i := 1; i < len(d.v6); i++ {
		if !d.v6[i-1].hi.Less(d.v6[i].lo) {
			return nil, fmt.Errorf("overlapping ranges at %s", d.v6[i].lo)
		}
	}
	return d, nil
}

// Lookup returns the ISO code of a's country, "" when it is in none.
func (d *DB) Lookup(a netip.Addr) string {
	a = a.Unmap()
	if a.Is4() {
		v := u32(a)
		i := sort.Search(len(d.v4), func(i int) bool { return d.v4[i].hi >= v })
		if i < len(d.v4) && d.v4[i].lo <= v {
			return d.codes[d.v4[i].cc]
		}
		return ""
	}
	i := sort.Search(len(d.v6), func(i int) bool { return !d.v6[i].hi.Less(a) })
	if i < len(d.v6) && !a.Less(d.v6[i].lo) {
		return d.codes[d.v6[i].cc]
	}
	return ""
}

// Ranges returns the ranges of one country, IPv4 then IPv6.
func (d *DB) Ranges(country string) []nft.Range {
	cc := -1
	for i, c := range d.codes {
		if c == country {
			cc = i
		}
	}
	if cc < 0 {
		return nil
	}
	var out []nft.Range
	for _, r := range d.v4 {
		if int(r.cc) == cc {
			out = append(out, nft.Range{Lo: a4(r.lo), Hi: a4(r.hi)})
		}
	}
	for _, r := range d.v6 {
		if int(r.cc) == cc {
			out = append(out, nft.Range{Lo: r.lo, Hi: r.hi})
		}
	}
	return out
}

// Count returns the number of ranges per country.
func (d *DB) Count() map[string]int {
	m := map[string]int{}
	for _, r := range d.v4 {
		m[d.codes[r.cc]]++
	}
	for _, r := range d.v6 {
		m[d.codes[r.cc]]++
	}
	return m
}

func u32(a netip.Addr) uint32 {
	b := a.As4()
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func a4(v uint32) netip.Addr {
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}
