package config

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStoreFreshDefaults(t *testing.T) {
	s := newStore(t)
	run, err := s.Running()
	if err != nil {
		t.Fatal(err)
	}
	if d := Diff(Default(), run); len(d) != 0 {
		t.Errorf("fresh Running() differs from Default(): %v", d)
	}
	cand, err := s.Candidate()
	if err != nil {
		t.Fatal(err)
	}
	if d := Diff(run, cand); len(d) != 0 {
		t.Errorf("fresh Candidate() differs from Running(): %v", d)
	}
	revs, err := s.ListRevisions()
	if err != nil || len(revs) != 0 {
		t.Errorf("ListRevisions() = %v, %v; want none", revs, err)
	}
}

func TestStoreCandidate(t *testing.T) {
	s := newStore(t)
	cfg := Default()
	cfg.System.Hostname = "edited"
	if err := s.SetCandidate(cfg); err != nil {
		t.Fatal(err)
	}
	got, err := s.Candidate()
	if err != nil {
		t.Fatal(err)
	}
	if got.System.Hostname != "edited" {
		t.Errorf("Candidate().Hostname = %q, want edited", got.System.Hostname)
	}
	run, _ := s.Running()
	if run.System.Hostname == "edited" {
		t.Error("SetCandidate changed the running config")
	}
	if err := s.DiscardCandidate(); err != nil {
		t.Fatal(err)
	}
	if err := s.DiscardCandidate(); err != nil {
		t.Errorf("second DiscardCandidate: %v", err)
	}
	got, _ = s.Candidate()
	if got.System.Hostname == "edited" {
		t.Error("candidate survived DiscardCandidate")
	}
	if err := s.SetCandidate(nil); err == nil {
		t.Error("SetCandidate(nil) succeeded")
	}
}

func TestStoreAtomicWriteSurvivesLeftoverTmp(t *testing.T) {
	s := newStore(t)
	// A crashed run left half-written temp files behind.
	for _, p := range []string{s.candidatePath(), s.runningPath(), s.indexPath(), s.revisionPath(1)} {
		if err := os.WriteFile(p+".tmp", []byte("{garbage"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := Default()
	cfg.System.Hostname = "after-crash"
	if err := s.SetCandidate(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveRevision(cfg, "admin", "recover"); err != nil {
		t.Fatal(err)
	}
	for _, get := range []func() (*Config, error){s.Candidate, s.Running} {
		got, err := get()
		if err != nil {
			t.Fatal(err)
		}
		if got.System.Hostname != "after-crash" {
			t.Errorf("Hostname = %q, want after-crash", got.System.Hostname)
		}
	}
	for _, p := range []string{s.candidatePath(), s.runningPath(), s.indexPath(), s.revisionPath(1)} {
		if _, err := os.Stat(p + ".tmp"); !os.IsNotExist(err) {
			t.Errorf("%s.tmp still exists (err %v)", filepath.Base(p), err)
		}
	}
}

func TestStoreRevisions(t *testing.T) {
	s := newStore(t)
	for i, host := range []string{"r1", "r2", "r3"} {
		cfg := Default()
		cfg.System.Hostname = host
		rev, err := s.SaveRevision(cfg, "admin", "commit "+host)
		if err != nil {
			t.Fatal(err)
		}
		if rev != i+1 {
			t.Errorf("SaveRevision #%d = %d, want %d", i+1, rev, i+1)
		}
	}
	run, _ := s.Running()
	if run.System.Hostname != "r3" {
		t.Errorf("Running().Hostname = %q, want r3", run.System.Hostname)
	}
	r2, err := s.Revision(2)
	if err != nil || r2.System.Hostname != "r2" {
		t.Errorf("Revision(2) = %v, %v; want hostname r2", r2, err)
	}
	if _, err := s.Revision(9); err == nil {
		t.Error("Revision(9) succeeded")
	}
	if _, err := s.Revision(0); err == nil {
		t.Error("Revision(0) succeeded")
	}
	revs, _ := s.ListRevisions()
	if len(revs) != 3 || revs[2].User != "admin" || revs[2].Comment != "commit r3" || revs[2].Time == "" {
		t.Errorf("ListRevisions() = %+v", revs)
	}

	// Reopening the directory continues the numbering.
	s2, err := New(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if rev, _ := s2.SaveRevision(Default(), "admin", ""); rev != 4 {
		t.Errorf("revision after reopen = %d, want 4", rev)
	}
}

func TestStorePrune(t *testing.T) {
	s := newStore(t)
	for i := 0; i < 5; i++ {
		if _, err := s.SaveRevision(Default(), "admin", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Prune(0); err == nil {
		t.Error("Prune(0) succeeded")
	}
	if err := s.Prune(2); err != nil {
		t.Fatal(err)
	}
	revs, _ := s.ListRevisions()
	if len(revs) != 2 || revs[0].Revision != 4 || revs[1].Revision != 5 {
		t.Fatalf("after Prune(2) revisions = %+v, want 4 and 5", revs)
	}
	for n := 1; n <= 3; n++ {
		if _, err := s.Revision(n); err == nil {
			t.Errorf("pruned revision %d still readable", n)
		}
	}
	// Numbers never get reused after pruning.
	if rev, _ := s.SaveRevision(Default(), "admin", ""); rev != 6 {
		t.Errorf("revision after prune = %d, want 6", rev)
	}
	if err := s.Prune(10); err != nil {
		t.Errorf("Prune(10) with 3 revisions: %v", err)
	}
}

func TestStoreIndexRebuiltWhenMissing(t *testing.T) {
	s := newStore(t)
	for i := 0; i < 2; i++ {
		if _, err := s.SaveRevision(Default(), "admin", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(s.indexPath()); err != nil {
		t.Fatal(err)
	}
	revs, err := s.ListRevisions()
	if err != nil || len(revs) != 2 {
		t.Fatalf("ListRevisions() = %v, %v; want 2 rebuilt entries", revs, err)
	}
	if rev, _ := s.SaveRevision(Default(), "admin", ""); rev != 3 {
		t.Errorf("revision after index loss = %d, want 3", rev)
	}
}

func TestStoreConcurrent(t *testing.T) {
	s := newStore(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.SaveRevision(Default(), "admin", ""); err != nil {
				t.Error(err)
			}
			if err := s.SetCandidate(Default()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	revs, _ := s.ListRevisions()
	if len(revs) != 8 || revs[7].Revision != 8 {
		t.Errorf("after 8 concurrent saves revisions = %+v", revs)
	}
}
