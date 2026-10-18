// Package capture runs packet captures for the web UI (task 076): one at a
// time, bounded in packets, time and bytes, with the filter built from
// validated fields (never a string handed to a shell). tcpdump only writes
// raw packets to its stdout (-w -), so it never decodes untrusted traffic;
// goblind saves the pcap and decodes the one-line summaries itself.
package capture

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// Limits.
const (
	MaxPackets     = 10000
	MaxSeconds     = 300
	MaxBytes       = 100 << 20
	DefaultPackets = 1000
	DefaultSeconds = 60
	DefaultSnaplen = 262 // headers and a little payload: enough to see what talks to what
	liveRate       = 50  // summaries per second to the live view; the rest are counted as dropped
)

// ErrBusy is returned while another capture runs.
var ErrBusy = errors.New("a capture is already running")

// ErrNotFound is returned for an unknown capture id.
var ErrNotFound = errors.New("no such capture")

// Request is a capture to start. Device is the kernel device (the API maps
// config interface names to it).
type Request struct {
	Interface  string     // config name, for display
	Device     string     // kernel device
	Host       netip.Addr // optional
	Port       int        // optional, 1-65535
	Protocol   string     // optional: tcp, udp, icmp, icmp6, arp
	MaxPackets int
	MaxSeconds int
	Snaplen    int
}

// Status is a capture's state.
type Status struct {
	ID        string     `json:"id"`
	State     string     `json:"state"` // running | done | failed
	Interface string     `json:"interface"`
	Filter    string     `json:"filter"`
	Packets   int        `json:"packets"`
	Bytes     int64      `json:"bytes"`
	Started   time.Time  `json:"started"`
	Finished  *time.Time `json:"finished,omitempty"`
	Error     string     `json:"error,omitempty"`
}

// Event is one message of the live stream: a packet, or a count of packets
// left out to keep the stream readable, or the end.
type Event struct {
	Packet  *Packet `json:"packet,omitempty"`
	Dropped int     `json:"dropped,omitempty"`
	Done    bool    `json:"done,omitempty"`
}

// Command starts the capture program with args and returns its stdout. The
// default runs tcpdump; tests replace it.
type Command func(ctx context.Context, args []string) (io.ReadCloser, func() error, error)

// Manager runs the captures.
type Manager struct {
	Dir     string // where the pcap file of the last capture is kept
	Command Command
	Now     func() time.Time

	mu   sync.Mutex
	cur  *run
	subs map[chan Event]bool
}

type run struct {
	status Status
	cancel context.CancelFunc
	done   chan struct{}
}

var devRE = regexp.MustCompile(`^[A-Za-z0-9_.:@-]{1,15}$`)

// filterArgs builds tcpdump's filter words from the validated fields.
func filterArgs(req Request) ([]string, error) {
	var parts [][]string
	if req.Protocol != "" {
		switch req.Protocol {
		case "tcp", "udp", "icmp", "icmp6", "arp":
			parts = append(parts, []string{req.Protocol})
		default:
			return nil, fmt.Errorf("protocol %q: want tcp, udp, icmp, icmp6 or arp", req.Protocol)
		}
	}
	if req.Host.IsValid() {
		parts = append(parts, []string{"host", req.Host.Unmap().String()})
	}
	if req.Port != 0 {
		if req.Port < 1 || req.Port > 65535 {
			return nil, fmt.Errorf("port %d out of range", req.Port)
		}
		if req.Protocol == "icmp" || req.Protocol == "icmp6" || req.Protocol == "arp" {
			return nil, fmt.Errorf("%s has no ports", req.Protocol)
		}
		parts = append(parts, []string{"port", strconv.Itoa(req.Port)})
	}
	var out []string
	for i, p := range parts {
		if i > 0 {
			out = append(out, "and")
		}
		out = append(out, p...)
	}
	return out, nil
}

// normalize fills defaults and checks limits.
func normalize(req *Request) error {
	if !devRE.MatchString(req.Device) {
		return fmt.Errorf("bad device %q", req.Device)
	}
	if req.MaxPackets == 0 {
		req.MaxPackets = DefaultPackets
	}
	if req.MaxSeconds == 0 {
		req.MaxSeconds = DefaultSeconds
	}
	if req.Snaplen == 0 {
		req.Snaplen = DefaultSnaplen
	}
	switch {
	case req.MaxPackets < 1 || req.MaxPackets > MaxPackets:
		return fmt.Errorf("max_packets must be 1-%d", MaxPackets)
	case req.MaxSeconds < 1 || req.MaxSeconds > MaxSeconds:
		return fmt.Errorf("max_seconds must be 1-%d", MaxSeconds)
	case req.Snaplen < 64 || req.Snaplen > 65535:
		return fmt.Errorf("snaplen must be 64-65535")
	}
	return nil
}

// tcpdumpArgs is the full command line: no name resolution, packet-buffered
// output, no privilege games (goblind reads the pipe).
func tcpdumpArgs(req Request, filter []string) []string {
	args := []string{"-i", req.Device, "-n", "-U", "-s", strconv.Itoa(req.Snaplen), "-c", strconv.Itoa(req.MaxPackets), "-w", "-"}
	return append(args, filter...)
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Manager) command() Command {
	if m.Command != nil {
		return m.Command
	}
	return func(ctx context.Context, args []string) (io.ReadCloser, func() error, error) {
		cmd := exec.CommandContext(ctx, "tcpdump", args...)
		out, err := cmd.StdoutPipe()
		if err != nil {
			return nil, nil, err
		}
		var stderr limitedBuffer
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			return nil, nil, fmt.Errorf("start tcpdump: %w", err)
		}
		return out, func() error {
			err := cmd.Wait()
			if err != nil && ctx.Err() == nil {
				return fmt.Errorf("tcpdump: %v: %s", err, stderr.String())
			}
			return nil
		}, nil
	}
}

