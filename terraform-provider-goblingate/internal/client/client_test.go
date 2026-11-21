package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGate answers the few routes the client uses and records what happened.
type fakeGate struct {
	mu         sync.Mutex
	dirty      int
	patches    []string
	commits    []map[string]any
	confirms   int
	discards   int
	failPatch  string // a path whose PATCH fails
	failCommit bool
}

func (g *fakeGate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer tok" {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"authentication required"}}`))
		return
	}
	switch r.Method + " " + r.URL.Path {
	case "GET /api/v1/config/diff":
		ch := []map[string]string{}
		for range g.dirty {
			ch = append(ch, map[string]string{"path": "system.hostname"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"count": g.dirty, "changes": ch})
	case "GET /api/v1/config/candidate":
		_, _ = w.Write([]byte(`{"addresses":[]}`))
	case "PATCH /api/v1/config/candidate":
		var p struct{ Path string }
		_ = json.NewDecoder(r.Body).Decode(&p)
		if p.Path == g.failPatch {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"code":"invalid_patch","message":"could not apply the change","details":"bad value"}}`))
			return
		}
		g.patches = append(g.patches, p.Path)
		_, _ = w.Write([]byte(`{"status":"ok","validation_errors":[]}`))
	case "POST /api/v1/commit":
		if g.failCommit {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"error":{"code":"validation_failed","message":"the config has errors; nothing was applied","details":[{"path":"policies[0].src_addr[0]","message":"unknown src_addr reference \"x\""}]}}`))
			return
		}
		var c map[string]any
		_ = json.NewDecoder(r.Body).Decode(&c)
		g.commits = append(g.commits, c)
		_, _ = w.Write([]byte(`{"revision":7}`))
	case "POST /api/v1/commit/confirm":
		g.confirms++
		_, _ = w.Write([]byte(`{"status":"confirmed"}`))
	case "POST /api/v1/commit/discard":
		g.discards++
		_, _ = w.Write([]byte(`{"status":"discarded"}`))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func newTest(t *testing.T, g *fakeGate, confirm int) *Client {
	t.Helper()
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	c, err := New(Options{URL: srv.URL, Token: "tok", ConfirmMinutes: confirm, Window: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func set(path string) func(Edit) error {
	return func(e Edit) error {
		if _, err := e.Candidate(); err != nil {
			return err
		}
		return e.Set(path, map[string]any{"name": "x"})
	}
}

func parallel(c *Client, changes map[string]string) map[string]error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	out := map[string]error{}
	for summary, path := range changes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := c.Change(context.Background(), summary, set(path))
			mu.Lock()
			out[summary] = err
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

func TestParallelChangesAreOneCommit(t *testing.T) {
	g := &fakeGate{}
	c := newTest(t, g, 0)
	errs := parallel(c, map[string]string{
		"create address a": "addresses[name=a]",
		"create address b": "addresses[name=b]",
		"create service c": "services[name=c]",
	})
	for s, err := range errs {
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	if len(g.commits) != 1 || len(g.patches) != 3 {
		t.Fatalf("commits %d patches %d, want 1 and 3", len(g.commits), len(g.patches))
	}
	msg, _ := g.commits[0]["comment"].(string)
	for _, want := range []string{"terraform: ", "create address a", "create address b", "create service c"} {
		if !strings.Contains(msg, want) {
			t.Errorf("comment %q lacks %q", msg, want)
		}
	}
	if _, ok := g.commits[0]["confirm_minutes"]; ok || g.confirms != 0 {
		t.Error("no confirm timer was asked for")
	}
	// a later change is a commit of its own
	if err := c.Change(context.Background(), "delete address a", set("addresses[name=a]")); err != nil {
		t.Fatal(err)
	}
	if len(g.commits) != 2 {
		t.Fatalf("commits %d, want 2", len(g.commits))
	}
}

func TestConfirm(t *testing.T) {
	g := &fakeGate{}
	c := newTest(t, g, 5)
	if err := c.Change(context.Background(), "create address a", set("addresses[name=a]")); err != nil {
		t.Fatal(err)
	}
	if g.commits[0]["confirm_minutes"] != float64(5) || g.confirms != 1 {
		t.Fatalf("commit %v confirms %d", g.commits[0], g.confirms)
	}
}

func TestRefusesSomeoneElsesChanges(t *testing.T) {
	g := &fakeGate{dirty: 2}
	c := newTest(t, g, 0)
	err := c.Change(context.Background(), "create address a", set("addresses[name=a]"))
	if err == nil || !strings.Contains(err.Error(), "2 uncommitted change(s) (system.hostname, system.hostname)") {
		t.Fatalf("err = %v", err)
	}
	if len(g.patches) != 0 || len(g.commits) != 0 || g.discards != 0 {
		t.Fatalf("touched the candidate: %+v", g)
	}
}

func TestFailedChangeFailsTheBatchAndDiscards(t *testing.T) {
	g := &fakeGate{failPatch: "services[name=c]"}
	c := newTest(t, g, 0)
	errs := parallel(c, map[string]string{
		"create address a": "addresses[name=a]",
		"create service c": "services[name=c]",
	})
	for s, err := range errs {
		if err == nil || !strings.Contains(err.Error(), "create service c: could not apply the change (HTTP 400, invalid_patch): bad value") {
			t.Errorf("%s: err = %v", s, err)
		}
	}
	if len(g.commits) != 0 || g.discards != 1 {
		t.Fatalf("commits %d discards %d", len(g.commits), g.discards)
	}
}

func TestInvalidCommitIsDiscardedWithTheProblems(t *testing.T) {
	g := &fakeGate{failCommit: true}
	c := newTest(t, g, 0)
	err := c.Change(context.Background(), "create policy 3 (x)", set("policies[id=3]"))
	if err == nil || !strings.Contains(err.Error(), `policies[0].src_addr[0]: unknown src_addr reference "x"`) {
		t.Fatalf("err = %v", err)
	}
	if g.discards != 1 {
		t.Fatalf("discards %d", g.discards)
	}
}

func TestBadToken(t *testing.T) {
	g := &fakeGate{}
	srv := httptest.NewServer(g)
	defer srv.Close()
	c, _ := New(Options{URL: srv.URL, Token: "wrong"})
	_, err := c.Running(context.Background())
	if err == nil || !strings.Contains(err.Error(), "check the token") {
		t.Fatalf("err = %v", err)
	}
}

func TestOptions(t *testing.T) {
	for _, o := range []Options{{}, {URL: "gate.lan"}, {URL: "ftp://gate"}, {URL: "https://gate", ConfirmMinutes: 2000}, {URL: "https://gate", CAFile: "/nonexistent"}} {
		if _, err := New(o); err == nil {
			t.Errorf("%+v accepted", o)
		}
	}
}

func TestCommentIsCut(t *testing.T) {
	var batch []*op
	for range 50 {
		batch = append(batch, &op{summary: "create address some-long-address-name"})
	}
	s := comment(batch)
	if len(s) > 420 || !strings.HasSuffix(s, "more") {
		t.Fatalf("comment %d bytes: %q", len(s), s)
	}
}
