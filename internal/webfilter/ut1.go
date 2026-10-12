package webfilter

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
)

// UT1URL is the Université Toulouse Capitole blacklist collection
// (CC BY-SA 4.0): one directory per category with a "domains" file.
const UT1URL = "https://dsi.ut-capitole.fr/blacklists/download/blacklists.tar.gz"

// BuildUT1 reads a UT1 blacklists.tar.gz and adds every <category>/domains
// file to b. Symlinked alias categories and the "urls" files are skipped.
func BuildUT1(r io.Reader, b *Builder) (categories int, err error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return 0, fmt.Errorf("webfilter: ut1: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return categories, fmt.Errorf("webfilter: ut1: %w", err)
		}
		if h.Typeflag != tar.TypeReg || path.Base(h.Name) != "domains" {
			continue
		}
		cat := path.Base(path.Dir(h.Name))
		if cat == "." || cat == "/" || !wfCategory(cat) {
			continue
		}
		sc := bufio.NewScanner(tr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || line[0] == '#' {
				continue
			}
			if err := b.Add(cat, line); err != nil {
				return categories, err
			}
		}
		if err := sc.Err(); err != nil {
			return categories, fmt.Errorf("webfilter: ut1 %s: %w", cat, err)
		}
		categories++
	}
	return categories, nil
}

// wfCategory matches config's category names ([a-z0-9_-]).
func wfCategory(s string) bool {
	if s == "" || len(s) > 40 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
