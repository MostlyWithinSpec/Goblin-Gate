package feeds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// Limits and defaults.
const (
	MaxDownload     = 50 << 20 // bytes
	MinRefresh      = 15 * time.Minute
	DefaultRefresh  = 60 * time.Minute
	FetchTimeout    = 2 * time.Minute
	RetryAfterError = 15 * time.Minute
	TickInterval    = time.Minute
)

// ErrUnknownFeed is returned by Refresh for a name not in the config.
var ErrUnknownFeed = errors.New("feeds: no such feed")

// Meta is the persisted state of a feed, stored next to its last good copy
// as <name>.meta.json.
type Meta struct {
	Count        int       `json:"count"`                   // entries in the last good copy
	FetchedAt    time.Time `json:"fetched_at,omitzero"`     // last good copy downloaded
	CheckedAt    time.Time `json:"checked_at,omitzero"`     // last attempt, good or not
	ETag         string    `json:"etag,omitempty"`          // of the last good copy
	LastModified string    `json:"last_modified,omitempty"` // of the last good copy
	Error        string    `json:"error,omitempty"`         // of the last attempt; "" = it worked
	URL          string    `json:"url,omitempty"`           // the last good copy came from here
}

// Status is the API view of one feed.
type Status struct {
	Name        string    `json:"name"`
	Type        string    `json:"type"`
	URL         string    `json:"url"`
	Enabled     bool      `json:"enabled"`
	NextRefresh time.Time `json:"next_refresh,omitzero"`
	Meta
}

type feed struct {
	cfg     config.Feed
	meta    Meta
	entries []string
	// lookup indexes; only one is used, by type
	prefixes []netip.Prefix
	names    map[string]bool
	next     time.Time
}

