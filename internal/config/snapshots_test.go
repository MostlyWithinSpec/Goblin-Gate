package config

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestSaveSnapshotBasics(t *testing.T) {
	s := newTestStore(t)
	cfg := Default()

	if err := s.SaveSnapshot("pre-change", "before the upgrade", "alice", 0, cfg); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	got, err := s.ListSnapshots()
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 snapshot, got %d", len(got))
	}
	if got[0].Name != "pre-change" || got[0].Comment != "before the upgrade" || got[0].CreatedBy != "alice" || got[0].Revision != 0 {
		t.Fatalf("unexpected summary: %+v", got[0])
	}

	full, err := s.GetSnapshot("pre-change")
	if err != nil {
		t.Fatalf("GetSnapshot: %v", err)
	}
	if full.Config == nil {
		t.Fatal("snapshot has no config")
	}
	if full.Created == "" {
		t.Fatal("snapshot created is empty")
	}
}

func TestSaveSnapshotDuplicate(t *testing.T) {
	s := newTestStore(t)
	cfg := Default()
	if err := s.SaveSnapshot("a", "", "u", 0, cfg); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if err := s.SaveSnapshot("a", "", "u", 0, cfg); !errors.Is(err, ErrSnapshotExists) {
		t.Fatalf("want ErrSnapshotExists, got %v", err)
	}
}

func TestSaveSnapshotNameRules(t *testing.T) {
	s := newTestStore(t)
	cfg := Default()
	for _, name := range []string{"", "has space", strings.Repeat("a", 64), "bad/name", "badname!"} {
		if err := s.SaveSnapshot(name, "", "u", 0, cfg); err == nil {
			t.Fatalf("name %q: want error, got nil", name)
		}
	}
	for _, name := range []string{"a", "A-Z_0.9", strings.Repeat("x", 63)} {
		if err := s.SaveSnapshot(name, "", "u", 0, cfg); err != nil {
			t.Fatalf("name %q: unexpected error %v", name, err)
		}
	}
}

func TestSaveSnapshotCap(t *testing.T) {
	s := newTestStore(t)
	cfg := Default()
	for i := 0; i < MaxSnapshots; i++ {
		if err := s.SaveSnapshot("s"+strconv.Itoa(i), "", "u", 0, cfg); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}
	if err := s.SaveSnapshot("overflow", "", "u", 0, cfg); !errors.Is(err, ErrTooManySnapshots) {
		t.Fatalf("want ErrTooManySnapshots, got %v", err)
	}
}

func TestDeleteSnapshot(t *testing.T) {
	s := newTestStore(t)
	cfg := Default()
	if err := s.SaveSnapshot("a", "", "u", 0, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := s.DeleteSnapshot("a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetSnapshot("a"); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("want ErrSnapshotNotFound, got %v", err)
	}
	if err := s.DeleteSnapshot("a"); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("second delete: want ErrSnapshotNotFound, got %v", err)
	}
}

func TestListSnapshotsNewestFirst(t *testing.T) {
	s := newTestStore(t)
	cfg := Default()
	if err := s.SaveSnapshot("first", "", "u", 0, cfg); err != nil {
		t.Fatalf("save first: %v", err)
	}
	if err := s.SaveSnapshot("second", "", "u", 0, cfg); err != nil {
		t.Fatalf("save second: %v", err)
	}
	got, err := s.ListSnapshots()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 || got[0].Name != "second" || got[1].Name != "first" {
		t.Fatalf("unexpected order: %+v", got)
	}
}
