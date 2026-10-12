package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"
)

// DefaultDir is the store root used when New gets an empty directory.
const DefaultDir = "/etc/goblingate"

// RevisionInfo describes one committed revision in history/index.json.
type RevisionInfo struct {
	Revision int    `json:"revision"`
	Time     string `json:"time"` // RFC3339, UTC
	User     string `json:"user"`
	Comment  string `json:"comment,omitempty"`
}

// Store persists the running and candidate configs plus a revision history
// under a directory:
//
//	<dir>/running.json      last committed config (what the box runs)
//	<dir>/candidate.json    in-flight edits; absent until first SetCandidate
//	<dir>/history/000001.json ... one file per committed revision
//	<dir>/history/index.json     [{revision, time, user, comment}]
//
// All writes are atomic (write .tmp, fsync, rename) and all methods are safe
// for concurrent use. The store never applies anything to the system; that is
// the commit engine's job (task 005).
type Store struct {
	mu  sync.Mutex
	dir string
}

// New opens the store rooted at dir, creating it and its history/ subdirectory
// if needed. An empty dir means DefaultDir.
func New(dir string) (*Store, error) {
	if dir == "" {
		dir = DefaultDir
	}
	if err := os.MkdirAll(filepath.Join(dir, "history"), 0o755); err != nil {
		return nil, fmt.Errorf("config store: %w", err)
	}
	return &Store{dir: dir}, nil
}

// Running returns the last committed config. If none exists yet (fresh box),
// it returns Default().
func (s *Store) Running() (*Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.runningPath())
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("config store: read running config: %w", err)
	}
	return loadConfig(b)
}

// Candidate returns the in-flight config. If no candidate exists yet it
// returns a copy of Running() so the first edit starts from the running state.
func (s *Store) Candidate() (*Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.candidatePath())
	if errors.Is(err, fs.ErrNotExist) {
		return s.readRunningLocked()
	}
	if err != nil {
		return nil, fmt.Errorf("config store: read candidate config: %w", err)
	}
	return loadConfig(b)
}

// SetCandidate atomically writes the in-flight config.
func (s *Store) SetCandidate(cfg *Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := marshalConfig(cfg)
	if err != nil {
		return fmt.Errorf("config store: set candidate: %w", err)
	}
	if err := atomicWrite(s.candidatePath(), data); err != nil {
		return fmt.Errorf("config store: set candidate: %w", err)
	}
	return nil
}

// DiscardCandidate removes the in-flight config, if any.
func (s *Store) DiscardCandidate() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.candidatePath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("config store: discard candidate: %w", err)
	}
	return nil
}

// SaveRevision commits cfg as the next history revision and makes it the new
// running config. It returns the revision number (1-based, always increasing).
func (s *Store) SaveRevision(cfg *Config, user, comment string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := marshalConfig(cfg)
	if err != nil {
		return 0, fmt.Errorf("config store: save revision: %w", err)
	}
	idx, err := s.indexLocked()
	if err != nil {
		return 0, err
	}
	rev := len(idx) + 1
	if n := len(idx); n > 0 {
		rev = idx[n-1].Revision + 1
	}
	if err := atomicWrite(s.revisionPath(rev), data); err != nil {
		return 0, fmt.Errorf("config store: save revision %d: %w", rev, err)
	}
	idx = append(idx, RevisionInfo{
		Revision: rev,
		Time:     time.Now().UTC().Format(time.RFC3339),
		User:     user,
		Comment:  comment,
	})
	if err := s.writeIndexLocked(idx); err != nil {
		return 0, fmt.Errorf("config store: save revision %d: %w", rev, err)
	}
	if err := atomicWrite(s.runningPath(), data); err != nil {
		return 0, fmt.Errorf("config store: save revision %d: %w", rev, err)
	}
	return rev, nil
}

// Revision returns the config stored at revision n.
func (s *Store) Revision(n int) (*Config, error) {
	if n < 1 {
		return nil, fmt.Errorf("config store: invalid revision %d", n)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.revisionPath(n))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("config store: revision %d not found", n)
	}
	if err != nil {
		return nil, fmt.Errorf("config store: read revision %d: %w", n, err)
	}
	return loadConfig(b)
}

