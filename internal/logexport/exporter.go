// Package logexport forwards firewall events to remote syslog servers and
// Loki push endpoints (task 096). It never blocks the caller: Observe drops
// and counts events when its buffer is full.
package logexport

import (
	"context"
	"crypto/x509"
	"log/slog"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/logs"
)

// BufferSize is the per-exporter event queue capacity. Events beyond it are
// dropped and counted in Status.Dropped.
const BufferSize = 4096

// TickInterval is how often Run re-reads the entry list from config.
const TickInterval = 30 * time.Second

// TLSRoots is the CA pool used to verify TLS syslog servers. Tests may
// replace it with a pool containing their own test certificate.
var TLSRoots *x509.CertPool

// Status is the per-entry view returned by GET /api/v1/log-export.
type Status struct {
	Name      string    `json:"name"`
	Sent      int64     `json:"sent"`
	Dropped   int64     `json:"dropped"`
	LastError string    `json:"last_error,omitempty"`
	LastSent  time.Time `json:"last_sent,omitzero"`
}

type entry struct {
	cfg      config.LogExport
	kinds    map[string]bool // nil = all kinds
	sent     int64
	dropped  int64
	lastErr  string
	lastSent time.Time
	ch       chan *logs.Event // per-destination queue, created when the sender starts
}

// Exporter forwards events to every enabled log export destination. Set
// Logger before Run; TLSRoots (package var) may be replaced by tests.
type Exporter struct {
	Logger *slog.Logger
	Now    func() time.Time // test hook; defaults to time.Now

	mu            sync.Mutex
	entries       map[string]*entry
	senderStarted map[string]bool
}

func (e *Exporter) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Exporter) logger() *slog.Logger {
	if e.Logger != nil {
		return e.Logger
	}
	return slog.Default()
}

// Observe queues ev for every enabled destination whose kinds filter matches.
// It never blocks: when a destination's buffer is full the event is dropped
// and counted on that destination.
func (e *Exporter) Observe(ev *logs.Event) {
	e.mu.Lock()
	var targets []*entry
	for _, en := range e.entries {
		if !en.cfg.Enabled || en.ch == nil {
			continue
		}
		if en.kinds != nil && !en.kinds[ev.Kind] {
			continue
		}
		targets = append(targets, en)
	}
	for _, t := range targets {
		select {
		case t.ch <- ev:
			t.sent++ // the send loop confirms it; on failure it is moved to dropped
		default:
			t.dropped++
		}
	}
	e.mu.Unlock()
}

// Run starts the export loops. entries returns the current list of log
// export destinations (normally from the running config). It blocks until
// ctx is done.
func (e *Exporter) Run(ctx context.Context, entries func() []config.LogExport) {
	t := time.NewTicker(TickInterval)
	defer t.Stop()
	e.Sync(ctx, entries())
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.Sync(ctx, entries())
		}
	}
}

// Sync reconciles the entry list. New or changed entries start a sender;
// removed ones are stopped. Safe to call from the API so fresh config shows
// up without waiting for the next tick.
func (e *Exporter) Sync(ctx context.Context, list []config.LogExport) {
	e.mu.Lock()
	if e.entries == nil {
		e.entries = map[string]*entry{}
	}
	if e.senderStarted == nil {
		e.senderStarted = map[string]bool{}
	}
	keep := make(map[string]bool, len(list))
	for _, c := range list {
		keep[c.Name] = true
		en, ok := e.entries[c.Name]
		if !ok || !reflect.DeepEqual(en.cfg, c) {
			e.entries[c.Name] = newEntry(c)
		}
	}
	for name := range e.entries {
		if !keep[name] {
			delete(e.entries, name)
			delete(e.senderStarted, name)
		}
	}
	var names []string
	for _, c := range list {
		if c.Enabled && !e.senderStarted[c.Name] {
			e.senderStarted[c.Name] = true
			names = append(names, c.Name)
		}
	}
	e.mu.Unlock()

	sort.Strings(names)
	for _, name := range names {
		e.startSender(ctx, name)
	}
}

func newEntry(c config.LogExport) *entry {
	en := &entry{cfg: c}
	if len(c.Kinds) > 0 {
		en.kinds = make(map[string]bool, len(c.Kinds))
		for _, k := range c.Kinds {
			en.kinds[k] = true
		}
	}
	return en
}

// startSender creates the per-destination queue and launches one loop that
// reads it and pushes batches to the destination.
func (e *Exporter) startSender(ctx context.Context, name string) {
	e.mu.Lock()
	en := e.entries[name]
	if en == nil || !en.cfg.Enabled {
		e.mu.Unlock()
		return
	}
	cfg := en.cfg
	if en.ch == nil {
		en.ch = make(chan *logs.Event, BufferSize)
	}
	ch := en.ch
	e.mu.Unlock()

	go func() {
		buf := make([]*logs.Event, 0, 64)
		flush := func() {
			if len(buf) == 0 {
				return
			}
			err := e.push(ctx, cfg, buf)
			e.record(name, err, len(buf))
			buf = buf[:0]
		}
		for {
			select {
			case <-ctx.Done():
				flush()
				return
			case ev := <-ch:
				buf = append(buf, ev)
				if len(buf) >= 64 {
					flush()
				}
			case <-time.After(e.flushInterval(cfg)):
				flush()
			}
		}
	}()
}

// flushInterval is the max time an event waits in a sender's local buffer.
func (e *Exporter) flushInterval(c config.LogExport) time.Duration {
	if c.Type == "loki" {
		return 2 * time.Second
	}
	return 50 * time.Millisecond
}

// push delivers buf to the destination and returns an error on failure. The
// error never contains secrets (see redact).
func (e *Exporter) push(ctx context.Context, c config.LogExport, evs []*logs.Event) error {
	var err error
	switch c.Type {
	case "syslog":
		err = sendSyslog(ctx, c, evs)
	case "loki":
		err = sendLoki(ctx, c, evs)
	default:
		err = nil
	}
	if err != nil {
		return redact(c, err)
	}
	return nil
}

// record updates the entry counters after a flush.
func (e *Exporter) record(name string, err error, n int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	en := e.entries[name]
	if en == nil {
		return
	}
	now := e.now()
	if err != nil {
		en.sent -= int64(n)
		en.dropped += int64(n)
		en.lastErr = err.Error()
	} else {
		en.lastErr = ""
		en.lastSent = now
	}
}

// Status returns all entry statuses sorted by name.
func (e *Exporter) Status() []Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Status, 0, len(e.entries))
	for _, en := range e.entries {
		out = append(out, Status{Name: en.cfg.Name, Sent: en.sent, Dropped: en.dropped, LastError: en.lastErr, LastSent: en.lastSent})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
