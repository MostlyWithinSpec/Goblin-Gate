// Package nft renders the firewall policy, objects, NAT and port forwards to
// a single nftables ruleset file.
//
// The file replaces two tables atomically when loaded with `nft -f`:
//
//   - table inet goblin: filter chains input/forward/output, object sets
//     (addr_*, addrgrp_*, svc_*, svcgrp_*, policy_<id>_{src,dst,svc}),
//     threat feed sets (feed_*), and one named counter per policy
//     (policy_<id>) and port forward (portfwd_<name>).
//   - table ip goblin_nat: DNAT for port forwards, masquerade for nat=true policies.
//
// Other tables (for example ones created by Docker) are left alone.
//
// Re-applying the file recreates both tables, so policy counters and runtime
// set elements (feed_*) start from zero after every commit. Consumers that
// need continuity must snapshot them before apply (stats.PolicyTracker treats
// a drop as a reset)
// or re-populate them after apply (feed manager, task 028).
//
// Logged policies send packets to NFLOG group LogGroup with the prefix
// "GG:<id>:"; ulogd2 writes them as JSON for internal/logs (see
// packaging/ulogd).
package nft

import (
	"fmt"
	"slices"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render"
)

// Path is where the ruleset is written; the applier loads it with `nft -f`.
const Path = "/etc/nftables.d/goblin.nft"

// Table names used by the ruleset.
const (
	FilterTable = "goblin"     // family inet
	NATTable    = "goblin_nat" // family ip
	LogPrefix   = "GG:%d:"     // fmt pattern for a policy's log prefix
	LogGroup    = 1            // NFLOG group ulogd2 listens on
	// tailscaleDirectPort is tailscaled's UDP port (tailscale.DirectPort).
	tailscaleDirectPort = 41641
	// WebFilterProxyPort is goblind's web filter DNS proxy
	// (internal/webfilter.ProxyPort).
	WebFilterProxyPort = 5300
	DefaultPort        = 443 // admin port when system.admin_port is 0
	sshPort            = 22  // port opened by allow_access "ssh"
	maxComment         = 128 // nftables comment length limit
	headerLine         = "#!/usr/sbin/nft -f"
)

// Packet and connection marks. Bits are reserved here so later features
// do not collide:
//
//	meta mark 0x00000001  masquerade this packet (set by a nat=true policy in
//	                      forward, consumed in goblin_nat postrouting)
//	ct mark   0x00000001  connection matched a policy with an IPS profile
//	ct mark   0x0000ff00  which IPS profile: index+1 (render.IPSProfilesInUse);
//	                      every packet of such a connection is queued to
//	                      that profile's Suricata (task 031)
//	ct mark   0x00ff0000  reserved for web filter profile ids (task 033)
//	ct mark   0xff000000  reserved for app control profile ids (task 035)
const (
	MarkMasquerade   uint32 = 0x00000001
	CtMarkIPS        uint32 = 0x00000001
	CtMarkIPSProfile uint32 = 0x0000ff00
)

// Renderer implements commit.Renderer for nftables.
type Renderer struct{}

// Name returns "nft".
func (Renderer) Name() string { return "nft" }

// Render returns the ruleset file keyed by Path. It never touches the OS.
func (Renderer) Render(cfg *config.Config) (map[string][]byte, error) {
	b, err := build(cfg)
	if err != nil {
		return nil, fmt.Errorf("render nft: %w", err)
	}
	return map[string][]byte{Path: b}, nil
}

// ruleset collects the pieces of both tables before writing them out.
type ruleset struct {
	cfg      *config.Config
	devs     map[string]string
	names    *namer
	objs     *objects
	sets     []setDecl
	counters []counterDecl
	input    []string
	forward  []string
	feedSet  map[string]string // feed name -> set, enabled ip feeds only
	feedHit  map[string]string // feed name -> hit counter
	feedFwd  []string          // chain threat_feeds (forward)
	feedIn   []string          // chain threat_feeds_input
	dnsRedir []string          // goblin_nat chain dns_redirect
	ips      map[string]render.IPSUse
	ipsQueue []string // top of forward: keep queueing IPS connections
	spoof    []string // input and forward: anti-spoofing drops
	dnsFwd   []string // forward: no DNS-over-TLS around the filter
	pre      []string // goblin_nat prerouting
}

type counterDecl struct{ name, comment string }

func build(cfg *config.Config) ([]byte, error) {
	r := &ruleset{cfg: cfg, devs: render.Devices(cfg), names: newNamer()}
	objs, err := resolveObjects(cfg, r.names)
	if err != nil {
		return nil, err
	}
	r.objs = objs
	r.sets = append(r.sets, objs.sets...)
	r.feedSets()
	r.dnsFilter()
	r.ipsQueues()
	r.tailscale()
	r.wireguard()
	r.inputRules()
	if err := r.portForwards(); err != nil {
		return nil, err
	}
	if err := r.policies(); err != nil {
		return nil, err
	}
	return r.write(), nil
}