// Start begins a capture.
func (m *Manager) Start(req Request) (Status, error) {
	if err := normalize(&req); err != nil {
		return Status{}, err
	}
	filter, err := filterArgs(req)
	if err != nil {
		return Status{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur != nil && m.cur.status.State == "running" {
		return Status{}, ErrBusy
	}
	if err := os.MkdirAll(m.Dir, 0o700); err != nil {
		return Status{}, err
	}
	if m.cur != nil {
		os.Remove(m.path(m.cur.status.ID))
	}
	idb := make([]byte, 8)
	rand.Read(idb)
	id := hex.EncodeToString(idb)
	f, err := os.OpenFile(m.path(id), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return Status{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(req.MaxSeconds)*time.Second)
	out, wait, err := m.command()(ctx, tcpdumpArgs(req, filter))
	if err != nil {
		cancel()
		f.Close()
		os.Remove(m.path(id))
		return Status{}, err
	}
	desc := "all traffic"
	if len(filter) > 0 {
		desc = joinWords(filter)
	}
	r := &run{status: Status{ID: id, State: "running", Interface: req.Interface, Filter: desc, Started: m.now().UTC()}, cancel: cancel, done: make(chan struct{})}
	m.cur = r
	go m.read(ctx, r, out, wait, f)
	return r.status, nil
}

func joinWords(w []string) string {
	s := ""
	for i, x := range w {
		if i > 0 {
			s += " "
		}
		s += x
	}
	return s
}

func (m *Manager) path(id string) string { return filepath.Join(m.Dir, id+".pcap") }

// read copies the pcap stream to the file, counts it and feeds the live view.
func (m *Manager) read(ctx context.Context, r *run, out io.ReadCloser, wait func() error, f *os.File) {
	defer close(r.done)
	defer r.cancel()
	var failure error
	pr, hdr, err := newPcapReader(out)
	if err == nil {
		_, err = f.Write(hdr)
	}
	if err != nil {
		failure = err
	}
	window, sent, dropped := m.now(), 0, 0
	for failure == nil {
		raw, at, origLen, data, err := pr.next()
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) && ctx.Err() == nil {
				failure = err
			}
			break
		}
		m.mu.Lock()
		if r.status.Bytes+int64(len(raw)) > MaxBytes {
			m.mu.Unlock()
			break
		}
		if _, err := f.Write(raw); err != nil {
			m.mu.Unlock()
			failure = err
			break
		}
		r.status.Packets++
		r.status.Bytes += int64(len(raw))
		n := r.status.Packets
		m.mu.Unlock()

		if now := m.now(); now.Sub(window) >= time.Second {
			if dropped > 0 {
				m.publish(Event{Dropped: dropped})
			}
			window, sent, dropped = now, 0, 0
		}
		if sent >= liveRate {
			dropped++
			continue
		}
		sent++
		src, dst, proto, info := summarize(pr.linkType, data)
		m.publish(Event{Packet: &Packet{N: n, Time: at, Src: src, Dst: dst, Protocol: proto, Length: origLen, Info: info}})
	}
	r.cancel() // stop tcpdump when we stopped first (byte limit, write error)
	io.Copy(io.Discard, out)
	out.Close()
	if err := wait(); err != nil && failure == nil && r.status.Packets == 0 {
		failure = err
	}
	f.Close()
	if dropped > 0 {
		m.publish(Event{Dropped: dropped})
	}
	m.mu.Lock()
	fin := m.now().UTC()
	r.status.Finished = &fin
	r.status.State = "done"
	if failure != nil {
		r.status.State, r.status.Error = "failed", failure.Error()
	}
	m.mu.Unlock()
	m.publish(Event{Done: true})
}

// Status returns the capture with that id.
func (m *Manager) Status(id string) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur == nil || m.cur.status.ID != id {
		return Status{}, ErrNotFound
	}
	return m.cur.status, nil
}

// File returns the pcap file of a finished (or running) capture.
func (m *Manager) File(id string) (string, error) {
	if _, err := m.Status(id); err != nil {
		return "", err
	}
	return m.path(id), nil
}

// Stop ends a running capture (it keeps its file); a finished one is
// deleted.
func (m *Manager) Stop(id string) error {
	m.mu.Lock()
	r := m.cur
	if r == nil || r.status.ID != id {
		m.mu.Unlock()
		return ErrNotFound
	}
	if r.status.State == "running" {
		m.mu.Unlock()
		r.cancel()
		<-r.done
		return nil
	}
	m.cur = nil
	m.mu.Unlock()
	return os.Remove(m.path(id))
}

// Wait blocks until the capture with that id has finished (tests).
func (m *Manager) Wait(id string) {
	m.mu.Lock()
	r := m.cur
	m.mu.Unlock()
	if r != nil && r.status.ID == id {
		<-r.done
	}
}

// Subscribe returns the live events of the current capture; cancel ends the
// subscription. A slow subscriber loses events rather than slowing the
// capture down.
func (m *Manager) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 256)
	m.mu.Lock()
	if m.subs == nil {
		m.subs = map[chan Event]bool{}
	}
	m.subs[ch] = true
	m.mu.Unlock()
	return ch, func() {
		m.mu.Lock()
		delete(m.subs, ch)
		m.mu.Unlock()
	}
}

func (m *Manager) publish(ev Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for ch := range m.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// limitedBuffer keeps the first 4 KB written to it (tcpdump's stderr).
type limitedBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := 4096 - len(b.buf); room > 0 {
		b.buf = append(b.buf, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
