package logs

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

type lineSink struct {
	mu    sync.Mutex
	lines []string
}

func (s *lineSink) add(l string) {
	s.mu.Lock()
	s.lines = append(s.lines, l)
	s.mu.Unlock()
}

func (s *lineSink) get() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.lines...)
}

// waitLines waits until the sink holds want lines and checks them.
func (s *lineSink) waitLines(t *testing.T, want ...string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got := s.get()
		if len(got) >= len(want) {
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("lines %q, want %q", got, want)
				}
			}
			if len(got) > len(want) {
				t.Fatalf("extra lines %q, want %q", got, want)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out: lines %q, want %q", s.get(), want)
}

func appendFile(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

func startTailer(t *testing.T, tl *Tailer, sink *lineSink) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- tl.Run(ctx, sink.add) }()
	return func() {
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestTailerAppendRestartTruncate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	pos := filepath.Join(dir, "tail-app.pos")
	tl := &Tailer{Path: path, PosFile: pos, Poll: 5 * time.Millisecond}

	sink := &lineSink{}
	stop := startTailer(t, tl, sink)
	time.Sleep(30 * time.Millisecond) // let the tailer see that the file is missing
	appendFile(t, path, "one\ntwo\n") // file appears after start: read from the beginning
	sink.waitLines(t, "one", "two")
	appendFile(t, path, "thr")
	time.Sleep(30 * time.Millisecond) // incomplete line is held back
	appendFile(t, path, "ee\r\n")
	sink.waitLines(t, "one", "two", "three")
	stop()

	// restart continues from the saved position
	appendFile(t, path, "four\n")
	sink = &lineSink{}
	stop = startTailer(t, tl, sink)
	sink.waitLines(t, "four")

	// truncation starts over
	if err := os.WriteFile(path, []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sink.waitLines(t, "four", "new")
	stop()
}

func TestTailerRotate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	appendFile(t, path, "old history\n")
	tl := &Tailer{Path: path, Poll: 5 * time.Millisecond} // no pos file: start at the end
	sink := &lineSink{}
	stop := startTailer(t, tl, sink)
	defer stop()
	time.Sleep(30 * time.Millisecond)
	appendFile(t, path, "a\n")
	sink.waitLines(t, "a")

	appendFile(t, path, "b\n")
	if err := os.Rename(path, path+".1"); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("Windows cannot rename a file another handle has open:", err)
		}
		t.Fatal(err)
	}
	appendFile(t, path, "c\n")
	sink.waitLines(t, "a", "b", "c")
}

func TestIngester(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(filepath.Join(dir, "logs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	nftPath := filepath.Join(dir, "goblin.json")
	eve := filepath.Join(dir, "eve.json")
	for _, f := range []struct{ path, fixture string }{{nftPath, "nftables.json"}, {eve, "suricata.json"}} {
		b, err := os.ReadFile(filepath.Join("testdata", f.fixture))
		if err != nil {
			t.Fatal(err)
		}
		appendFile(t, f.path, string(b)+"not json at all\n")
	}
	in := &Ingester{
		Store: store, PosDir: dir, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		PolicyAction: func(id string) string { return map[string]string{"3": "accept", "7": "deny"}[id] },
		FlushEvery:   10 * time.Millisecond, Poll: 5 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		// files that already exist when ingest starts for the first time
		// are read from their end, so append the events after startup
		in.Run(ctx, []Source{{Name: "nft", Path: nftPath, Parse: ParseNftables}, {Name: "eve", Path: eve, Parse: ParseSuricata}})
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	for _, f := range []struct{ path, fixture string }{{nftPath, "nftables.json"}, {eve, "suricata.json"}} {
		b, _ := os.ReadFile(filepath.Join("testdata", f.fixture))
		appendFile(t, f.path, string(b))
	}
	deadline := time.Now().Add(5 * time.Second)
	var evs []StoredEvent
	for time.Now().Before(deadline) {
		evs, _, _ = store.ListEvents(ctx, EventFilter{})
		if len(evs) >= 8 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if len(evs) != 8 {
		t.Fatalf("stored %d events, want 3 nft + 5 eve", len(evs))
	}
	actions := map[string]string{}
	for _, e := range evs {
		if e.Kind == "traffic" && e.PolicyID != "" {
			actions[e.PolicyID] = e.Action
		}
	}
	if actions["3"] != "accept" || actions["7"] != "deny" {
		t.Fatalf("policy actions %v", actions)
	}
	if _, err := os.Stat(filepath.Join(dir, "tail-nft.pos")); err != nil {
		t.Fatalf("position file: %v", err)
	}
}
