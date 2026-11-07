// Package throughput runs an iperf3 server on demand, so a computer on the
// LAN can measure how fast it reaches the box. One server at a time, bound
// to one interface's address, open in the firewall only on that interface
// (nft ThroughputSet) and stopped by itself after a few minutes.
package throughput

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// Port is the iperf3 port (nft.ThroughputPort).
const Port = 5201

// Durations a test window may last.
const (
	DefaultDuration = 5 * time.Minute
	MaxDuration     = 15 * time.Minute
	maxResults      = 20
)

// ErrRunning is returned by Start while a server runs.
var ErrRunning = errors.New("a throughput test server is already running")

// Status is the server's state and the results of its tests (kept until the
// next Start).
type Status struct {
	Running   bool      `json:"running"`
	Interface string    `json:"interface,omitempty"`
	Device    string    `json:"device,omitempty"`
	Address   string    `json:"address,omitempty"` // what clients connect to
	Port      int       `json:"port"`
	Started   time.Time `json:"started,omitzero"`
	Expires   time.Time `json:"expires,omitzero"`
	Results   []Result  `json:"results"`
	Error     string    `json:"error,omitempty"` // the last failure of the server
}

// Result is one test a client ran.
type Result struct {
	Time          time.Time `json:"time"`
	Client        string    `json:"client"`
	Protocol      string    `json:"protocol"`  // tcp | udp
	Direction     string    `json:"direction"` // upload (client → box) | download (box → client, iperf3 -R)
	Streams       int       `json:"streams"`
	Seconds       float64   `json:"seconds"`
	BitsPerSecond float64   `json:"bits_per_second"`
	Retransmits   int       `json:"retransmits,omitempty"`  // TCP download only (the box is the sender)
	LostPercent   float64   `json:"lost_percent,omitempty"` // UDP
	JitterMs      float64   `json:"jitter_ms,omitempty"`    // UDP
}

// Manager runs the server.
type Manager struct {
	// Serve runs a one-off iperf3 server on addr:port and returns its JSON
	// report when a client's test ends (nil = the real iperf3).
	Serve func(ctx context.Context, addr string, port int) ([]byte, error)
	// SetOpen leaves exactly these devices open for the port in the
	// firewall; nil or empty closes it.
	SetOpen func(devices []string) error
	Now     func() time.Time // nil = time.Now

	mu     sync.Mutex
	st     Status
	cancel context.CancelFunc
	done   chan struct{}
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// Start opens the port on dev and serves tests on addr for d (0 = the
// default, at most MaxDuration).
func (m *Manager) Start(iface, dev, addr string, d time.Duration) (Status, error) {
	if d <= 0 {
		d = DefaultDuration
	}
	d = min(d, MaxDuration)
	m.mu.Lock()
	if m.st.Running {
		m.mu.Unlock()
		return Status{}, ErrRunning
	}
	if m.SetOpen != nil {
		if err := m.SetOpen([]string{dev}); err != nil {
			m.mu.Unlock()
			return Status{}, fmt.Errorf("open the firewall: %w", err)
		}
	}
	now := m.now()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	m.st = Status{Running: true, Interface: iface, Device: dev, Address: addr, Port: Port, Started: now, Expires: now.Add(d), Results: []Result{}}
	m.cancel = cancel
	m.done = make(chan struct{})
	done := m.done
	m.mu.Unlock()
	go m.loop(ctx, addr, done)
	return m.Status(), nil
}

func (m *Manager) loop(ctx context.Context, addr string, done chan struct{}) {
	defer close(done)
	serve := m.Serve
	if serve == nil {
		serve = iperf3
	}
	for ctx.Err() == nil {
		out, err := serve(ctx, addr, Port)
		if ctx.Err() != nil {
			break
		}
		r, perr := ParseResult(out, m.now())
		m.mu.Lock()
		switch {
		case perr == nil:
			m.st.Results = append(m.st.Results, r)
			if len(m.st.Results) > maxResults {
				m.st.Results = m.st.Results[len(m.st.Results)-maxResults:]
			}
			m.st.Error = ""
		case err != nil:
			m.st.Error = err.Error()
		default:
			m.st.Error = perr.Error()
		}
		m.mu.Unlock()
		if perr != nil {
			// a server that cannot start (address in use, no iperf3) would
			// spin: wait a little before the next try
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
			}
		}
	}
	m.mu.Lock()
	m.st.Running = false
	m.cancel = nil
	if m.SetOpen != nil {
		if err := m.SetOpen(nil); err != nil {
			m.st.Error = "close the firewall: " + err.Error()
		}
	}
	m.mu.Unlock()
}

