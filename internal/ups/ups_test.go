package ups

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// upsc output of an APC Back-UPS, trimmed.
const online = `battery.charge: 100
battery.runtime: 1820
device.mfr: American Power Conversion
device.model: Back-UPS ES 700G
ups.load: 12
ups.status: OL
`

func reading(status string, charge, runtime string) string {
	return "battery.charge: " + charge + "\nbattery.runtime: " + runtime + "\ndevice.model: Back-UPS ES 700G\nups.status: " + status + "\n"
}

type fake struct {
	out      string
	err      error
	events   []string
	shutdown int
}

func newManager(f *fake, u config.UPS) *Manager {
	return &Manager{
		Config:   func() (config.UPS, error) { return u, nil },
		Query:    func(context.Context, string) (string, error) { return f.out, f.err },
		Publish:  func(kind, title, msg string) { f.events = append(f.events, kind) },
		Shutdown: func(context.Context) error { f.shutdown++; return nil },
		Now:      func() time.Time { return time.Date(2026, 10, 22, 3, 0, 0, 0, time.UTC) },
	}
}

func TestParse(t *testing.T) {
	var st Status
	parse(online, &st)
	if st.State != StateOnline || *st.Charge != 100 || *st.Runtime != 1820 || *st.Load != 12 ||
		st.Model != "American Power Conversion Back-UPS ES 700G" {
		t.Fatalf("%+v", st)
	}
	st = Status{}
	parse("ups.status: OB DISCHRG LB\n", &st)
	if st.State != StateLowBattery || !slices.Equal(st.Flags, []string{"OB", "DISCHRG", "LB"}) {
		t.Fatalf("%+v", st)
	}
}

func TestUSBEvents(t *testing.T) {
	f := &fake{out: online}
	m := newManager(f, config.UPS{Enabled: true, Mode: "usb"})
	ctx := context.Background()
	m.Poll(ctx)
	if st := m.Status(); !st.Connected || st.State != StateOnline || len(f.events) != 0 {
		t.Fatalf("baseline %+v %v", st, f.events)
	}
	f.out = reading("OB DISCHRG", "80", "1200")
	m.Poll(ctx)
	f.out = reading("OB DISCHRG", "70", "1000")
	m.Poll(ctx) // still on battery: no second event
	f.out = reading("OB DISCHRG LB", "20", "200")
	m.Poll(ctx)
	f.out = reading("OL CHRG", "25", "400")
	m.Poll(ctx)
	want := []string{EventOnBattery, EventLow, EventBack}
	if !slices.Equal(f.events, want) {
		t.Fatalf("events %v, want %v", f.events, want)
	}
	// with a USB UPS upsmon shuts the box down, not goblind
	if f.shutdown != 0 {
		t.Fatal("goblind shut down a USB-mode box")
	}
	if st := m.Status(); st.LastEvent == nil || st.LastEvent.Kind != EventBack {
		t.Fatalf("last event %+v", st.LastEvent)
	}
}

func TestRemoteThresholdShutdown(t *testing.T) {
	f := &fake{out: online}
	m := newManager(f, config.UPS{Enabled: true, Mode: "remote", Host: "nas", Name: "apc", ShutdownPercent: 40})
	ctx := context.Background()
	m.Poll(ctx)
	f.out = reading("OB DISCHRG", "60", "900")
	m.Poll(ctx)
	f.out = reading("OB DISCHRG", "39", "500")
	m.Poll(ctx) // first low reading: wait for a second one
	if f.shutdown != 0 {
		t.Fatal("shut down on a single reading")
	}
	m.Poll(ctx)
	if f.shutdown != 1 || m.Status().State != StateLowBattery {
		t.Fatalf("shutdown %d state %s", f.shutdown, m.Status().State)
	}
	m.Poll(ctx)
	if f.shutdown != 1 {
		t.Fatal("shutdown ran twice")
	}
	if !slices.Equal(f.events, []string{EventOnBattery, EventLow}) {
		t.Fatalf("events %v", f.events)
	}
}

func TestRemoteNoThresholdOnMains(t *testing.T) {
	// a low charge while on mains (recharging after an outage) is no reason
	// to shut down
	f := &fake{out: reading("OL CHRG", "10", "100")}
	m := newManager(f, config.UPS{Enabled: true, Mode: "remote", Host: "nas", Name: "apc", ShutdownPercent: 40})
	for range 3 {
		m.Poll(context.Background())
	}
	if f.shutdown != 0 || len(f.events) != 0 {
		t.Fatalf("shutdown %d events %v", f.shutdown, f.events)
	}
}

func TestQueryError(t *testing.T) {
	f := &fake{err: errors.New("upsc: Error: Connection failure: Connection refused")}
	m := newManager(f, config.UPS{Enabled: true, Mode: "remote", Host: "nas", Name: "apc"})
	m.Poll(context.Background())
	st := m.Status()
	if st.Connected || st.LastError == "" || st.State != StateUnknown {
		t.Fatalf("%+v", st)
	}
	// disabled: an empty status
	m.Config = func() (config.UPS, error) { return config.UPS{}, nil }
	m.Poll(context.Background())
	if st := m.Status(); st.Enabled || st.LastError != "" {
		t.Fatalf("%+v", st)
	}
}
