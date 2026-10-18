package geoip

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/nft"
)

// DefaultSource is DB-IP's monthly IP to Country Lite file ({month} = 2026-10).
const DefaultSource = "https://download.db-ip.com/free/dbip-country-lite-{month}.csv.gz"

// Attribution is the credit the DB-IP licence (CC BY 4.0) asks for.
const Attribution = "IP geolocation by DB-IP (https://db-ip.com), CC BY 4.0"

// MinRows is the smallest database accepted from DB-IP (the real one has
// about 600k rows): a truncated or wrong file must not empty every country.
const MinRows = 100_000

// MaxDownload bounds the compressed and the uncompressed file.
const MaxDownload = 200 << 20

// CheckEvery is how often Run looks for a new month's database.
const CheckEvery = 6 * time.Hour

// Meta describes the database on disk.
type Meta struct {
	Source  string    `json:"source"` // the URL it came from
	Month   string    `json:"month"`  // 2026-10
	Rows    int       `json:"rows"`
	Fetched time.Time `json:"fetched"`
}

// Status is GET /api/v1/geoip.
type Status struct {
	Loaded      bool      `json:"loaded"`
	Month       string    `json:"month,omitempty"`
	Rows        int       `json:"rows,omitempty"`
	Fetched     time.Time `json:"fetched,omitzero"`
	Source      string    `json:"source,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	LastCheck   time.Time `json:"last_check,omitzero"`
	Used        []string  `json:"used"` // countries the running config uses
	Attribution string    `json:"attribution"`
}

// Manager owns the database. Set the exported fields before use.
type Manager struct {
	Dir    string // data dir, e.g. /var/lib/goblingate/geoip
	Client *http.Client
	Logger *slog.Logger
	Now    func() time.Time
	// RunNFT loads an nft script (default nft -f -); --dev sets a no-op.
	RunNFT func(ctx context.Context, script []byte) error

	mu        sync.Mutex
	db        *DB
	meta      Meta
	lastErr   string
	lastCheck time.Time
	fetching  sync.Mutex // one download at a time

	nftMu sync.Mutex
	sets  []nft.CountrySet // of the live ruleset
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
	return &http.Client{Timeout: 5 * time.Minute}
}

func (m *Manager) dataPath() string { return filepath.Join(m.Dir, "country.csv.gz") }
func (m *Manager) metaPath() string { return filepath.Join(m.Dir, "meta.json") }

// Load reads the database kept on disk, if any.
func (m *Manager) Load() error {
	b, err := os.ReadFile(m.metaPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var meta Meta
	if err := json.Unmarshal(b, &meta); err != nil {
		return fmt.Errorf("geoip: %s: %w", m.metaPath(), err)
	}
	f, err := os.Open(m.dataPath())
	if err != nil {
		return err
	}
	defer f.Close()
	db, err := parseGzip(f)
	if err != nil {
		return fmt.Errorf("geoip: %s: %w", m.dataPath(), err)
	}
	m.mu.Lock()
	m.db, m.meta = db, meta
	m.mu.Unlock()
	return nil
}

// Loaded reports whether a database is in memory.
func (m *Manager) Loaded() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.db != nil
}

// Lookup returns the country of a, "" when unknown or no database.
func (m *Manager) Lookup(a netip.Addr) string {
	m.mu.Lock()
	db := m.db
	m.mu.Unlock()
	if db == nil {
		return ""
	}
	return db.Lookup(a)
}

// Status reports the database and the countries live uses.
func (m *Manager) Status(live *config.Config) Status {
	m.mu.Lock()
	st := Status{Loaded: m.db != nil, Month: m.meta.Month, Rows: m.meta.Rows, Fetched: m.meta.Fetched, LastError: m.lastErr, LastCheck: m.lastCheck, Attribution: Attribution}
	if m.meta.Source != "" {
		st.Source = m.meta.Source
	}
	m.mu.Unlock()
	st.Used = []string{}
	if live != nil {
		if sets, err := nft.CountrySets(live); err == nil {
			st.Used = append(st.Used, nft.UsedCountries(sets)...)
		}
	}
	return st
}

// sourceURLs returns the URLs to try: this month's file, then last month's
// (DB-IP publishes a new one early in the month).
func (m *Manager) sourceURLs(source string) []struct{ url, month string } {
	if source == "" {
		source = DefaultSource
	}
	now := m.now().UTC()
	months := []string{now.Format("2006-01"), now.AddDate(0, 0, -now.Day()).Format("2006-01")}
	if !strings.Contains(source, "{month}") {
		months = months[:1]
	}
	var out []struct{ url, month string }
	for _, mo := range months {
		out = append(out, struct{ url, month string }{strings.ReplaceAll(source, "{month}", mo), mo})
	}
	return out
}

// Refresh downloads the database from source ("" = DB-IP), keeps it on
// disk and refills the country sets. On failure the old one stays.
func (m *Manager) Refresh(ctx context.Context, source string) error {
	m.fetching.Lock()
	defer m.fetching.Unlock()
	err := m.refresh(ctx, source)
	m.mu.Lock()
	m.lastCheck = m.now()
	m.lastErr = ""
	if err != nil {
		m.lastErr = err.Error()
	}
	m.mu.Unlock()
	if err != nil {
		m.logger().Warn("geoip: refresh failed", "error", err)
		return err
	}
	m.Resync(nil)
	return nil
}

func (m *Manager) refresh(ctx context.Context, source string) error {
	var errs []string
	for _, u := range m.sourceURLs(source) {
		m.mu.Lock()
		have := m.db != nil && m.meta.Month == u.month && m.meta.Source == redactURL(u.url)
		m.mu.Unlock()
		if have { // early in a month: last month's file is still the newest
			return nil
		}
		data, err := m.download(ctx, u.url)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		db, err := parseGzip(bytes.NewReader(data))
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", redactURL(u.url), err))
			continue
		}
		if source == "" && db.Rows() < MinRows {
			errs = append(errs, fmt.Sprintf("%s: only %d rows, want at least %d", redactURL(u.url), db.Rows(), MinRows))
			continue
		}
		meta := Meta{Source: redactURL(u.url), Month: u.month, Rows: db.Rows(), Fetched: m.now().UTC()}
		if err := m.save(data, meta); err != nil {
			return err
		}
		m.mu.Lock()
		m.db, m.meta = db, meta
		m.mu.Unlock()
		m.logger().Info("geoip: database loaded", "month", meta.Month, "rows", meta.Rows)
		return nil
	}
	return errors.New(strings.Join(errs, "; "))
}

func (m *Manager) download(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", redactURL(u), err)
	}
	resp, err := m.client().Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("%s: %w", redactURL(u), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", redactURL(u), resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxDownload+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", redactURL(u), err)
	}
	if len(data) > MaxDownload {
		return nil, fmt.Errorf("%s: larger than %d MB", redactURL(u), MaxDownload>>20)
	}
	return data, nil
}

func (m *Manager) save(data []byte, meta Meta) error {
	if err := os.MkdirAll(m.Dir, 0o755); err != nil {
		return err
	}
	if err := writeAtomic(m.dataPath(), data); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(meta, "", "  ")
	return writeAtomic(m.metaPath(), b)
}

// Ensure loads a database if there is none (a commit about to use
// countries); source is the config's geoip.source_url.
func (m *Manager) Ensure(ctx context.Context, source string) error {
	if m.Loaded() {
		return nil
	}
	return m.Refresh(ctx, source)
}

// Run keeps the database current while the running config uses countries:
// a download when there is none, then a new month's file once it is out.
// current returns the running config.
func (m *Manager) Run(ctx context.Context, current func() *config.Config) {
	t := time.NewTicker(CheckEvery)
	defer t.Stop()
	for {
		m.check(ctx, current())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (m *Manager) check(ctx context.Context, live *config.Config) {
	if live == nil {
		return
	}
	sets, err := nft.CountrySets(live)
	if err != nil || len(sets) == 0 {
		return
	}
	m.mu.Lock()
	stale := m.db == nil || m.meta.Month != m.now().UTC().Format("2006-01")
	recent := m.now().Sub(m.lastCheck) < 24*time.Hour && m.db != nil
	m.mu.Unlock()
	if stale && !recent {
		m.Refresh(ctx, live.GeoIP.SourceURL)
	}
}

// Resync refills the country sets. With a config (after every ruleset load,
// which empties them) it first learns the live sets; with nil it reuses
// them (a new database).
func (m *Manager) Resync(live *config.Config) {
	m.nftMu.Lock()
	defer m.nftMu.Unlock()
	if live != nil {
		sets, err := nft.CountrySets(live)
		if err != nil {
			m.logger().Warn("geoip: country sets", "error", err)
			return
		}
		m.sets = sets
	}
	m.mu.Lock()
	db := m.db
	m.mu.Unlock()
	if len(m.sets) == 0 || db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	start := m.now()
	if err := m.runNFT(ctx, nft.CountryBatch(m.sets, db.Ranges)); err != nil {
		m.logger().Error("geoip: loading country sets failed", "sets", len(m.sets), "error", err)
		return
	}
	m.logger().Info("geoip: country sets loaded", "sets", len(m.sets), "took", m.now().Sub(start).Round(time.Millisecond))
}

func (m *Manager) runNFT(ctx context.Context, script []byte) error {
	if m.RunNFT != nil {
		return m.RunNFT(ctx, script)
	}
	cmd := exec.CommandContext(ctx, "nft", "-f", "-")
	cmd.Stdin = bytes.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("nft: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func parseGzip(r io.Reader) (*DB, error) {
	z, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("not a gzip file: %w", err)
	}
	defer z.Close()
	return Parse(io.LimitReader(z, MaxDownload))
}

// redactURL drops credentials and the query (a mirror's token) from a URL
// shown in the status or logs.
func redactURL(s string) string {
	u, err := url.Parse(s)
	if err != nil {
		return "the source URL"
	}
	u.User = nil
	if u.RawQuery != "" {
		u.RawQuery = "…"
	}
	return u.String()
}

func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
