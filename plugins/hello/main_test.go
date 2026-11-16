package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/plugins"
	"github.com/networkgoblinlabs/goblin-gate/pkg/pluginsdk"
)

// fakeGoblind serves exactly the routes plugins may call (internal/plugins
// Routes, the table goblind enforces), so the plugin cannot reach anything else.
// An answer starting with "!" is an error: "!403 <json>".
type fakeGoblind struct {
	t     *testing.T
	mu    sync.Mutex
	calls []string
	reply map[string]string
}

func newFake(t *testing.T) (*fakeGoblind, *pluginsdk.Client) {
	t.Helper()
	f := &fakeGoblind{t: t, reply: map[string]string{}}
	mux := http.NewServeMux()
	for pattern := range plugins.Routes {
		pattern := pattern
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			f.mu.Lock()
			f.calls = append(f.calls, strings.TrimSpace(r.Method+" "+r.URL.RequestURI()+" "+string(body)))
			ans, ok := f.reply[pattern]
			f.mu.Unlock()
			if !ok {
				ans = "{}"
			}
			w.Header().Set("Content-Type", "application/json")
			if strings.HasPrefix(ans, "!") {
				code, rest, _ := strings.Cut(ans[1:], " ")
				if code == "403" {
					w.WriteHeader(http.StatusForbidden)
				}
				io.WriteString(w, rest)
				return
			}
			io.WriteString(w, ans)
		})
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("%s %s is not a plugin route", r.Method, r.URL.Path)
		http.NotFound(w, r)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return f, &pluginsdk.Client{HTTP: srv.Client(), Base: srv.URL}
}

func (f *fakeGoblind) set(pattern, answer string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reply[pattern] = answer
}

func (f *fakeGoblind) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// lineSink collects the slog records a plugin writes, as decoded JSON.
type lineSink struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *lineSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

// find returns the logged records whose msg is msg.
func (s *lineSink) find(msg string) []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []map[string]any
	dec := json.NewDecoder(bytes.NewReader(s.buf.Bytes()))
	for {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			break
		}
		if m["msg"] == msg {
			out = append(out, m)
		}
	}
	return out
}

// text is the whole log, for a search over every field.
func (s *lineSink) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// fakeLoad answers System without HTTP, so a test controls every answer.
type fakeLoad struct {
	mu    sync.Mutex
	sys   pluginsdk.System
	err   error
	calls int
}

func (l *fakeLoad) System(context.Context) (pluginsdk.System, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	return l.sys, l.err
}

func (l *fakeLoad) set(err error, sys pluginsdk.System) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.err, l.sys = err, sys
}

func (l *fakeLoad) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls
}

func selfWith(caps []string, settings map[string]any) pluginsdk.Self {
	return pluginsdk.Self{Name: "hello", Capabilities: caps, Settings: settings}
}

// runLoop starts loop in the background with a logger into sink and its own
// tick channel, waits for its first log line, and returns a stop func that
// cancels ctx and waits for loop.
func runLoop(t *testing.T, self pluginsdk.Self, load loader) (*lineSink, chan time.Time, func()) {
	t.Helper()
	sink := &lineSink{}
	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time, 4)
	started := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- loop(ctx, slog.New(slog.NewJSONHandler(sink, nil)), self, load, ticks, started)
	}()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("loop stopped before it logged anything: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not log its first line")
	}
	return sink, ticks, sync.OnceFunc(func() {
		t.Helper()
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("loop: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("loop did not return after ctx ended")
		}
	})
}

