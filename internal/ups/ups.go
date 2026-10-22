// Package ups watches the UPS NUT reports (task 112): it reads `upsc` every
// few seconds for the status page, publishes on-battery / back / low-battery
// events, and for a remote NUT server shuts the box down at the owner's own
// thresholds (with a UPS on USB the driver raises the low-battery flag at
// those thresholds and upsmon shuts down).
package ups

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// Event kinds published to notifications.
const (
	EventOnBattery = "ups_on_battery"
	EventBack      = "ups_back"
	EventLow       = "ups_low"
)

// State of the power.
const (
	StateOnline     = "online"
	StateOnBattery  = "on_battery"
	StateLowBattery = "low_battery"
	StateUnknown    = "unknown"
)

// Status is GET /api/v1/ups.
type Status struct {
	Enabled   bool   `json:"enabled"`
	Mode      string `json:"mode,omitempty"`
	Connected bool   `json:"connected"`
	State     string `json:"state"`
	// Flags are NUT's ups.status words (OL, OB, LB, CHRG, ...).
	Flags      []string  `json:"flags,omitempty"`
	Charge     *float64  `json:"charge,omitempty"`          // %
	Runtime    *int      `json:"runtime_seconds,omitempty"` // battery runtime left
	Load       *float64  `json:"load,omitempty"`            // % of capacity
	Model      string    `json:"model,omitempty"`
	LastUpdate time.Time `json:"last_update,omitzero"`
	LastEvent  *Event    `json:"last_event,omitempty"`
	LastError  string    `json:"last_error,omitempty"`
}

// Event is the last power event.
type Event struct {
	Kind string    `json:"kind"`
	Time time.Time `json:"time"`
}

// Manager polls the UPS.
type Manager struct {
	Config func() (config.UPS, error)
	// Query returns `upsc <target>` output; nil runs upsc.
	Query func(ctx context.Context, target string) (string, error)
	// Publish sends an event to notifications; nil drops it.
	Publish func(kind, title, message string)
	// Shutdown powers the box off; nil runs /sbin/shutdown -h +0.
	Shutdown func(ctx context.Context) error
	Logger   *slog.Logger
	Now      func() time.Time
	// Every is the poll interval; 0 = 5 s.
	Every time.Duration

	mu       sync.Mutex
	st       Status
	lowPolls int  // consecutive polls at or below the remote thresholds
	shutting bool // shutdown started
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Manager) logger() *slog.Logger {
	if m.Logger != nil {
		return m.Logger
	}
	return slog.Default()
}

// Status returns the last poll's result.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.st
	st.Flags = slices.Clone(st.Flags)
	return st
}