// Stop ends the server now and waits until the port is closed.
func (m *Manager) Stop() {
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	m.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

// Status returns the current state.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.st
	st.Port = Port
	st.Results = append([]Result{}, m.st.Results...)
	return st
}

// Resync puts the running server's device back into the firewall set,
// which a ruleset load empties.
func (m *Manager) Resync() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.st.Running && m.SetOpen != nil {
		m.SetOpen([]string{m.st.Device})
	}
}

// iperf3 runs `iperf3 --server --one-off --json` on addr.
func iperf3(ctx context.Context, addr string, port int) ([]byte, error) {
	out, err := exec.CommandContext(ctx, "iperf3", "-s", "-1", "-J", "-B", addr, "-p", strconv.Itoa(port)).Output()
	if err != nil && len(out) == 0 {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("iperf3: %s", ee.Stderr)
		}
		return nil, fmt.Errorf("iperf3: %w", err)
	}
	return out, nil
}

// report is the part of iperf3's JSON output that is used.
type report struct {
	Start struct {
		Connected []struct {
			RemoteHost string `json:"remote_host"`
		} `json:"connected"`
		TestStart struct {
			Protocol   string `json:"protocol"`
			NumStreams int    `json:"num_streams"`
			Reverse    int    `json:"reverse"`
		} `json:"test_start"`
	} `json:"start"`
	End struct {
		SumSent struct {
			Seconds       float64 `json:"seconds"`
			BitsPerSecond float64 `json:"bits_per_second"`
			Retransmits   int     `json:"retransmits"`
		} `json:"sum_sent"`
		SumReceived struct {
			Seconds       float64 `json:"seconds"`
			BitsPerSecond float64 `json:"bits_per_second"`
		} `json:"sum_received"`
		Sum struct { // UDP
			Seconds       float64 `json:"seconds"`
			BitsPerSecond float64 `json:"bits_per_second"`
			JitterMs      float64 `json:"jitter_ms"`
			LostPercent   float64 `json:"lost_percent"`
		} `json:"sum"`
	} `json:"end"`
	Error string `json:"error"`
}

// ParseResult reads the server's JSON report of one test.
func ParseResult(b []byte, now time.Time) (Result, error) {
	var rep report
	if err := json.Unmarshal(b, &rep); err != nil {
		return Result{}, fmt.Errorf("iperf3 report: %w", err)
	}
	if rep.Error != "" {
		return Result{}, fmt.Errorf("iperf3: %s", rep.Error)
	}
	r := Result{Time: now, Streams: rep.Start.TestStart.NumStreams, Direction: "upload", Protocol: "tcp"}
	if len(rep.Start.Connected) > 0 {
		r.Client = rep.Start.Connected[0].RemoteHost
	}
	if rep.Start.TestStart.Reverse != 0 {
		r.Direction = "download"
	}
	switch {
	case rep.Start.TestStart.Protocol == "UDP":
		r.Protocol = "udp"
		r.Seconds, r.BitsPerSecond = rep.End.Sum.Seconds, rep.End.Sum.BitsPerSecond
		r.JitterMs, r.LostPercent = rep.End.Sum.JitterMs, rep.End.Sum.LostPercent
	case r.Direction == "download": // the box sent
		r.Seconds, r.BitsPerSecond, r.Retransmits = rep.End.SumSent.Seconds, rep.End.SumSent.BitsPerSecond, rep.End.SumSent.Retransmits
	default: // the box received
		r.Seconds, r.BitsPerSecond = rep.End.SumReceived.Seconds, rep.End.SumReceived.BitsPerSecond
	}
	if r.BitsPerSecond == 0 && r.Seconds == 0 {
		return Result{}, errors.New("iperf3 report without results")
	}
	return r, nil
}
