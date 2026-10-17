package nft

import (
	"bytes"
	"fmt"
	"net/netip"
	"sort"
	"time"
)

// Temporary blocks: two sets whose elements carry their own timeout, so
// every block expires in the kernel by itself. goblind fills them at run
// time (internal/blocks) and again after every ruleset load, which empties
// them. The temp_blocks chain runs first in input and forward and drops
// both directions, also of connections that were already established.
const (
	BlockSet4      = "block_tmp"
	BlockSet6      = "block_tmp6"
	BlockLogPrefix = "GG:block:"
	blockChain     = "temp_blocks"
)

// blockRules is the temp_blocks chain. Logging is rate limited (a blocked
// host may keep sending); every packet is dropped.
func blockRules() []string {
	log := fmt.Sprintf("limit rate 10/second burst 20 packets log group %d prefix %s", LogGroup, quote(BlockLogPrefix))
	var out []string
	for _, m := range []struct{ fam, set string }{{"ip", BlockSet4}, {"ip6", BlockSet6}} {
		for _, dir := range []string{"saddr", "daddr"} {
			match := fmt.Sprintf("%s %s @%s", m.fam, dir, m.set)
			out = append(out, match+" "+log, match+" drop")
		}
	}
	return out
}

// BlockScript returns an nft script that (re)sets the given blocks with
// their remaining time and removes the others listed in remove. Each
// element is added plain, deleted and added again: adding an existing
// element is a no-op and deleting a missing one fails, so this sequence
// works whatever the set holds and replaces the timeout.
func BlockScript(add map[netip.Addr]time.Duration, remove []netip.Addr) []byte {
	var b bytes.Buffer
	set := func(a netip.Addr) string {
		if a.Is4() {
			return BlockSet4
		}
		return BlockSet6
	}
	addrs := make([]netip.Addr, 0, len(add))
	for a := range add {
		addrs = append(addrs, a)
	}
	sort.Slice(addrs, func(i, j int) bool { return addrs[i].Less(addrs[j]) })
	for _, a := range addrs {
		secs := int(add[a].Round(time.Second) / time.Second)
		if secs < 1 {
			secs = 1
		}
		s := set(a)
		fmt.Fprintf(&b, "add element inet %s %s { %s }\n", FilterTable, s, a)
		fmt.Fprintf(&b, "delete element inet %s %s { %s }\n", FilterTable, s, a)
		fmt.Fprintf(&b, "add element inet %s %s { %s timeout %ds }\n", FilterTable, s, a, secs)
	}
	for _, a := range remove {
		s := set(a)
		fmt.Fprintf(&b, "add element inet %s %s { %s }\n", FilterTable, s, a)
		fmt.Fprintf(&b, "delete element inet %s %s { %s }\n", FilterTable, s, a)
	}
	return b.Bytes()
}
