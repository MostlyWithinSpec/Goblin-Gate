package nft

import (
	"bytes"
	"fmt"
	"net/netip"
	"sort"
	"time"
)

// CrowdSec bans (task 104): four sets filled at run time by
// internal/crowdsec (single addresses and ranges, per family; every element
// carries the decision's remaining time) and a chain that drops them. A
// commit reloads the table and empties the sets; the manager refills them
// (Resync), like the temporary blocks.
const (
	CrowdSecSet4    = "crowdsec4"
	CrowdSecSet6    = "crowdsec6"
	CrowdSecRange4  = "crowdsec_range4"
	CrowdSecRange6  = "crowdsec_range6"
	CrowdSecCounter = "crowdsec_drops"
	crowdSecChain   = "crowdsec"
)

// crowdSecSets lists the sets with their element type and flags.
var crowdSecSets = []struct{ name, typ, flags string }{
	{CrowdSecSet4, "ipv4_addr", "timeout"},
	{CrowdSecSet6, "ipv6_addr", "timeout"},
	{CrowdSecRange4, "ipv4_addr", "interval, timeout"},
	{CrowdSecRange6, "ipv6_addr", "interval, timeout"},
}

// crowdSecRules returns the crowdsec chain's rules, or nil when CrowdSec
// is off (no chain, no sets).
func (r *ruleset) crowdSecRules() []string {
	c := r.cfg.CrowdSec
	if !c.Enabled {
		return nil
	}
	in := ""
	if c.Scope != "all" {
		wan := r.wanMatch()
		if wan == "" {
			return []string{"# crowdsec: no WAN interface"}
		}
		in = "iifname " + wan + " "
	}
	dirs := []string{"saddr"}
	if c.Scope == "all" {
		dirs = append(dirs, "daddr")
	}
	var out []string
	for _, dir := range dirs {
		for _, s := range crowdSecSets {
			fam := "ip"
			if s.typ == "ipv6_addr" {
				fam = "ip6"
			}
			out = append(out, fmt.Sprintf("%s%s %s @%s counter name %s drop", in, fam, dir, s.name, quote(CrowdSecCounter)))
		}
	}
	return out
}

// CrowdSecBan is one decision as the sets hold it.
type CrowdSecBan struct {
	Prefix netip.Prefix // a single address is a /32 or /128
	Left   time.Duration
}

// CrowdSecScript (re)sets bans with their remaining time and removes the
// prefixes in remove. Like BlockScript, each element is added plain,
// deleted and added again, so the script works whatever the set holds.
func CrowdSecScript(add []CrowdSecBan, remove []netip.Prefix) []byte {
	var b bytes.Buffer
	sort.Slice(add, func(i, j int) bool { return add[i].Prefix.String() < add[j].Prefix.String() })
	for _, ban := range add {
		set, elem := crowdSecElem(ban.Prefix)
		secs := int(ban.Left.Round(time.Second) / time.Second)
		if secs < 1 {
			secs = 1
		}
		fmt.Fprintf(&b, "add element inet %s %s { %s }\n", FilterTable, set, elem)
		fmt.Fprintf(&b, "delete element inet %s %s { %s }\n", FilterTable, set, elem)
		fmt.Fprintf(&b, "add element inet %s %s { %s timeout %ds }\n", FilterTable, set, elem, secs)
	}
	for _, p := range remove {
		set, elem := crowdSecElem(p)
		fmt.Fprintf(&b, "add element inet %s %s { %s }\n", FilterTable, set, elem)
		fmt.Fprintf(&b, "delete element inet %s %s { %s }\n", FilterTable, set, elem)
	}
	return b.Bytes()
}

func crowdSecElem(p netip.Prefix) (set, elem string) {
	p = p.Masked()
	switch {
	case p.Addr().Is4() && p.IsSingleIP():
		return CrowdSecSet4, p.Addr().String()
	case p.Addr().Is4():
		return CrowdSecRange4, p.String()
	case p.IsSingleIP():
		return CrowdSecSet6, p.Addr().String()
	default:
		return CrowdSecRange6, p.String()
	}
}
