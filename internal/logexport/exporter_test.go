package logexport

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/logs"
)

// TestKindsFilter checks that Observe only queues events whose kind is in the
// entry's kinds filter (empty = all).
func TestKindsFilter(t *testing.T) {
	e := &Exporter{Now: func() time.Time { return time.Unix(1700000000, 0) }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Two destinations: one filters to traffic only, the other takes all.
	e.Sync(ctx, []config.LogExport{
		{Name: "only-traffic", Enabled: true, Type: "syslog", Address: "127.0.0.1:1", Protocol: "udp", Kinds: []string{"traffic"}},
		{Name: "all", Enabled: true, Type: "syslog", Address: "127.0.0.1:1", Protocol: "udp"},
	})

	e.Observe(&logs.Event{Kind: "traffic"})
	e.Observe(&logs.Event{Kind: "dns"})

	st := map[string]Status{}
	for _, s := range e.Status() {
		st[s.Name] = s
	}
	if st["only-traffic"].Sent != 1 {
		t.Errorf("only-traffic sent = %d, want 1 (traffic only)", st["only-traffic"].Sent)
	}
	if st["all"].Sent != 2 {
		t.Errorf("all sent = %d, want 2 (no filter)", st["all"].Sent)
	}
}

// TestDropCounting checks that when a destination's buffer is full the event
// is dropped and counted on that destination only.
func TestDropCounting(t *testing.T) {
	e := &Exporter{Now: func() time.Time { return time.Unix(1700000000, 0) }}

	// Use a tiny buffer for the test by filling it directly.
	en := newEntry(config.LogExport{Name: "full", Enabled: true, Type: "syslog", Address: "127.0.0.1:1", Protocol: "udp"})
	en.ch = make(chan *logs.Event, 2)
	e.mu.Lock()
	if e.entries == nil {
		e.entries = map[string]*entry{}
	}
	e.entries["full"] = en
	e.mu.Unlock()

	ev := &logs.Event{Kind: "traffic"}
	// Fill the buffer (2), then the next two must be dropped.
	for i := 0; i < 4; i++ {
		e.Observe(ev)
	}

	st := map[string]Status{}
	for _, s := range e.Status() {
		st[s.Name] = s
	}
	if st["full"].Sent != 2 {
		t.Errorf("sent = %d, want 2 (buffer capacity)", st["full"].Sent)
	}
	if st["full"].Dropped != 2 {
		t.Errorf("dropped = %d, want 2", st["full"].Dropped)
	}
}

// TestNoSecretInError checks that a send failure never leaks the password,
// token or a URL with embedded credentials.
func TestNoSecretInError(t *testing.T) {
	e := &Exporter{Now: func() time.Time { return time.Unix(1700000000, 0) }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// A loki destination whose URL embeds credentials and that also has a
	// password/token. The connection fails immediately (nothing listening).
	c := config.LogExport{
		Name: "sec", Enabled: true, Type: "loki",
		URL:      "http://user:p4ss@127.0.0.1:9/loki/api/v1/push",
		Password: "hunter2", Token: "tok-abc",
	}
	e.Sync(ctx, []config.LogExport{c})

	ev := &logs.Event{Kind: "system"}
	e.Observe(ev)

	// Wait for the sender to attempt and fail.
	deadline := time.Now().Add(3 * time.Second)
	for {
		st := e.Status()
		if len(st) > 0 && st[0].LastError != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for an error")
		}
		time.Sleep(10 * time.Millisecond)
	}

	errStr := e.Status()[0].LastError
	for _, secret := range []string{"p4ss", "hunter2", "tok-abc", "user:p4ss@"} {
		if strings.Contains(errStr, secret) {
			t.Errorf("error leaks secret %q: %q", secret, errStr)
		}
	}
}

// TestStatusSorted checks Status() returns entries sorted by name.
func TestStatusSorted(t *testing.T) {
	e := &Exporter{Now: func() time.Time { return time.Unix(1700000000, 0) }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.Sync(ctx, []config.LogExport{
		{Name: "zeta", Enabled: false, Type: "syslog", Address: "127.0.0.1:1"},
		{Name: "alpha", Enabled: false, Type: "syslog", Address: "127.0.0.1:1"},
	})
	st := e.Status()
	if len(st) != 2 || st[0].Name != "alpha" || st[1].Name != "zeta" {
		t.Errorf("status order = %v", names(st))
	}
}

func names(st []Status) []string {
	out := make([]string, len(st))
	for i, s := range st {
		out[i] = s.Name
	}
	return out
}

// TestSyncRemovesGone checks that a destination removed from config is dropped.
func TestSyncRemovesGone(t *testing.T) {
	e := &Exporter{Now: func() time.Time { return time.Unix(1700000000, 0) }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.Sync(ctx, []config.LogExport{{Name: "a", Enabled: false, Type: "syslog", Address: "127.0.0.1:1"}})
	if len(e.Status()) != 1 {
		t.Fatal("expected 1 entry")
	}
	e.Sync(ctx, nil)
	if len(e.Status()) != 0 {
		t.Errorf("entries after removal = %d, want 0", len(e.Status()))
	}
}

// TestSyslogErrorNoAddressLeak checks that a syslog send error (unknown
// protocol) does not include the full address with port.
func TestSyslogErrorNoAddressLeak(t *testing.T) {
	c := config.LogExport{Name: "x", Type: "syslog", Address: "127.0.0.1:1", Protocol: "sctp"}
	err := sendSyslog(context.Background(), c, []*logs.Event{{Kind: "system"}})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "127.0.0.1:1") {
		t.Errorf("error leaks full address: %q", err.Error())
	}
}
