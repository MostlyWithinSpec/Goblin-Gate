// Package blocks keeps temporary blocks: addresses dropped in both
// directions until a deadline. It is the only way automation (plugins such
// as Goblin Sentry) may cut traffic, so it refuses protected targets and
// rate limits each source. Blocks live in the nft sets block_tmp and
// block_tmp6 (internal/render/nft/blocks.go), whose elements expire in the
// kernel on their own, and in a JSON file so they survive restarts and
// ruleset reloads.
package blocks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/render/nft"
)

// Limits.
const (
	MaxMinutes = 10080 // a week
	// PluginRate is how many blocks one plugin may add per hour.
	PluginRate = 30
	// AdminRate is how many blocks one admin may add per hour.
	AdminRate = 300
	// MaxBlocks caps the active blocks (the sets stay small).
	MaxBlocks = 10000
)

// DefaultPath is the store on the box.
const DefaultPath = "/var/lib/goblingate/blocks.json"

// Block is one temporary block.
type Block struct {
	IP        netip.Addr `json:"ip"`
	Until     time.Time  `json:"until"`
	Reason    string     `json:"reason"`
	Source    string     `json:"source"`     // admin | plugin:<name>
	CreatedBy string     `json:"created_by"` // admin user or plugin name
	Created   time.Time  `json:"created"`
}

// ErrRateLimited is returned when a source added too many blocks.
var ErrRateLimited = errors.New("too many blocks from this source in the last hour")

// ErrTooMany is returned when MaxBlocks are active.
var ErrTooMany = errors.New("too many active blocks")

// Manager holds the blocks and keeps the kernel sets in line.
type Manager struct {
	Path string
	// Run loads an nft script (default: nft -f -).
	Run    func(ctx context.Context, script []byte) error
	Now    func() time.Time
	Logger *slog.Logger

	mu     sync.Mutex
	loaded bool
	blocks map[netip.Addr]Block
	recent map[string][]time.Time // source/created_by -> times of its last adds
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Manager) logger() *slog.Logger {
	if m.Logger != nil {
		return m.Logger
	}
	return slog.Default()
}

func (m *Manager) run(ctx context.Context, script []byte) error {
	if len(script) == 0 {
		return nil
	}
	if m.Run != nil {
		return m.Run(ctx, script)
	}
	cmd := exec.CommandContext(ctx, "nft", "-f", "-")
	cmd.Stdin = bytes.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("nft: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// loadLocked reads the store once; a missing file is an empty store.
func (m *Manager) loadLocked() {
	if m.loaded {
		return
	}
	m.loaded = true
	m.blocks = map[netip.Addr]Block{}
	m.recent = map[string][]time.Time{}
	data, err := os.ReadFile(m.Path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			m.logger().Warn("blocks: reading the store failed", "error", err)
		}
		return
	}
	var list []Block
	if err := json.Unmarshal(data, &list); err != nil {
		m.logger().Warn("blocks: the store is not valid JSON, starting empty", "error", err)
		return
	}
	for _, b := range list {
		m.blocks[b.IP] = b
	}
}

// pruneLocked forgets expired blocks (the kernel already dropped them).
func (m *Manager) pruneLocked() {
	now := m.now()
	for ip, b := range m.blocks {
		if !now.Before(b.Until) {
			delete(m.blocks, ip)
		}
	}
}

func (m *Manager) listLocked() []Block {
	out := make([]Block, 0, len(m.blocks))
	for _, b := range m.blocks {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Until.Equal(out[j].Until) {
			return out[i].Until.Before(out[j].Until)
		}
		return out[i].IP.Less(out[j].IP)
	})
	return out
}

func (m *Manager) saveLocked() error {
	data, err := json.MarshalIndent(m.listLocked(), "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.Path), 0o750); err != nil {
		return err
	}
	tmp := m.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, m.Path)
}

// List returns the active blocks, soonest to expire first.
func (m *Manager) List() []Block {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadLocked()
	m.pruneLocked()
	return m.listLocked()
}

// Get returns the active block of ip.
func (m *Manager) Get(ip netip.Addr) (Block, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadLocked()
	m.pruneLocked()
	b, ok := m.blocks[ip.Unmap()]
	return b, ok
}

// Add blocks b.IP for d (1 minute to MaxMinutes) and returns the stored
// block. Blocking an address again replaces the earlier block. limit is how
// many blocks one requester (source and created_by) may add per hour
// (0 = no limit). Callers check protected targets
// first (Protected).
func (m *Manager) Add(ctx context.Context, b Block, d time.Duration, limit int) (Block, error) {
	if d < time.Minute || d > MaxMinutes*time.Minute {
		return Block{}, fmt.Errorf("a block lasts 1 to %d minutes", MaxMinutes)
	}
	b.IP = b.IP.Unmap()
	if !b.IP.IsValid() {
		return Block{}, errors.New("not an IP address")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadLocked()
	m.pruneLocked()
	now := m.now()
	key := b.Source + "/" + b.CreatedBy
	if limit > 0 {
		var keep []time.Time
		for _, t := range m.recent[key] {
			if now.Sub(t) < time.Hour {
				keep = append(keep, t)
			}
		}
		m.recent[key] = keep
		if len(keep) >= limit {
			return Block{}, ErrRateLimited
		}
	}
	if _, exists := m.blocks[b.IP]; !exists && len(m.blocks) >= MaxBlocks {
		return Block{}, ErrTooMany
	}
	b.Created = now
	b.Until = now.Add(d)
	if err := m.run(ctx, nft.BlockScript(map[netip.Addr]time.Duration{b.IP: d}, nil)); err != nil {
		return Block{}, err
	}
	m.blocks[b.IP] = b
	m.recent[key] = append(m.recent[key], now)
	if err := m.saveLocked(); err != nil {
		m.logger().Warn("blocks: saving the store failed", "error", err)
	}
	return b, nil
}

// Remove lifts the block of ip; false when there was none.
func (m *Manager) Remove(ctx context.Context, ip netip.Addr) (bool, error) {
	ip = ip.Unmap()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadLocked()
	m.pruneLocked()
	if _, ok := m.blocks[ip]; !ok {
		return false, nil
	}
	if err := m.run(ctx, nft.BlockScript(nil, []netip.Addr{ip})); err != nil {
		return false, err
	}
	delete(m.blocks, ip)
	if err := m.saveLocked(); err != nil {
		m.logger().Warn("blocks: saving the store failed", "error", err)
	}
	return true, nil
}

// Resync puts every active block back into the kernel with its remaining
// time. Call it after each ruleset load (which empties the sets) and at
// start.
func (m *Manager) Resync(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadLocked()
	m.pruneLocked()
	if len(m.blocks) == 0 {
		return
	}
	now := m.now()
	add := make(map[netip.Addr]time.Duration, len(m.blocks))
	for ip, b := range m.blocks {
		add[ip] = b.Until.Sub(now)
	}
	if err := m.run(ctx, nft.BlockScript(add, nil)); err != nil {
		m.logger().Error("blocks: restoring the blocks failed", "blocks", len(add), "error", err)
		return
	}
	m.logger().Info("blocks: restored", "blocks", len(add))
}