// FeedNames maps each enabled IP threat feed to its set (feed_<name>) and
// hit counter (feedhit_<name>) in table inet goblin. The feed syncer uses it
// to fill the sets at runtime; feeds of other types have no set.
func FeedNames(cfg *config.Config) (sets, counters map[string]string) {
	sets, counters = map[string]string{}, map[string]string{}
	n := newNamer()
	feeds := append([]config.Feed(nil), cfg.Feeds...)
	sortBy(feeds, func(f config.Feed) string { return f.Name })
	for _, f := range feeds {
		if f.Enabled && f.Type == "ip" {
			sets[f.Name] = n.get("feed_", f.Name)
			counters[f.Name] = n.get("feedhit_", f.Name)
		}
	}
	return sets, counters
}

// feedSets declares an empty set and a hit counter per enabled IP threat
// feed (filled at runtime, see FeedBatch), and the block_wan drop rules.
func (r *ruleset) feedSets() {
	r.feedSet, r.feedHit = FeedNames(r.cfg)
	feeds := append([]config.Feed(nil), r.cfg.Feeds...)
	sortBy(feeds, func(f config.Feed) string { return f.Name })
	wan := r.wanMatch()
	for _, f := range feeds {
		set, ok := r.feedSet[f.Name]
		if !ok {
			continue
		}
		r.names.used[set], r.names.used[r.feedHit[f.Name]] = true, true
		r.sets = append(r.sets, setDecl{
			name:    set,
			typ:     "ipv4_addr",
			autoMrg: true,
			comment: "threat feed " + f.Name + " (filled at runtime)",
		})
		hit := r.feedHit[f.Name]
		r.counters = append(r.counters, counterDecl{hit, "threat feed " + f.Name + " drops"})
		if !f.BlockWAN || wan == "" {
			continue
		}
		drop := fmt.Sprintf("counter name %s drop comment %s", quote(hit), quote("feed "+f.Name+": all wan traffic"))
		r.feedFwd = append(r.feedFwd,
			fmt.Sprintf("iifname %s ip saddr @%s %s", wan, set, drop),
			fmt.Sprintf("oifname %s ip daddr @%s %s", wan, set, drop))
		r.feedIn = append(r.feedIn, fmt.Sprintf("iifname %s ip saddr @%s %s", wan, set, drop))
	}
}

// wanMatch is the device list ("eth0" or { "eth0", "eth0.7" }) of the
// enabled WAN interfaces and VLANs on them; "" if there are none.
func (r *ruleset) wanMatch() string {
	wan := map[string]bool{}
	var devs []string
	for _, i := range r.cfg.Interfaces {
		wan[i.Name] = i.Role == "wan"
		if i.Role == "wan" && i.Enabled {
			if d, ok := r.devs[i.Name]; ok {
				devs = append(devs, d)
			}
		}
	}
	for _, v := range r.cfg.VLANs {
		if wan[v.Parent] && v.Enabled {
			if d, ok := r.devs[v.Name]; ok {
				devs = append(devs, d)
			}
		}
	}
	slices.Sort(devs)
	switch len(devs) {
	case 0:
		return ""
	case 1:
		return quote(devs[0])
	}
	q := make([]string, len(devs))
	for i, d := range devs {
		q[i] = quote(d)
	}
	return "{ " + strings.Join(q, ", ") + " }"
}

// devMatch renders an iifname/oifname match for interface names; "" means any.
func (r *ruleset) devMatch(key string, names []string) (string, error) {
	var devs []string
	for _, n := range names {
		if isAll(n) {
			return "", nil
		}
		d, ok := r.devs[n]
		if !ok {
			return "", fmt.Errorf("unknown interface %q", n)
		}
		devs = append(devs, quote(d))
	}
	switch len(devs) {
	case 0:
		return "", nil
	case 1:
		return key + " " + devs[0], nil
	}
	return key + " { " + strings.Join(dedupe(devs), ", ") + " }", nil
}

// quote returns s as an nft string literal, dropping characters nft cannot carry.
func quote(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '"' || r == '\\' || r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	if len(s) > maxComment {
		s = s[:maxComment]
	}
	return `"` + s + `"`
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// sortBy stable-sorts s by a string key.
func sortBy[T any](s []T, key func(T) string) {
	slices.SortStableFunc(s, func(a, b T) int { return strings.Compare(key(a), key(b)) })
}

func joinNonEmpty(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " ")
}
