package flowexport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/flows"
)

// ResolveInterval is how often a DNS collector name is looked up again, so a
// collector on a dynamic address follows it without a restart.
const ResolveInterval = 5 * time.Minute

// FlushInterval is how often the send loop wakes to push what Observe queued.
// It matches the sampler, so a sample's records leave within one tick; a box
// with no traffic still gets a wake-up to close open flows.
const FlushInterval = flows.DefaultInterval

// MaxPending bounds the records waiting for the collector. While it cannot be
// reached (no address, no route), the oldest records are dropped and counted
// in Status.RecordsDropped, so a busy box does not grow without limit.
const MaxPending = 100000

// Status is the exporter's state for GET /api/v1/flow-export.
type Status struct {
	Enabled     bool   `json:"enabled"`
	Collector   string `json:"collector,omitempty"`
	Protocol    string `json:"protocol,omitempty"`
	Running     bool   `json:"running"`
	PacketsSent uint64 `json:"packets_sent"`
	RecordsSent uint64 `json:"records_sent"`
	// RecordsDropped counts records thrown away because the collector could
	// not be reached for longer than MaxPending records.
	RecordsDropped uint64    `json:"records_dropped"`
	LastError      string    `json:"last_error,omitempty"`
	LastSent       time.Time `json:"last_sent,omitzero"`
	OpenFlows      int       `json:"open_flows"`
}

// openFlow is one connection the exporter has seen and not yet reported as
// finished, with the counters the last record left over.
type openFlow struct {
	key      string
	flow     flows.Flow
	start    time.Time // first sample that saw it
	lastSent time.Time // sample time of the last record for it
	// sent is what the last record reported, so every record is a delta.
	sent flows.Flow
}

// recordsSince returns the records for f's counters since the last record of
// of (none when nothing moved), and remembers f's counters as reported.
func (of *openFlow) recordsSince(f flows.Flow, end time.Time) []Record {
	d := f
	d.BytesOut, d.BytesIn = sub(f.BytesOut, of.sent.BytesOut), sub(f.BytesIn, of.sent.BytesIn)
	d.PacketsOut, d.PacketsIn = sub(f.PacketsOut, of.sent.PacketsOut), sub(f.PacketsIn, of.sent.PacketsIn)
	of.sent = f
	return recordsOf(d, of.lastSent, end)
}

// Exporter turns sampled connections into NetFlow v9 or IPFIX records and
// sends them to a UDP collector. Observe is the flows.Sampler observer hook: it
// runs on the sampling goroutine and never blocks on the network — a slow
// collector delays a sample at worst, and a collector that is down only shows up
// in Status.LastError.
type Exporter struct {
	Logger *slog.Logger
	Now    func() time.Time // test hook; defaults to time.Now
	// LookupHost resolves the collector's name; nil = net.LookupHost.
	LookupHost func(host string) ([]string, error)
	// Dial opens the UDP socket to an address; nil = net.Dial("udp", addr).
	// Tests replace it to count packets without a listener.
	Dial func(addr string) (net.Conn, error)

	mu      sync.Mutex
	cfg     config.FlowExport
	pending []Record
	open    map[string]*openFlow
	builder *packetBuilder
	status  Status
	closed  bool
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

// Configure sets the section to export with. goblind calls it on every
// commit, so an unchanged section changes nothing: resetting the open flows
// would make the next record of every long connection report its bytes since
// the connection began a second time. A changed section restarts the packet
// builder (new templates and sequence numbers) and keeps the open flows and
// their counters; an empty collector or a disabled section stops sending and
// forgets them.
func (e *Exporter) Configure(cfg config.FlowExport) {
	e.mu.Lock()
	defer e.mu.Unlock()
	on := cfg.Enabled && cfg.Collector != ""
	if cfg == e.cfg && on == (e.builder != nil) {
		return
	}
	e.cfg = cfg
	if !on {
		e.status.Enabled = false
		e.status.Running = false
		e.builder = nil
		e.open = nil
		e.pending = nil
		return
	}
	e.status.Enabled = true
	e.status.Running = true // configured: Run owns the socket and will keep trying
	e.status.Collector = cfg.Collector
	e.status.Protocol = protocolName(cfg.Protocol)
	e.builder = newPacketBuilder(e.status.Protocol, e.now())
	if e.open == nil {
		e.open = map[string]*openFlow{}
	}
}

func protocolName(p string) string {
	if p == ProtocolNetFlow9 {
		return ProtocolNetFlow9
	}
	return ProtocolIPFIX
}

// ProtocolName maps a config value to the protocol the exporter reports, so a
// status handler cannot disagree with the encoder ("", empty or unknown = ipfix).
func ProtocolName(p string) string { return protocolName(p) }

// Observe is the flows.Sampler observer: it takes the whole sample and turns it
// into records. Flows that vanished since the last sample are finished; flows
// older than the active timeout get a delta record now.
func (e *Exporter) Observe(at time.Time, list []flows.Flow) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.builder == nil || e.closed {
		return
	}
	timeout := time.Duration(e.cfg.ActiveTimeout()) * time.Second
	seen := make(map[string]bool, len(list))
	var recs []Record
	for _, f := range list {
		seen[f.Key()] = true
		of, ok := e.open[f.Key()]
		if !ok {
			// conntrack does not say when a flow started, so the first sample
			// that saw it starts the flow's record window.
			of = &openFlow{key: f.Key(), flow: f, start: at, lastSent: at}
			e.open[f.Key()] = of
		}
		of.flow = f
		if at.Sub(of.lastSent) < timeout {
			continue // not due yet; the counters wait for the record that follows
		}
		// delta counters since the record before this one
		recs = append(recs, of.recordsSince(f, at)...)
		// the next record is due a full timeout after the one just sent. Keep
		// lastSent on the sample that produced the record (not on the ideal
		// timeout boundary): with a 3 s sampler and a 10 s timeout, snapping to
		// 10 s/20 s/30 s would leave a 3 s gap at 21 s and emit a record twice
		// as often as asked.
		of.lastSent = at
	}
	for key, of := range e.open {
		if seen[key] {
			continue
		}
		// the connection left the conntrack table: report what is left of it
		recs = append(recs, of.recordsSince(of.flow, at)...)
		delete(e.open, key)
	}
	e.pending = append(e.pending, recs...)
	e.trimPending()
}

