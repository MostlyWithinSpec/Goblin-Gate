package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// createdLayout is RFC 3339 with a fixed-width fraction, so the strings sort
// in time order (RFC3339Nano trims trailing zeros).
const createdLayout = "2006-01-02T15:04:05.000000000Z07:00"

// MaxSnapshots is the maximum number of lab snapshots the store will keep.
const MaxSnapshots = 50

// Snapshot is a named, timestamped copy of a config (running or candidate).
type Snapshot struct {
	Name      string  `json:"name"`
	Comment   string  `json:"comment,omitempty"`
	Created   string  `json:"created"` // RFC3339, UTC
	CreatedBy string  `json:"created_by"`
	Revision  int     `json:"revision"` // running revision it was taken from; 0 for candidate
	Config    *Config `json:"config"`
}

// SnapshotInfo is the summary form returned by list endpoints (no config body).
type SnapshotInfo struct {
	Name      string `json:"name"`
	Comment   string `json:"comment,omitempty"`
	Created   string `json:"created"`
	CreatedBy string `json:"created_by"`
	Revision  int    `json:"revision"`
}

var (
	// ErrSnapshotExists is returned when saving a snapshot with an existing name.
	ErrSnapshotExists = errors.New("snapshot already exists")
	// ErrSnapshotNotFound is returned when the named snapshot does not exist.
	ErrSnapshotNotFound = errors.New("snapshot not found")
	// ErrTooManySnapshots is returned when the 50-snapshot cap would be exceeded.
	ErrTooManySnapshots = errors.New("too many snapshots (max 50)")
)

// SaveSnapshot stores cfg under name, creating the snapshots/ directory if
// needed. The revision field records which running revision it was taken from
// (0 when saved from the candidate). It returns ErrSnapshotExists if a
// snapshot with that name already exists and ErrTooManySnapshots when 50
// snapshots exist.
func (s *Store) SaveSnapshot(name, comment, user string, revision int, cfg *Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !ValidName(name) {
		return fmt.Errorf("invalid snapshot name %q", name)
	}
	dir := s.snapshotsDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("config store: create snapshots dir: %w", err)
	}
	path := s.snapshotPath(name)
	if _, err := os.Stat(path); err == nil {
		return ErrSnapshotExists
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("config store: stat snapshot: %w", err)
	}
	n, err := s.countSnapshotsLocked()
	if err != nil {
		return err
	}
	if n >= MaxSnapshots {
		return ErrTooManySnapshots
	}

	snap := &Snapshot{
		Name:      name,
		Comment:   comment,
		Created:   time.Now().UTC().Format(createdLayout),
		CreatedBy: user,
		Revision:  revision,
		Config:    cfg,
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("config store: encode snapshot: %w", err)
	}
	if err := atomicWrite(path, append(data, '\n')); err != nil {
		return fmt.Errorf("config store: write snapshot: %w", err)
	}
	return nil
}

// GetSnapshot returns the full snapshot (including config) by name.
func (s *Store) GetSnapshot(name string) (*Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readSnapshotLocked(name)
}

// ListSnapshots returns summaries of all snapshots, newest first.
func (s *Store) ListSnapshots() ([]SnapshotInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.snapshotsDir()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config store: list snapshots: %w", err)
	}
	var out []SnapshotInfo
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		name := e.Name()[:len(e.Name())-5] // strip .json
		snap, err := s.readSnapshotLocked(name)
		if err != nil {
			continue // skip unreadable entries
		}
		out = append(out, SnapshotInfo{
			Name:      snap.Name,
			Comment:   snap.Comment,
			Created:   snap.Created,
			CreatedBy: snap.CreatedBy,
			Revision:  snap.Revision,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Created != out[j].Created {
			return out[i].Created > out[j].Created
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// DeleteSnapshot removes the named snapshot.
func (s *Store) DeleteSnapshot(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !ValidName(name) {
		return fmt.Errorf("invalid snapshot name %q", name)
	}
	path := s.snapshotPath(name)
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return ErrSnapshotNotFound
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("config store: delete snapshot: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------- helpers

func (s *Store) snapshotsDir() string { return filepath.Join(s.dir, "snapshots") }

func (s *Store) snapshotPath(name string) string {
	return filepath.Join(s.snapshotsDir(), name+".json")
}

func (s *Store) readSnapshotLocked(name string) (*Snapshot, error) {
	if !ValidName(name) {
		return nil, fmt.Errorf("invalid snapshot name %q", name)
	}
	b, err := os.ReadFile(s.snapshotPath(name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrSnapshotNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("config store: read snapshot: %w", err)
	}
	var snap Snapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return nil, fmt.Errorf("config store: parse snapshot: %w", err)
	}
	return &snap, nil
}

func (s *Store) countSnapshotsLocked() (int, error) {
	dir := s.snapshotsDir()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("config store: count snapshots: %w", err)
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			n++
		}
	}
	return n, nil
}
