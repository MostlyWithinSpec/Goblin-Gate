// Package notify sends alerts about events (task 053) to the channels the
// config names: email, webhook, Discord, ntfy and Telegram. A Dispatcher
// takes events without blocking, applies the config's rules, rate-limits
// repeats and retries failed deliveries. Secrets never reach the logs.
package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// Event is something worth telling the admin about. Kind is one of
// config.NotifyEvents; Key separates independent instances of the same kind
// for rate limiting (e.g. the WAN name, the feed name).
type Event struct {
	Kind    string            `json:"event"`
	Key     string            `json:"key,omitempty"`
	Title   string            `json:"title"`
	Message string            `json:"message"`
	Time    time.Time         `json:"time"`
	Fields  map[string]string `json:"fields,omitempty"`
}

// Dispatcher delivers events to the channels the rules name.
type Dispatcher struct {
	// Config returns the notification settings of the running config.
	Config func() (config.Notifications, error)
	// Hostname names the box in messages ("goblin-gate: ...").
	Hostname func() string
	Client   *http.Client // nil = 15 s timeout
	Logger   *slog.Logger
	// Delivered, if set, is called once per event that went out to at least
	// one channel (goblind records it in the log store).
	Delivered func(Event, []string)

	RateLimit time.Duration    // default 10 min per kind+key
	Retries   int              // default 3 attempts in all
	Backoff   time.Duration    // first retry delay, doubled each time; default 2 s
	Now       func() time.Time // tests

	// TelegramAPI is the Bot API base URL (tests point it elsewhere).
	TelegramAPI string

	once  sync.Once
	queue chan Event
	mu    sync.Mutex
	last  map[string]time.Time

	subMu sync.Mutex
	subs  map[chan Event]struct{}
}

// Subscribe returns a channel that receives every published event (before
// rules and rate limits: GET /api/v1/events) and a cancel function. A slow
// subscriber loses events instead of blocking Publish.
func (d *Dispatcher) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	d.subMu.Lock()
	if d.subs == nil {
		d.subs = map[chan Event]struct{}{}
	}
	d.subs[ch] = struct{}{}
	d.subMu.Unlock()
	return ch, func() {
		d.subMu.Lock()
		delete(d.subs, ch)
		d.subMu.Unlock()
	}
}

func (d *Dispatcher) broadcast(ev Event) {
	d.subMu.Lock()
	defer d.subMu.Unlock()
	for ch := range d.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

func (d *Dispatcher) init() {
	d.once.Do(func() {
		d.queue = make(chan Event, 256)
		d.last = map[string]time.Time{}
	})
}

func (d *Dispatcher) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *Dispatcher) logger() *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return slog.Default()
}

// Publish queues an event; it never blocks (a full queue drops the event).
// A nil Dispatcher ignores everything, so callers need no checks.
func (d *Dispatcher) Publish(ev Event) {
	if d == nil {
		return
	}
	d.init()
	if ev.Time.IsZero() {
		ev.Time = d.now()
	}
	d.broadcast(ev)
	select {
	case d.queue <- ev:
	default:
		d.logger().Warn("notify: queue full, event dropped", "event", ev.Kind)
	}
}

// Run delivers queued events until ctx ends.
func (d *Dispatcher) Run(ctx context.Context) {
	d.init()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-d.queue:
			d.deliver(ctx, ev)
		}
	}
}

// Test sends a test message to one channel now, ignoring rules and rate
// limits, and returns the delivery error.
func (d *Dispatcher) Test(ctx context.Context, channel string) error {
	d.init()
	n, err := d.Config()
	if err != nil {
		return err
	}
	for _, c := range n.Channels {
		if c.Name == channel {
			ev := Event{Kind: "test", Title: "Test notification", Message: "Goblin Gate can reach this channel.", Time: d.now()}
			return d.send(ctx, c, d.decorate(ev))
		}
	}
	return ErrUnknownChannel
}

// ErrUnknownChannel: Test named a channel the running config does not have.
var ErrUnknownChannel = errors.New("notify: no such channel")

// deliver applies the rules and the rate limit, then sends with retries.
func (d *Dispatcher) deliver(ctx context.Context, ev Event) {
	d.init()
	n, err := d.Config()
	if err != nil {
		d.logger().Warn("notify: no config", "error", err)
		return
	}
	targets := map[string]bool{}
	for _, r := range n.Rules {
		if r.Event == ev.Kind {
			for _, c := range r.Channels {
				targets[c] = true
			}
		}
	}
	if len(targets) == 0 {
		return
	}
	if !d.allow(ev) {
		return
	}
	ev = d.decorate(ev)
	var sent []string
	for _, c := range n.Channels {
		if !targets[c.Name] || !c.Enabled {
			continue
		}
		if err := d.sendRetry(ctx, c, ev); err != nil {
			d.logger().Warn("notify: delivery failed", "channel", c.Name, "type", c.Type, "event", ev.Kind, "error", err)
			continue
		}
		sent = append(sent, c.Name)
	}
	if len(sent) > 0 && d.Delivered != nil {
		d.Delivered(ev, sent)
	}
}

// allow enforces at most one event per kind+key per RateLimit.
func (d *Dispatcher) allow(ev Event) bool {
	limit := d.RateLimit
	if limit == 0 {
		limit = 10 * time.Minute
	}
	key := ev.Kind + "\x00" + ev.Key
	d.mu.Lock()
	defer d.mu.Unlock()
	if t, ok := d.last[key]; ok && d.now().Sub(t) < limit {
		return false
	}
	d.last[key] = d.now()
	return true
}

func (d *Dispatcher) decorate(ev Event) Event {
	if d.Hostname != nil {
		if h := d.Hostname(); h != "" && !strings.HasPrefix(ev.Title, h+":") {
			ev.Title = h + ": " + ev.Title
		}
	}
	return ev
}

func (d *Dispatcher) sendRetry(ctx context.Context, c config.NotifyChannel, ev Event) error {
	tries := d.Retries
	if tries <= 0 {
		tries = 3
	}
	wait := d.Backoff
	if wait <= 0 {
		wait = 2 * time.Second
	}
	var err error
	for i := 0; i < tries; i++ {
		if err = d.send(ctx, c, ev); err == nil {
			return nil
		}
		var perm permanentError
		if errors.As(err, &perm) || i == tries-1 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		wait *= 2
	}
	return err
}

// permanentError is a failure a retry cannot fix (bad credentials, 4xx).
type permanentError struct{ error }

func (d *Dispatcher) send(ctx context.Context, c config.NotifyChannel, ev Event) error {
	switch c.Type {
	case "webhook":
		return d.sendWebhook(ctx, c, ev)
	case "discord":
		return d.sendDiscord(ctx, c, ev)
	case "ntfy":
		return d.sendNtfy(ctx, c, ev)
	case "telegram":
		return d.sendTelegram(ctx, c, ev)
	case "email":
		return sendEmail(ctx, c, ev)
	}
	return permanentError{fmt.Errorf("unknown channel type %q", c.Type)}
}
