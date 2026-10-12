package nft

import (
	"bytes"
	"fmt"
	"net/netip"
	"strings"
)

// feedChunk is how many elements go in one add element statement; nft
// handles large statements but chunks keep each netlink message small.
const feedChunk = 4096

// FeedBatch returns an `nft -f` script that replaces the elements of a
// feed set in one transaction (flush + add), so the set is never seen half
// filled. Only IPv4 addresses and prefixes are kept (the sets are
// ipv4_addr); entries must not overlap (feeds.Normalize collapses them).
func FeedBatch(set string, entries []string) []byte {
	var b bytes.Buffer
	writeFeedBatch(&b, set, entries)
	return b.Bytes()
}

// FeedBatches is FeedBatch for several sets in one transaction.
func FeedBatches(sets map[string][]string) []byte {
	var b bytes.Buffer
	names := make([]string, 0, len(sets))
	for s := range sets {
		names = append(names, s)
	}
	sortBy(names, func(s string) string { return s })
	for _, s := range names {
		writeFeedBatch(&b, s, sets[s])
	}
	return b.Bytes()
}

func writeFeedBatch(b *bytes.Buffer, set string, entries []string) {
	fmt.Fprintf(b, "flush set inet %s %s\n", FilterTable, set)
	var chunk []string
	flush := func() {
		if len(chunk) > 0 {
			fmt.Fprintf(b, "add element inet %s %s { %s }\n", FilterTable, set, strings.Join(chunk, ", "))
			chunk = chunk[:0]
		}
	}
	for _, e := range entries {
		if !ipv4Entry(e) {
			continue
		}
		chunk = append(chunk, e)
		if len(chunk) == feedChunk {
			flush()
		}
	}
	flush()
}

func ipv4Entry(e string) bool {
	if strings.Contains(e, "/") {
		p, err := netip.ParsePrefix(e)
		return err == nil && p.Addr().Is4()
	}
	a, err := netip.ParseAddr(e)
	return err == nil && a.Is4()
}
