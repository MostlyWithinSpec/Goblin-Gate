package feeds

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Parse extracts the raw entries of a feed body in the given format, then
// normalizes them for the feed type (see Normalize). Format is "" or
// "plain", "csv:<column>" (a 0-based column number, or a header name when
// the first row is a header) or "json:<path>" (a dot path; arrays on the way
// are walked element by element, an empty path means the document itself).
func Parse(data []byte, format, typ string) ([]string, error) {
	var raw []string
	var err error
	switch {
	case format == "" || format == "plain":
		raw = parsePlain(data)
	case strings.HasPrefix(format, "csv:"):
		raw, err = parseCSV(data, strings.TrimPrefix(format, "csv:"))
	case strings.HasPrefix(format, "json:"):
		raw, err = parseJSON(data, strings.TrimPrefix(format, "json:"))
	default:
		return nil, fmt.Errorf("feeds: unknown format %q", format)
	}
	if err != nil {
		return nil, err
	}
	return Normalize(raw, typ)
}

// bom is the UTF-8 byte order mark some feeds start with.
var bom = string(rune(0xFEFF))

// parsePlain returns one entry per line. "#" and ";" start comments (whole
// line or inline after whitespace). Hosts-file lines ("0.0.0.0 example.com")
// yield the name.
func parsePlain(data []byte) []string {
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, bom))
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if i := strings.IndexAny(line, " \t"); i >= 0 {
			rest := strings.TrimSpace(line[i:])
			first := line[:i]
			if rest == "" || rest[0] == '#' || rest[0] == ';' {
				line = first
			} else if _, err := netip.ParseAddr(first); err == nil {
				// hosts file: take the name, dropping its own comment
				line = strings.Fields(rest)[0]
			} else {
				line = first
			}
		}
		out = append(out, line)
	}
	return out
}

func parseCSV(data []byte, column string) ([]string, error) {
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1
	r.Comment = '#'
	r.LazyQuotes = true
	r.TrimLeadingSpace = true
	idx, err := strconv.Atoi(column)
	byName := err != nil
	if !byName && idx < 0 {
		return nil, fmt.Errorf("feeds: csv column %d is negative", idx)
	}
	var out []string
	first := true
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("feeds: csv: %w", err)
		}
		if first && byName {
			first = false
			idx = -1
			for i, h := range rec {
				if strings.EqualFold(strings.TrimSpace(h), column) {
					idx = i
				}
			}
			if idx < 0 {
				return nil, fmt.Errorf("feeds: csv header has no column %q", column)
			}
			continue
		}
		if idx < len(rec) {
			out = append(out, strings.TrimSpace(rec[idx]))
		}
	}
	return out, nil
}

func parseJSON(data []byte, path string) ([]string, error) {
	var doc any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("feeds: json: %w", err)
	}
	var keys []string
	if path != "" {
		keys = strings.Split(path, ".")
	}
	var out []string
	var walk func(v any, keys []string)
	walk = func(v any, keys []string) {
		switch x := v.(type) {
		case []any:
			for _, e := range x {
				walk(e, keys)
			}
		case map[string]any:
			if len(keys) > 0 {
				walk(x[keys[0]], keys[1:])
			}
		case string:
			if len(keys) == 0 {
				out = append(out, x)
			}
		}
	}
	walk(doc, keys)
	return out, nil
}

// Normalize cleans entries for a feed type, drops invalid ones and returns
// them sorted and de-duplicated.
//   - ip: addresses and CIDRs (IPv4 and IPv6) in canonical form; a single
//     host is written without a prefix length; prefixes inside another entry
//     are dropped and sibling prefixes merged.
//   - domain: lowercased names with at least one dot; a leading "*." or "."
//     and a trailing "." are removed.
//   - url: http(s) URLs with the scheme and host lowercased.
func Normalize(entries []string, typ string) ([]string, error) {
	switch typ {
	case "ip":
		return normalizeIPs(entries), nil
	case "domain":
		return normalizeDomains(entries), nil
	case "url":
		return normalizeURLs(entries), nil
	}
	return nil, fmt.Errorf("feeds: unknown feed type %q", typ)
}

func normalizeIPs(entries []string) []string {
	var ps []netip.Prefix
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if strings.Contains(e, "/") {
			if p, err := netip.ParsePrefix(e); err == nil {
				ps = append(ps, p.Masked())
			}
			continue
		}
		if a, err := netip.ParseAddr(e); err == nil && a.Zone() == "" {
			a = a.Unmap()
			ps = append(ps, netip.PrefixFrom(a, a.BitLen()))
		}
	}
	ps = collapse(ps)
	out := make([]string, len(ps))
	for i, p := range ps {
		if p.IsSingleIP() {
			out[i] = p.Addr().String()
		} else {
			out[i] = p.String()
		}
	}
	return out
}

// collapse sorts prefixes, removes those covered by another and merges
// sibling pairs (10.0.0.0/25 + 10.0.0.128/25 = 10.0.0.0/24) until stable.
func collapse(ps []netip.Prefix) []netip.Prefix {
	for {
		sort.Slice(ps, func(i, j int) bool {
			a, b := ps[i], ps[j]
			if c := a.Addr().Compare(b.Addr()); c != 0 {
				return c < 0
			}
			return a.Bits() < b.Bits()
		})
		var out []netip.Prefix
		for _, p := range ps {
			if n := len(out); n > 0 && out[n-1].Addr().BitLen() == p.Addr().BitLen() && out[n-1].Contains(p.Addr()) && out[n-1].Bits() <= p.Bits() {
				continue // covered by the previous (shorter or equal) prefix
			}
			out = append(out, p)
		}
		merged := false
		var res []netip.Prefix
		for i := 0; i < len(out); i++ {
			p := out[i]
			if i+1 < len(out) && p.Bits() > 0 && out[i+1].Bits() == p.Bits() {
				parent, _ := p.Addr().Prefix(p.Bits() - 1)
				if parent.Addr() == p.Addr() && parent.Contains(out[i+1].Addr()) {
					res = append(res, parent)
					i++
					merged = true
					continue
				}
			}
			res = append(res, p)
		}
		ps = res
		if !merged {
			return ps
		}
	}
}

var labelRe = regexp.MustCompile(`^[a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9_])?$`)

func validDomain(d string) bool {
	if len(d) > 253 || !strings.Contains(d, ".") {
		return false
	}
	for _, l := range strings.Split(d, ".") {
		if !labelRe.MatchString(l) {
			return false
		}
	}
	// an all-numeric name is an address, not a domain
	_, err := netip.ParseAddr(d)
	return err != nil
}

func normalizeDomains(entries []string) []string {
	set := map[string]bool{}
	for _, e := range entries {
		d := strings.ToLower(strings.TrimSpace(e))
		d = strings.TrimPrefix(d, "*.")
		d = strings.TrimPrefix(d, ".")
		d = strings.TrimSuffix(d, ".")
		if validDomain(d) {
			set[d] = true
		}
	}
	return sortedKeys(set)
}

func normalizeURLs(entries []string) []string {
	set := map[string]bool{}
	for _, e := range entries {
		u, err := url.Parse(strings.TrimSpace(e))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "HTTP" && u.Scheme != "HTTPS") || u.Host == "" {
			continue
		}
		u.Scheme = strings.ToLower(u.Scheme)
		u.Host = strings.ToLower(u.Host)
		u.Fragment = ""
		set[u.String()] = true
	}
	return sortedKeys(set)
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
