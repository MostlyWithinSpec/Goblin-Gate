// Package stats provides live numbers for the API: policy hit counters from
// nftables and system load from /proc. Historical event statistics live in
// internal/logs (Store.Query).
package stats

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Counter is one nft named counter.
type Counter struct {
	Packets uint64 `json:"packets"`
	Bytes   uint64 `json:"bytes"`
}

// CounterSource reads the policy counters, keyed by policy id.
type CounterSource interface {
	PolicyCounters(ctx context.Context) (map[uint32]Counter, error)
}

// FakeCounters is a CounterSource for tests and --dev mode.
type FakeCounters struct {
	mu       sync.Mutex
	Counters map[uint32]Counter
	Feeds    map[string]Counter // by nft counter name
	Err      error
}

// PolicyCounters returns a copy of f.Counters.
func (f *FakeCounters) PolicyCounters(context.Context) (map[uint32]Counter, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return nil, f.Err
	}
	out := make(map[uint32]Counter, len(f.Counters))
	for k, v := range f.Counters {
		out[k] = v
	}
	return out, nil
}

// Set replaces one counter.
func (f *FakeCounters) Set(id uint32, c Counter) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Counters == nil {
		f.Counters = map[uint32]Counter{}
	}
	f.Counters[id] = c
}

// NFTCounters reads the policy_<id> counters of table inet goblin (see
// internal/render/nft). Run defaults to executing nft.
type NFTCounters struct {
	Run func(ctx context.Context) ([]byte, error)
}

// PolicyCounters runs `nft -j list counters table inet goblin`.
func (n NFTCounters) PolicyCounters(ctx context.Context) (map[uint32]Counter, error) {
	data, err := n.list(ctx)
	if err != nil {
		return nil, err
	}
	return ParseNFTCounters(data)
}

func (n NFTCounters) list(ctx context.Context) ([]byte, error) {
	run := n.Run
	if run == nil {
		run = func(ctx context.Context) ([]byte, error) {
			out, err := exec.CommandContext(ctx, "nft", "-j", "list", "counters", "table", "inet", "goblin").Output()
			if ee, ok := err.(*exec.ExitError); ok {
				return nil, fmt.Errorf("nft: %w: %s", err, strings.TrimSpace(string(ee.Stderr)))
			}
			return out, err
		}
	}
	return run(ctx)
}

// ParseNFTCounters extracts policy_<id> counters from nft JSON output:
// {"nftables":[{"metainfo":{...}},{"counter":{"name":"policy_1","packets":3,"bytes":180,...}}]}.
// Other counters (port forwards, feeds) are ignored.
func ParseNFTCounters(data []byte) (map[uint32]Counter, error) {
	named, err := parseNamedCounters(data)
	if err != nil {
		return nil, err
	}
	out := map[uint32]Counter{}
	for name, c := range named {
		idStr, ok := strings.CutPrefix(name, "policy_")
		if !ok {
			continue
		}
		id, err := strconv.ParseUint(idStr, 10, 32)
		if err != nil {
			continue
		}
		out[uint32(id)] = c
	}
	return out, nil
}

// ParseNFTFeedCounters returns the feedhit_* counters by counter name (map
// them to feed names with nft.FeedNames).
func ParseNFTFeedCounters(data []byte) (map[string]Counter, error) {
	named, err := parseNamedCounters(data)
	if err != nil {
		return nil, err
	}
	out := map[string]Counter{}
	for name, c := range named {
		if strings.HasPrefix(name, "feedhit_") {
			out[name] = c
		}
	}
	return out, nil
}

func parseNamedCounters(data []byte) (map[string]Counter, error) {
	var doc struct {
		Nftables []struct {
			Counter *struct {
				Name    string `json:"name"`
				Table   string `json:"table"`
				Packets uint64 `json:"packets"`
				Bytes   uint64 `json:"bytes"`
			} `json:"counter"`
		} `json:"nftables"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("nft counters: %w", err)
	}
	out := map[string]Counter{}
	for _, item := range doc.Nftables {
		c := item.Counter
		if c == nil || (c.Table != "" && c.Table != "goblin") {
			continue
		}
		out[c.Name] = Counter{Packets: c.Packets, Bytes: c.Bytes}
	}
	return out, nil
}

// FeedCounterSource reads the threat feed hit counters, keyed by nft
// counter name (feedhit_<name>).
type FeedCounterSource interface {
	FeedCounters(ctx context.Context) (map[string]Counter, error)
}

// FeedCounters implements FeedCounterSource.
func (n NFTCounters) FeedCounters(ctx context.Context) (map[string]Counter, error) {
	data, err := n.list(ctx)
	if err != nil {
		return nil, err
	}
	return ParseNFTFeedCounters(data)
}

// FeedCounters implements FeedCounterSource for tests and --dev: f.Feeds.
func (f *FakeCounters) FeedCounters(context.Context) (map[string]Counter, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return nil, f.Err
	}
	out := make(map[string]Counter, len(f.Feeds))
	for k, v := range f.Feeds {
		out[k] = v
	}
	return out, nil
}

// PolicyStat is one row of GET /api/v1/stats/policies.
type PolicyStat struct {
	ID      uint32    `json:"id"`
	Packets uint64    `json:"packets"`
	Bytes   uint64    `json:"bytes"`
	LastHit time.Time `json:"last_hit,omitzero"`
}

// PolicyTracker adds last-hit times to a CounterSource: nft has none, so a
// policy was hit when its packet count went up since the previous read.
// Counters restart from zero when a commit reloads the ruleset; a count
// below the previous one is treated as such a reset.
type PolicyTracker struct {
	Source CounterSource
	Now    func() time.Time

	mu      sync.Mutex
	prev    map[uint32]Counter
	lastHit map[uint32]time.Time
}

// Read returns the counters sorted by policy id.
func (t *PolicyTracker) Read(ctx context.Context) ([]PolicyStat, error) {
	cur, err := t.Source.PolicyCounters(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if t.Now != nil {
		now = t.Now()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.lastHit == nil {
		t.lastHit = map[uint32]time.Time{}
	}
	out := make([]PolicyStat, 0, len(cur))
	for id, c := range cur {
		p, seen := t.prev[id]
		switch {
		case !seen:
			// first sight: nothing to compare with, so no last hit yet
		case c.Packets > p.Packets, c.Packets < p.Packets && c.Packets > 0:
			t.lastHit[id] = now
		}
		out = append(out, PolicyStat{ID: id, Packets: c.Packets, Bytes: c.Bytes, LastHit: t.lastHit[id]})
	}
	t.prev = cur
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