// Manager downloads the configured feeds on their schedules and keeps the
// last good copy of each in Dir. Set the exported fields before Run.
type Manager struct {
	Dir    string       // data_dir/feeds
	Client *http.Client // nil = a client with FetchTimeout
	Logger *slog.Logger
	Now    func() time.Time

	// OnUpdate is called with the entries of a feed whenever its last good
	// copy changes, and once per feed when it is first loaded from disk.
	// It must not call back into the Manager.
	OnUpdate func(name string, entries []string)

	mu      sync.Mutex
	feeds   map[string]*feed
	fetchMu sync.Mutex // one download at a time
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

func (m *Manager) client() *http.Client {
	if m.Client != nil {
		return m.Client
	}
	return &http.Client{Timeout: FetchTimeout}
}

// RefreshInterval is the schedule of a feed: refresh_minutes, at least
// MinRefresh, DefaultRefresh when unset.
func RefreshInterval(f config.Feed) time.Duration {
	if f.RefreshMinutes == 0 {
		return DefaultRefresh
	}
	return max(time.Duration(f.RefreshMinutes)*time.Minute, MinRefresh)
}

// Run keeps the feeds in step with current() (normally the running config's
// feeds) and refreshes due feeds until ctx ends. When current fails, the
// previous feed list is kept.
func (m *Manager) Run(ctx context.Context, current func() ([]config.Feed, error)) {
	t := time.NewTicker(TickInterval)
	defer t.Stop()
	for {
		if list, err := current(); err != nil {
			m.logger().Warn("feeds: could not read the feed list", "error", err)
		} else {
			m.SetFeeds(list)
		}
		m.RefreshDue(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// SetFeeds replaces the feed list. New feeds load their last good copy from
// Dir (if any) and are due according to its age; removed feeds are
// forgotten (their files stay).
func (m *Manager) SetFeeds(list []config.Feed) {
	type update struct {
		name    string
		entries []string
	}
	var loaded []update
	m.mu.Lock()
	if m.feeds == nil {
		m.feeds = map[string]*feed{}
	}
	keep := map[string]bool{}
	for _, c := range list {
		keep[c.Name] = true
		if f, ok := m.feeds[c.Name]; ok {
			changed := f.cfg.URL != c.URL || f.cfg.Format != c.Format || f.cfg.Type != c.Type || f.cfg.AuthHeader != c.AuthHeader
			f.cfg = c
			if changed {
				f.next = time.Time{} // download again now
				f.meta.ETag, f.meta.LastModified = "", ""
			}
			continue
		}
		f := &feed{cfg: c}
		if err := m.load(f); err != nil && !errors.Is(err, fs.ErrNotExist) {
			m.logger().Warn("feeds: last good copy unreadable", "feed", c.Name, "error", err)
		}
		if f.meta.URL != c.URL || f.meta.FetchedAt.IsZero() {
			f.next = time.Time{}
		} else {
			f.next = f.meta.FetchedAt.Add(RefreshInterval(c))
		}
		m.feeds[c.Name] = f
		if f.entries != nil {
			loaded = append(loaded, update{c.Name, f.entries})
		}
	}
	for name := range m.feeds {
		if !keep[name] {
			delete(m.feeds, name)
		}
	}
	m.mu.Unlock()
	if m.OnUpdate != nil {
		for _, u := range loaded {
			m.OnUpdate(u.name, u.entries)
		}
	}
}

// RefreshDue downloads every enabled feed whose next refresh has passed.
func (m *Manager) RefreshDue(ctx context.Context) {
	now := m.now()
	m.mu.Lock()
	var due []string
	for name, f := range m.feeds {
		if f.cfg.Enabled && !now.Before(f.next) {
			due = append(due, name)
		}
	}
	m.mu.Unlock()
	sort.Strings(due)
	for _, name := range due {
		if ctx.Err() != nil {
			return
		}
		if _, err := m.Refresh(ctx, name); err != nil && !errors.Is(err, ErrUnknownFeed) {
			m.logger().Warn("feeds: refresh failed", "feed", name, "error", err)
		}
	}
}

// Refresh downloads one feed now, enabled or not, and returns its status.
// A failed download keeps the last good copy; the error is also recorded in
// the status.
func (m *Manager) Refresh(ctx context.Context, name string) (Status, error) {
	m.fetchMu.Lock()
	defer m.fetchMu.Unlock()

	m.mu.Lock()
	f, ok := m.feeds[name]
	if !ok {
		m.mu.Unlock()
		return Status{}, ErrUnknownFeed
	}
	cfg, meta, lastCount := f.cfg, f.meta, len(f.entries)
	if f.entries == nil {
		lastCount = -1 // no last good copy: only the empty check applies
	}
	m.mu.Unlock()

	entries, newMeta, changed, err := m.fetch(ctx, cfg, meta, lastCount)
	now := m.now()
	newMeta.CheckedAt = now

	m.mu.Lock()
	f, ok = m.feeds[name]
	if !ok || f.cfg.URL != cfg.URL {
		// removed or re-pointed while downloading: drop the result
		m.mu.Unlock()
		return Status{}, ErrUnknownFeed
	}
	if err != nil {
		f.meta.Error = err.Error()
		f.meta.CheckedAt = now
		f.next = now.Add(min(RetryAfterError, RefreshInterval(f.cfg)))
	} else {
		f.meta = newMeta
		f.next = now.Add(RefreshInterval(f.cfg))
		if changed {
			f.setEntries(entries)
		}
	}
	st := f.status()
	toSave, saveEntries := f.meta, f.entries
	m.mu.Unlock()

	if saveErr := m.save(name, toSave, saveEntries, err == nil && changed); saveErr != nil {
		m.logger().Warn("feeds: could not save", "feed", name, "error", saveErr)
	}
	if err == nil && changed {
		m.logger().Info("feeds: updated", "feed", name, "count", len(entries))
		if m.OnUpdate != nil {
			m.OnUpdate(name, entries)
		}
	}
	return st, err
}

// fetch downloads and checks a feed. changed is false for 304 Not Modified.
func (m *Manager) fetch(ctx context.Context, cfg config.Feed, meta Meta, lastCount int) (entries []string, out Meta, changed bool, err error) {
	out = meta
	ctx, cancel := context.WithTimeout(ctx, FetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.URL, nil)
	if err != nil {
		return nil, out, false, fmt.Errorf("bad url: %w", err)
	}
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return nil, out, false, fmt.Errorf("unsupported url scheme %q", req.URL.Scheme)
	}
	req.Header.Set("User-Agent", "goblin-gate-feeds")
	if meta.URL == cfg.URL {
		if meta.ETag != "" {
			req.Header.Set("If-None-Match", meta.ETag)
		}
		if meta.LastModified != "" {
			req.Header.Set("If-Modified-Since", meta.LastModified)
		}
	}
	if name, val, ok := strings.Cut(cfg.AuthHeader, ":"); ok {
		req.Header.Set(strings.TrimSpace(name), strings.TrimSpace(val))
	}
	resp, err := m.client().Do(req)
	if err != nil {
		return nil, out, false, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotModified:
		if lastCount < 0 {
			return nil, out, false, errors.New("server said not modified but there is no saved copy")
		}
		out.Error = ""
		return nil, out, false, nil
	case http.StatusOK:
	default:
		return nil, out, false, fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxDownload+1))
	if err != nil {
		return nil, out, false, fmt.Errorf("download: %w", err)
	}
	if len(body) > MaxDownload {
		return nil, out, false, fmt.Errorf("download is larger than %d MB", MaxDownload>>20)
	}
	entries, err = Parse(body, cfg.Format, cfg.Type)
	if err != nil {
		return nil, out, false, err
	}
	if err := checkSize(len(entries), lastCount); err != nil {
		return nil, out, false, err
	}
	out = Meta{
		Count:        len(entries),
		FetchedAt:    m.now(),
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		URL:          cfg.URL,
	}
	return entries, out, true, nil
}

// checkSize rejects an update that is empty, shrinks by more than 90% or
// grows more than 10x compared with the last good copy (last < 0: none).
func checkSize(n, last int) error {
	switch {
	case n == 0:
		return errors.New("rejected: the update has no valid entries")
	case last > 0 && n*10 < last:
		return fmt.Errorf("rejected: the update has %d entries, down from %d (more than 90%% fewer)", n, last)
	case last > 0 && n > last*10:
		return fmt.Errorf("rejected: the update has %d entries, up from %d (more than 10x)", n, last)
	}
	return nil
}

func (f *feed) setEntries(entries []string) {
	f.entries = entries
	f.prefixes, f.names = nil, nil
	if f.cfg.Type == "ip" {
		f.prefixes = make([]netip.Prefix, 0, len(entries))
		for _, e := range entries {
			if p, err := netip.ParsePrefix(e); err == nil {
				f.prefixes = append(f.prefixes, p)
			} else if a, err := netip.ParseAddr(e); err == nil {
				f.prefixes = append(f.prefixes, netip.PrefixFrom(a, a.BitLen()))
			}
		}
		return
	}
	f.names = make(map[string]bool, len(entries))
	for _, e := range entries {
		f.names[e] = true
	}
}

func (f *feed) status() Status {
	st := Status{Name: f.cfg.Name, Type: f.cfg.Type, URL: f.cfg.URL, Enabled: f.cfg.Enabled, Meta: f.meta}
	if f.cfg.Enabled {
		st.NextRefresh = f.next
	}
	return st
}

// Status returns every feed's status, sorted by name.
func (m *Manager) Status() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Status, 0, len(m.feeds))
	for _, f := range m.feeds {
		out = append(out, f.status())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Entries returns a copy of a feed's last good entries.
func (m *Manager) Entries(name string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if f, ok := m.feeds[name]; ok {
		return append([]string(nil), f.entries...)
	}
	return nil
}

// Lookup returns the names of the feeds (sorted) whose last good copy
// contains value: an IP inside an ip feed's prefix, a domain or any parent
// domain in a domain feed, or an exact URL in a url feed.
func (m *Manager) Lookup(value string) []string {
	value = strings.TrimSpace(value)
	addr, addrErr := netip.ParseAddr(value)
	if addrErr == nil {
		addr = addr.Unmap()
	}
	domains := domainAndParents(value)
	urls := normalizeURLs([]string{value})

	m.mu.Lock()
	defer m.mu.Unlock()
	out := []string{}
	for name, f := range m.feeds {
		hit := false
		switch f.cfg.Type {
		case "ip":
			if addrErr == nil {
				for _, p := range f.prefixes {
					if p.Contains(addr) {
						hit = true
						break
					}
				}
			}
		case "domain":
			for _, d := range domains {
				if f.names[d] {
					hit = true
					break
				}
			}
		case "url":
			hit = len(urls) == 1 && f.names[urls[0]]
		}
		if hit {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// domainAndParents returns "a.b.example.com", "b.example.com", "example.com"
// (never a bare TLD).
func domainAndParents(v string) []string {
	d := normalizeDomains([]string{v})
	if len(d) == 0 {
		return nil
	}
	var out []string
	for s := d[0]; strings.Contains(s, "."); s = s[strings.Index(s, ".")+1:] {
		out = append(out, s)
	}
	return out
}

// Files: <name>.txt holds one entry per line, <name>.meta.json the Meta.

func (m *Manager) paths(name string) (string, string) {
	return filepath.Join(m.Dir, name+".txt"), filepath.Join(m.Dir, name+".meta.json")
}

func (m *Manager) load(f *feed) error {
	txt, metaPath := m.paths(f.cfg.Name)
	b, err := os.ReadFile(metaPath)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &f.meta); err != nil {
		return fmt.Errorf("meta: %w", err)
	}
	if f.meta.Count == 0 {
		return nil // never had a good copy
	}
	data, err := os.ReadFile(txt)
	if err != nil {
		return err
	}
	entries := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	f.setEntries(entries)
	f.meta.Count = len(entries)
	return nil
}

func (m *Manager) save(name string, meta Meta, entries []string, withEntries bool) error {
	if m.Dir == "" {
		return nil
	}
	if err := os.MkdirAll(m.Dir, 0o750); err != nil {
		return err
	}
	txt, metaPath := m.paths(name)
	if withEntries {
		if err := writeAtomic(txt, []byte(strings.Join(entries, "\n")+"\n")); err != nil {
			return err
		}
	}
	b, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(metaPath, append(b, '\n'))
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