// trimPending drops the oldest pending records beyond MaxPending. e.mu held.
func (e *Exporter) trimPending() {
	if over := len(e.pending) - MaxPending; over > 0 {
		e.pending = append(e.pending[:0:0], e.pending[over:]...)
		e.status.RecordsDropped += uint64(over)
	}
}

func sub(a, b uint64) uint64 {
	if a < b {
		return 0
	}
	return a - b
}

// Run resolves the collector, sends what Observe queued, and re-resolves the
// name every ResolveInterval. It blocks until ctx is done.
func (e *Exporter) Run(ctx context.Context) {
	var (
		conn net.Conn
		addr string
	)
	defer func() {
		if conn != nil {
			conn.Close()
		}
	}()
	resolve := time.Time{}
	collector := "" // the configured name the socket was opened for
	tick := time.NewTicker(FlushInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			e.flushOnStop(conn)
			return
		case <-tick.C:
		}
		now := e.now()
		e.mu.Lock()
		want, off := e.cfg.Collector, e.builder == nil
		e.mu.Unlock()
		if off {
			// disabled: drop the socket, and do not resolve an empty name
			if conn != nil {
				conn.Close()
				conn, addr, collector = nil, "", ""
			}
			continue
		}
		if conn == nil || want != collector || now.Sub(resolve) >= ResolveInterval {
			a, err := e.resolve()
			if err != nil {
				e.setError(err.Error())
				continue
			}
			if a != addr && conn != nil {
				conn.Close()
				conn = nil
			}
			if conn == nil {
				c, err := e.dial(a)
				if err != nil {
					e.setError(err.Error())
					continue
				}
				conn = c
				addr = a
			}
			resolve = now
			collector = want
		}
		e.send(conn)
	}
}

func (e *Exporter) resolve() (string, error) {
	e.mu.Lock()
	collector := e.cfg.Collector
	e.mu.Unlock()
	host, port, err := net.SplitHostPort(collector)
	if err != nil {
		return "", err
	}
	if host == "" {
		return "", errors.New("flow_export: collector has no host")
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return net.JoinHostPort(ip.String(), port), nil
	}
	lookup := e.LookupHost
	if lookup == nil {
		lookup = net.LookupHost
	}
	addrs, err := lookup(host)
	if err != nil {
		return "", err
	}
	if len(addrs) == 0 {
		return "", errors.New("flow_export: collector name resolved to nothing")
	}
	// A dual-stack collector is reached over IPv6, which is what most
	// collectors of this generation listen on.
	family := false
	for _, a := range addrs {
		if ip, err := netip.ParseAddr(a); err == nil && ip.Is6() {
			family = true
			break
		}
	}
	for _, a := range addrs {
		if ip, err := netip.ParseAddr(a); err == nil && ip.Is6() == family {
			return net.JoinHostPort(a, port), nil
		}
	}
	return "", errors.New("flow_export: collector name has no usable address")
}

func (e *Exporter) dial(addr string) (net.Conn, error) {
	if e.Dial != nil {
		return e.Dial(addr)
	}
	c, err := net.Dial("udp", addr)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// send writes everything queued since the last call and updates the counters.
func (e *Exporter) send(conn net.Conn) {
	e.mu.Lock()
	recs := e.pending
	e.pending = nil
	builder := e.builder
	e.mu.Unlock()
	if builder == nil || conn == nil {
		e.mu.Lock()
		e.pending = append(recs, e.pending...)
		e.trimPending()
		e.mu.Unlock()
		return
	}
	at := e.now()
	b, err := builder.add(at, recs)
	if err != nil {
		e.setError(err.Error())
		return
	}
	for i, p := range b.packets {
		if len(p) > maxUDPPayload {
			// Cannot happen with these templates, but a collector that has to
			// reassemble an IP fragment is a collector that drops the packet.
			e.setError(fmt.Sprintf("flow_export: packet %d is %d bytes, over the %d-byte limit", i, len(p), maxUDPPayload))
			continue
		}
		if _, err := conn.Write(p); err != nil {
			e.setError(err.Error())
			return
		}
		e.count(b.records[i])
	}
}

// count adds one sent packet to the status; records is how many flow records
// it carried (0 for a template packet).
func (e *Exporter) count(records int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.status.PacketsSent++
	e.status.RecordsSent += uint64(records)
	e.status.LastSent = e.now()
	e.status.LastError = ""
}

func (e *Exporter) setError(msg string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.status.LastError = msg
}

// flushOnStop queues every flow still open, so a restart does not lose its
// counters, and sends what is left while the socket is still open.
func (e *Exporter) flushOnStop(conn net.Conn) {
	e.mu.Lock()
	at := e.now()
	for key, of := range e.open {
		e.pending = append(e.pending, of.recordsSince(of.flow, at)...)
		delete(e.open, key)
	}
	e.closed = true
	e.mu.Unlock()
	e.send(conn)
}

// Status returns the exporter's state.
func (e *Exporter) Status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.status
	s.OpenFlows = len(e.open)
	s.Enabled = e.builder != nil
	return s
}