// ListRevisions returns all revisions in ascending order.
func (s *Store) ListRevisions() ([]RevisionInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, err := s.indexLocked()
	if err != nil {
		return nil, err
	}
	out := make([]RevisionInfo, len(idx))
	copy(out, idx)
	return out, nil
}

// Prune keeps only the newest keep revisions and deletes the rest. The running
// config is always the newest revision, so it is never pruned.
func (s *Store) Prune(keep int) error {
	if keep < 1 {
		return fmt.Errorf("config store: prune: keep must be >= 1")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	idx, err := s.indexLocked()
	if err != nil {
		return err
	}
	if len(idx) <= keep {
		return nil
	}
	drop := idx[:len(idx)-keep]
	for _, info := range drop {
		if err := os.Remove(s.revisionPath(info.Revision)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("config store: prune revision %d: %w", info.Revision, err)
		}
	}
	return s.writeIndexLocked(idx[len(idx)-keep:])
}

// ---------------------------------------------------------------- helpers

func (s *Store) runningPath() string   { return filepath.Join(s.dir, "running.json") }
func (s *Store) candidatePath() string { return filepath.Join(s.dir, "candidate.json") }
func (s *Store) historyDir() string    { return filepath.Join(s.dir, "history") }
func (s *Store) indexPath() string     { return filepath.Join(s.historyDir(), "index.json") }
func (s *Store) revisionPath(n int) string {
	return filepath.Join(s.historyDir(), fmt.Sprintf("%06d.json", n))
}

// readRunningLocked returns the running config, or Default() on a fresh box.
func (s *Store) readRunningLocked() (*Config, error) {
	b, err := os.ReadFile(s.runningPath())
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("config store: read running config: %w", err)
	}
	return loadConfig(b)
}

func loadConfig(b []byte) (*Config, error) {
	cfg, err := Load(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("config store: parse config: %w", err)
	}
	return cfg, nil
}

func marshalConfig(cfg *Config) ([]byte, error) {
	if cfg == nil {
		return nil, errors.New("nil config")
	}
	return cfg.Marshal()
}

// indexLocked returns the history index in ascending order. If index.json is
// missing it is reconstructed from the revision files on disk (crash recovery).
func (s *Store) indexLocked() ([]RevisionInfo, error) {
	b, err := os.ReadFile(s.indexPath())
	if errors.Is(err, fs.ErrNotExist) {
		return s.scanHistoryLocked()
	}
	if err != nil {
		return nil, fmt.Errorf("config store: read history index: %w", err)
	}
	var idx []RevisionInfo
	if err := json.Unmarshal(b, &idx); err != nil {
		return nil, fmt.Errorf("config store: parse history index: %w", err)
	}
	sort.Slice(idx, func(i, j int) bool { return idx[i].Revision < idx[j].Revision })
	return idx, nil
}

var revisionFileRE = regexp.MustCompile(`^(\d{6})\.json$`)

func (s *Store) scanHistoryLocked() ([]RevisionInfo, error) {
	entries, err := os.ReadDir(s.historyDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config store: scan history: %w", err)
	}
	var idx []RevisionInfo
	for _, e := range entries {
		m := revisionFileRE.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		idx = append(idx, RevisionInfo{Revision: n})
	}
	sort.Slice(idx, func(i, j int) bool { return idx[i].Revision < idx[j].Revision })
	return idx, nil
}

func (s *Store) writeIndexLocked(idx []RevisionInfo) error {
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("config store: encode history index: %w", err)
	}
	if err := atomicWrite(s.indexPath(), append(data, '\n')); err != nil {
		return fmt.Errorf("config store: write history index: %w", err)
	}
	return nil
}

// atomicWrite writes data to path atomically: it writes path+".tmp", fsyncs
// the file, renames it over path and best-effort fsyncs the directory. A
// stale .tmp left behind by a crashed run is simply overwritten.
func atomicWrite(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", tmp, err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("fsync %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename %s to %s: %w", tmp, path, err)
	}
	syncDir(filepath.Dir(path))
	return nil
}

func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	defer d.Close()
	_ = d.Sync()
}