func wait(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

const greetingMsg = "hello there"

func TestLoopLogsTheGreetingWithTheLoad(t *testing.T) {
	load := &fakeLoad{sys: pluginsdk.System{UptimeSeconds: 4242, Sessions: 77}}
	sink, ticks, stop := runLoop(t, selfWith([]string{pluginsdk.CapStatsRead},
		map[string]any{"greeting": greetingMsg, "token": "s3cret"}), load)
	defer stop()

	started := sink.find("hello: started")
	if len(started) != 1 {
		t.Fatalf("started log = %d records, want 1", len(started))
	}
	if started[0]["greeting"] != greetingMsg {
		t.Errorf("greeting = %v, want %q", started[0]["greeting"], greetingMsg)
	}
	if started[0]["token_set"] != true {
		t.Errorf("token_set = %v, want true", started[0]["token_set"])
	}
	if strings.Contains(sink.text(), "s3cret") {
		t.Error("the secret's value must never be logged")
	}

	ticks <- time.Now()
	wait(t, func() bool { return len(sink.find(greetingMsg)) == 1 }, "the first greeting")
	line := sink.find(greetingMsg)[0]
	if line["uptime_seconds"] != float64(4242) || line["sessions"] != float64(77) {
		t.Errorf("load = %v/%v, want 4242/77", line["uptime_seconds"], line["sessions"])
	}
	if load.count() != 1 {
		t.Errorf("System called %d times for one tick, want 1", load.count())
	}

	ticks <- time.Now()
	wait(t, func() bool { return len(sink.find(greetingMsg)) == 2 }, "the second greeting")
}

func TestLoopWithoutTheCapabilityLogsOnceAndStaysUp(t *testing.T) {
	load := &fakeLoad{}
	sink, ticks, stop := runLoop(t, selfWith(nil, map[string]any{"greeting": "yo"}), load)
	defer stop()

	if got := sink.find("hello: started"); len(got) != 0 {
		t.Fatalf("it started although stats:read is not approved: %v", got)
	}
	warn := sink.find(deniedAtStart)
	if len(warn) != 1 {
		t.Fatalf("refusal logged %d times, want 1", len(warn))
	}
	if warn[0]["greeting"] != "yo" {
		t.Errorf("greeting = %v, want yo", warn[0]["greeting"])
	}
	if warn[0]["token_set"] != false {
		t.Errorf("token_set = %v, want false when no token is set", warn[0]["token_set"])
	}
	ticks <- time.Now()
	time.Sleep(50 * time.Millisecond)
	if load.count() != 0 {
		t.Errorf("System called %d times without the capability, want 0", load.count())
	}
	if got := len(sink.find(deniedAtStart)); got != 1 {
		t.Errorf("refusal logged %d times after a tick, want still 1", got)
	}
	stop()
}

func TestLoopLogsADeniedCapabilityOnceAndKeepsRunning(t *testing.T) {
	load := &fakeLoad{err: &pluginsdk.APIError{Status: http.StatusForbidden, Code: "scope_denied"}}
	sink, ticks, stop := runLoop(t, selfWith([]string{pluginsdk.CapStatsRead}, nil), load)
	defer stop()

	if got := sink.find("hello: started"); len(got) != 1 {
		t.Fatalf("started log = %d records, want 1", len(got))
	}
	for i := 1; i <= 3; i++ {
		ticks <- time.Now()
		wait(t, func() bool { return load.count() == i }, "the denial to be handled")
	}
	if got := len(sink.find(deniedWhileRunning)); got != 1 {
		t.Errorf("denial logged %d times for three ticks, want once", got)
	}

	// A plain load error is logged each time and is not fatal.
	load.set(errors.New("goblind: 500 boom"), pluginsdk.System{})
	ticks <- time.Now()
	wait(t, func() bool { return len(sink.find("reading the load failed")) == 1 }, "the load error")

	// And the plugin recovers when goblind answers again.
	load.set(nil, pluginsdk.System{UptimeSeconds: 5, Sessions: 1})
	ticks <- time.Now()
	wait(t, func() bool { return len(sink.find("hello")) == 1 }, "a greeting after recovery")
}

// TestLoopAgainstAFakeGoblind drives the plugin through the real SDK client,
// so every URL it asks for must be a plugin route goblind would serve.
func TestLoopAgainstAFakeGoblind(t *testing.T) {
	fake, client := newFake(t)
	fake.set("GET /api/v1/plugin/self", `{"name":"hello","capabilities":["stats:read"],`+
		`"settings":{"greeting":"hello","token":"s3cret"}}`)
	fake.set("GET /api/v1/stats/system", `{"uptime_seconds":99,"sessions":3}`)
	self, err := client.Self(context.Background())
	if err != nil {
		t.Fatalf("Self: %v", err)
	}
	sink, ticks, stop := runLoop(t, self, client)
	defer stop()

	ticks <- time.Now()
	wait(t, func() bool { return len(sink.find("hello")) == 1 }, "the greeting from the fake goblind")
	line := sink.find("hello")[0]
	if line["uptime_seconds"] != float64(99) || line["sessions"] != float64(3) {
		t.Errorf("load = %v/%v, want 99/3", line["uptime_seconds"], line["sessions"])
	}
	if got := fake.count("GET /api/v1/plugin/self"); got != 1 {
		t.Errorf("self called %d times, want once, at start", got)
	}
	if got := fake.count("GET /api/v1/stats/system"); got != 1 {
		t.Errorf("stats/system called %d times for one tick, want 1", got)
	}
	if strings.Contains(sink.text(), "s3cret") {
		t.Error("the secret's value must never be logged")
	}
}

// TestDeniedOverHTTP checks that a 403 from goblind is what pluginsdk.Denied
// recognises, the case loop logs once for.
func TestDeniedOverHTTP(t *testing.T) {
	fake, client := newFake(t)
	fake.set("GET /api/v1/stats/system", `!403 {"error":{"code":"scope_denied","message":"stats:read is not approved"}}`)
	_, err := client.System(context.Background())
	if !pluginsdk.Denied(err) {
		t.Fatalf("Denied(%v) = false, want true", err)
	}
}

func TestManifestIsValid(t *testing.T) {
	raw, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatalf("read plugin.json: %v", err)
	}
	m, err := plugins.ParseManifest(raw, "hello")
	if err != nil {
		t.Fatalf("plugin.json: %v", err)
	}
	if m.Name != "hello" {
		t.Errorf("name = %q, want hello", m.Name)
	}
	if len(m.Capabilities) != 1 || m.Capabilities[0] != pluginsdk.CapStatsRead {
		t.Errorf("capabilities = %v, want only stats:read", m.Capabilities)
	}
	props := m.SettingsSchema.Properties
	if p := props["greeting"]; p == nil || p.Type != "string" {
		t.Errorf("greeting = %+v, want a string setting", p)
	}
	tok := props["token"]
	if tok == nil || tok.Type != "string" || !tok.Secret {
		t.Errorf("token = %+v, want a secret string", tok)
	}
	if tok != nil && tok.Default != nil {
		t.Error("a secret must have no default")
	}
	if len(props) != 2 {
		t.Errorf("settings = %v, want greeting and token only", props)
	}
}