// Run polls until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	every := m.Every
	if every == 0 {
		every = 5 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		m.Poll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Poll reads the UPS once and acts on what changed.
func (m *Manager) Poll(ctx context.Context) {
	u, err := m.Config()
	if err != nil {
		m.logger().Warn("ups: reading the config", "error", err)
		return
	}
	if !u.Enabled {
		m.mu.Lock()
		m.st = Status{State: StateUnknown}
		m.lowPolls = 0
		m.mu.Unlock()
		return
	}
	qctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	out, err := m.query(qctx, u.Target())
	cancel()

	m.mu.Lock()
	prev := m.st
	st := Status{Enabled: true, Mode: u.Mode, State: StateUnknown, LastEvent: prev.LastEvent}
	if err != nil {
		st.LastError = err.Error()
		st.LastUpdate = prev.LastUpdate
		m.st = st
		m.mu.Unlock()
		return
	}
	parse(out, &st)
	st.Connected = len(st.Flags) > 0
	st.LastUpdate = m.now()
	if !st.Connected {
		st.LastError = "the UPS did not report a status (is it plugged in and switched on?)"
	}

	// events on state changes; the first poll only sets the baseline, except
	// that a box starting on battery says so
	var publish []Event
	ev := func(kind string) {
		e := Event{Kind: kind, Time: st.LastUpdate}
		st.LastEvent = &e
		publish = append(publish, e)
	}
	onBattery := st.State == StateOnBattery || st.State == StateLowBattery
	wasOnBattery := prev.State == StateOnBattery || prev.State == StateLowBattery
	switch {
	case onBattery && !wasOnBattery:
		ev(EventOnBattery)
	case st.State == StateOnline && wasOnBattery:
		ev(EventBack)
	}

	// remote: the box's own thresholds, two polls in a row to ride out a
	// glitch in one reading
	low := st.State == StateLowBattery
	if onBattery && u.Mode == "remote" && belowThresholds(u, st) {
		m.lowPolls++
		low = low || m.lowPolls >= 2
	} else if !onBattery {
		m.lowPolls = 0
	}
	wasLow := prev.State == StateLowBattery
	if low {
		st.State = StateLowBattery
		if !wasLow {
			ev(EventLow)
		}
	}
	shutdown := low && onBattery && u.Mode == "remote" && !m.shutting
	if shutdown {
		m.shutting = true
	}
	m.st = st
	m.mu.Unlock()

	for _, e := range publish {
		m.publish(e.Kind, st)
	}
	if shutdown {
		m.logger().Warn("ups: battery low, shutting down", "charge", fmtPtr(st.Charge), "runtime", fmtIntPtr(st.Runtime))
		if err := m.shutdown(ctx); err != nil {
			m.logger().Error("ups: shutdown failed", "error", err)
			m.mu.Lock()
			m.shutting = false
			m.mu.Unlock()
		}
	}
}

func belowThresholds(u config.UPS, st Status) bool {
	if u.ShutdownPercent > 0 && st.Charge != nil && *st.Charge <= float64(u.ShutdownPercent) {
		return true
	}
	return u.ShutdownRuntime > 0 && st.Runtime != nil && *st.Runtime <= u.ShutdownRuntime
}

func (m *Manager) publish(kind string, st Status) {
	if m.Publish == nil {
		return
	}
	detail := []string{}
	if st.Charge != nil {
		detail = append(detail, "battery "+fmtPtr(st.Charge)+" %")
	}
	if st.Runtime != nil {
		detail = append(detail, "about "+strconv.Itoa(*st.Runtime/60)+" min left")
	}
	msg := strings.Join(detail, ", ")
	switch kind {
	case EventOnBattery:
		m.Publish(kind, "Power failure: running on battery", msg)
	case EventBack:
		m.Publish(kind, "Power is back", msg)
	case EventLow:
		m.Publish(kind, "UPS battery low: shutting down", msg)
	}
}

func (m *Manager) query(ctx context.Context, target string) (string, error) {
	if m.Query != nil {
		return m.Query(ctx, target)
	}
	out, err := exec.CommandContext(ctx, "upsc", target).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		// upsc's messages name the target and the error, never a password
		return "", errors.New("upsc: " + firstLine(msg))
	}
	return string(out), nil
}

func (m *Manager) shutdown(ctx context.Context) error {
	if m.Shutdown != nil {
		return m.Shutdown(ctx)
	}
	return exec.CommandContext(ctx, "/sbin/shutdown", "-h", "+0", "Goblin Gate: UPS battery low").Run()
}

// parse reads upsc's "key: value" lines.
func parse(out string, st *Status) {
	var model, mfr string
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ": ")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch k {
		case "ups.status":
			st.Flags = strings.Fields(v)
		case "battery.charge":
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				st.Charge = &f
			}
		case "battery.runtime":
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				n := int(f)
				st.Runtime = &n
			}
		case "ups.load":
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				st.Load = &f
			}
		case "device.model", "ups.model":
			if model == "" {
				model = v
			}
		case "device.mfr", "ups.mfr":
			if mfr == "" {
				mfr = v
			}
		}
	}
	st.Model = model
	if mfr != "" && !strings.HasPrefix(model, mfr) {
		st.Model = strings.TrimSpace(mfr + " " + model)
	}
	switch {
	case slices.Contains(st.Flags, "LB") || slices.Contains(st.Flags, "FSD"):
		st.State = StateLowBattery
	case slices.Contains(st.Flags, "OB"):
		st.State = StateOnBattery
	case slices.Contains(st.Flags, "OL"):
		st.State = StateOnline
	}
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
}

func fmtPtr(f *float64) string {
	if f == nil {
		return "?"
	}
	return strconv.FormatFloat(*f, 'f', -1, 64)
}

func fmtIntPtr(n *int) string {
	if n == nil {
		return "?"
	}
	return strconv.Itoa(*n)
}
