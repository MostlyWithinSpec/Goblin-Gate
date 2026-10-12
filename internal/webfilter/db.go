// Package webfilter is Goblin Gate's web filter (task 033): a compact
// domain category database, a DNS proxy that applies web filter profiles
// per client subnet, and the block page.
//
// Decision (task 033): the filter works at the DNS layer. Category lists
// are millions of names (UT1's adult list alone is ~4.6M), far too many for
// Unbound RPZ zones or Suricata SNI rules on a home router, so goblind
// answers the DNS queries of filtered subnets itself (nft redirects them to
// the proxy) using a hash database of ~9 bytes per name. There is no SNI
// layer: DNS from filtered subnets is forced to the box and DNS-over-TLS is
// rejected (task 029); DNS-over-HTTPS resolvers are blockable by name (UT1
// category "doh").
package webfilter

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"sort"
	"strings"
)

// dbMagic starts a database file.
const dbMagic = "GGWF1\n"

// DB maps domain names to categories. A name's categories are those of the
// name itself and of every parent domain in the database (a listed
// "example.com" also covers "www.example.com"). Names are stored as 64-bit
// FNV-1a hashes; collisions are negligible at list sizes.
type DB struct {
	names  []string // category names; index = category id
	hashes []uint64 // sorted; parallel to cats
	cats   []uint8
}

// Builder collects names per category. Entries are kept as a flat slice
// (16 bytes each) and sorted once, so millions of names build in little
// memory.
type Builder struct {
	ids     map[string]uint8
	names   []string
	entries []entry
}

type entry struct {
	hash uint64
	cat  uint8
}

// NewBuilder starts an empty database.
func NewBuilder() *Builder {
	return &Builder{ids: map[string]uint8{}}
}

// Add puts domain into category (at most 255 categories).
func (b *Builder) Add(category, domain string) error {
	d := normalize(domain)
	if d == "" {
		return nil
	}
	id, ok := b.ids[category]
	if !ok {
		if len(b.names) == 255 {
			return errors.New("webfilter: more than 255 categories")
		}
		id = uint8(len(b.names))
		b.ids[category] = id
		b.names = append(b.names, category)
	}
	b.entries = append(b.entries, entry{hash(d), id})
	return nil
}

// WriteTo writes the database: magic, category count and names, entry
// count, then (hash uint64, category uint8) pairs sorted by hash and
// category, without duplicates.
func (b *Builder) WriteTo(w io.Writer) (int64, error) {
	sort.Slice(b.entries, func(i, j int) bool {
		x, y := b.entries[i], b.entries[j]
		return x.hash < y.hash || x.hash == y.hash && x.cat < y.cat
	})
	uniq := b.entries[:0]
	for i, e := range b.entries {
		if i == 0 || e != b.entries[i-1] {
			uniq = append(uniq, e)
		}
	}
	b.entries = uniq

	bw := bufio.NewWriter(w)
	var n int64
	write := func(p []byte) {
		m, _ := bw.Write(p)
		n += int64(m)
	}
	write([]byte(dbMagic))
	var buf [9]byte
	binary.LittleEndian.PutUint16(buf[:2], uint16(len(b.names)))
	write(buf[:2])
	for _, name := range b.names {
		write([]byte{byte(len(name))})
		write([]byte(name))
	}
	binary.LittleEndian.PutUint32(buf[:4], uint32(len(b.entries)))
	write(buf[:4])
	for _, e := range b.entries {
		binary.LittleEndian.PutUint64(buf[:8], e.hash)
		buf[8] = e.cat
		write(buf[:9])
	}
	return n, bw.Flush()
}

// Read loads a database written by Builder.WriteTo.
func Read(r io.Reader) (*DB, error) {
	br := bufio.NewReaderSize(r, 1<<20)
	magic := make([]byte, len(dbMagic))
	if _, err := io.ReadFull(br, magic); err != nil || string(magic) != dbMagic {
		return nil, errors.New("webfilter: not a category database")
	}
	var buf [9]byte
	if _, err := io.ReadFull(br, buf[:2]); err != nil {
		return nil, fmt.Errorf("webfilter: database header: %w", err)
	}
	db := &DB{names: make([]string, binary.LittleEndian.Uint16(buf[:2]))}
	for i := range db.names {
		l, err := br.ReadByte()
		if err != nil {
			return nil, fmt.Errorf("webfilter: database header: %w", err)
		}
		name := make([]byte, l)
		if _, err := io.ReadFull(br, name); err != nil {
			return nil, fmt.Errorf("webfilter: database header: %w", err)
		}
		db.names[i] = string(name)
	}
	if _, err := io.ReadFull(br, buf[:4]); err != nil {
		return nil, fmt.Errorf("webfilter: database header: %w", err)
	}
	n := binary.LittleEndian.Uint32(buf[:4])
	db.hashes = make([]uint64, n)
	db.cats = make([]uint8, n)
	for i := range db.hashes {
		if _, err := io.ReadFull(br, buf[:9]); err != nil {
			return nil, fmt.Errorf("webfilter: database entries: %w", err)
		}
		db.hashes[i] = binary.LittleEndian.Uint64(buf[:8])
		db.cats[i] = buf[8]
		if int(db.cats[i]) >= len(db.names) || i > 0 && db.hashes[i] < db.hashes[i-1] {
			return nil, errors.New("webfilter: corrupt database")
		}
	}
	return db, nil
}

// Open reads a database file.
func Open(path string) (*DB, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Read(f)
}

// Categories returns the category names in the database.
func (db *DB) Categories() []string {
	if db == nil {
		return nil
	}
	return append([]string(nil), db.names...)
}

// Len is the number of (name, category) entries.
func (db *DB) Len() int {
	if db == nil {
		return 0
	}
	return len(db.hashes)
}

// Lookup returns the sorted categories of domain and its parent domains.
func (db *DB) Lookup(domain string) []string {
	if db == nil {
		return nil
	}
	seen := map[string]bool{}
	for _, d := range Suffixes(normalize(domain)) {
		for _, c := range db.exact(d) {
			seen[c] = true
		}
	}
	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// exact returns the categories listed for exactly this (normalized) name.
func (db *DB) exact(d string) []string {
	if db == nil {
		return nil
	}
	h := hash(d)
	var out []string
	i := sort.Search(len(db.hashes), func(i int) bool { return db.hashes[i] >= h })
	for ; i < len(db.hashes) && db.hashes[i] == h; i++ {
		out = append(out, db.names[db.cats[i]])
	}
	return out
}

// Suffixes returns d and its parent domains, most specific first, never a
// bare top-level label ("a.b.example.com" -> a.b.example.com, b.example.com,
// example.com).
func Suffixes(d string) []string {
	var out []string
	for d != "" && strings.Contains(d, ".") {
		out = append(out, d)
		d = d[strings.Index(d, ".")+1:]
	}
	return out
}

func normalize(d string) string {
	d = strings.ToLower(strings.TrimSpace(d))
	d = strings.TrimPrefix(d, "*.")
	d = strings.TrimSuffix(d, ".")
	if d == "" || strings.ContainsAny(d, " /:\t") || !strings.Contains(d, ".") {
		return ""
	}
	return d
}

func hash(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}
