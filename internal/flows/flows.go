// Package flows samples the kernel's connection tracking table for the
// live flow table and per-device bandwidth (task 078). It reads
// /proc/net/nf_conntrack every few seconds; rates come from the byte
// counters' difference between two samples. The counters exist only with
// net.netfilter.nf_conntrack_acct=1 (and connection ages with
// nf_conntrack_timestamp=1): the sampler turns both on itself, because at
// boot systemd-sysctl runs before the conntrack module is loaded.
package flows

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Defaults.
const (
	DefaultTable    = "/proc/net/nf_conntrack"
	DefaultInterval = 3 * time.Second
	sysctlAcct      = "/proc/sys/net/netfilter/nf_conntrack_acct"
	sysctlTimestamp = "/proc/sys/net/netfilter/nf_conntrack_timestamp"
	maxEntries      = 200000 // the table can be huge on a busy box; beyond this, sampling stops reading
)

// Flow is one tracked connection, seen from the side that started it.
type Flow struct {
	Protocol  string     `json:"protocol"`
	Src       netip.Addr `json:"src"`
	SrcPort   int        `json:"src_port,omitempty"`
	Dst       netip.Addr `json:"dst"`
	DstPort   int        `json:"dst_port,omitempty"`
	State     string     `json:"state,omitempty"` // TCP state; "" for other protocols
	BytesOut  uint64     `json:"bytes_out"`       // from the side that started it
	BytesIn   uint64     `json:"bytes_in"`
	Packets   uint64     `json:"packets"`
	RateOut   float64    `json:"rate_out_bps"`
	RateIn    float64    `json:"rate_in_bps"`
	Age       int64      `json:"age_seconds,omitempty"`
	Device    netip.Addr `json:"-"` // the box's own network's address in the flow (who to count it for)
	DeviceIn  bool       `json:"-"` // the device received bytes_out (an inbound connection)
	SrcDevice *Device    `json:"src_device,omitempty"`
	key       string
}

// Device is a name for an address of the local networks (device inventory).
type Device struct {
	MAC  string `json:"mac,omitempty"`
	Name string `json:"name,omitempty"`
}

// DeviceRate is the bandwidth of one local address.
type DeviceRate struct {
	IP    netip.Addr `json:"ip"`
	MAC   string     `json:"mac,omitempty"`
	Name  string     `json:"name,omitempty"`
	RxBps float64    `json:"rx_bps"` // to the device
	TxBps float64    `json:"tx_bps"` // from the device
	Flows int        `json:"flows"`
}

// Sampler keeps the latest sample.
type Sampler struct {
	Table    string        // DefaultTable when ""
	Interval time.Duration // DefaultInterval when 0
	// Local reports whether an address belongs to one of the box's own
	// networks (LAN side); nil = private addresses.
	Local func(netip.Addr) bool
	// Names names local addresses (device inventory); nil = no names.
	Names  func(netip.Addr) (Device, bool)
	Logger *slog.Logger
	Now    func() time.Time

	mu      sync.Mutex
	local   []netip.Prefix // SetLocalPrefixes; overrides Local
	flows   []Flow
	at      time.Time
	prev    map[string][2]uint64
	prevAt  time.Time
	enabled bool
}

func (s *Sampler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// SetLocalPrefixes sets the box's own networks (goblind: after every apply,
// render.InternalPrefixes of the running config).
func (s *Sampler) SetLocalPrefixes(p []netip.Prefix) {
	s.mu.Lock()
	s.local = append([]netip.Prefix(nil), p...)
	s.mu.Unlock()
}

// Run samples until ctx ends.
func (s *Sampler) Run(ctx context.Context) {
	iv := s.Interval
	if iv == 0 {
		iv = DefaultInterval
	}
	t := time.NewTicker(iv)
	defer t.Stop()
	for {
		if err := s.Sample(); err != nil && s.Logger != nil {
			s.Logger.Warn("flows: sample", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// enableAccounting turns on the counters and timestamps once.
func (s *Sampler) enableAccounting() {
	if s.enabled || s.Table != "" && s.Table != DefaultTable {
		return
	}
	for _, p := range []string{sysctlAcct, sysctlTimestamp} {
		if b, err := os.ReadFile(p); err == nil && strings.TrimSpace(string(b)) == "0" {
			if err := os.WriteFile(p, []byte("1\n"), 0o644); err != nil && s.Logger != nil {
				s.Logger.Warn("flows: enable "+p, "error", err)
			}
		}
	}
	s.enabled = true
}

// Sample reads the table once.
func (s *Sampler) Sample() error {
	s.enableAccounting()
	path := s.Table
	if path == "" {
		path = DefaultTable
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	flows, err := Parse(f)
	if err != nil {
		return err
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	local := s.Local
	if s.local != nil {
		prefixes := s.local
		local = func(a netip.Addr) bool {
			for _, p := range prefixes {
				if p.Contains(a) {
					return true
				}
			}
			return false
		}
	}
	if local == nil {
		local = func(a netip.Addr) bool { return a.IsPrivate() || a.IsLinkLocalUnicast() }
	}
	dt := now.Sub(s.prevAt).Seconds()
	cur := make(map[string][2]uint64, len(flows))
	for i := range flows {
		fl := &flows[i]
		cur[fl.key] = [2]uint64{fl.BytesOut, fl.BytesIn}
		if p, ok := s.prev[fl.key]; ok && dt > 0 {
			if fl.BytesOut >= p[0] {
				fl.RateOut = float64(fl.BytesOut-p[0]) * 8 / dt
			}
			if fl.BytesIn >= p[1] {
				fl.RateIn = float64(fl.BytesIn-p[1]) * 8 / dt
			}
		}
		switch {
		case local(fl.Src):
			fl.Device = fl.Src
		case local(fl.Dst):
			fl.Device, fl.DeviceIn = fl.Dst, true
		}
		if fl.Device.IsValid() && s.Names != nil {
			if d, ok := s.Names(fl.Device); ok {
				dd := d
				fl.SrcDevice = &dd
			}
		}
	}
	sort.SliceStable(flows, func(i, j int) bool {
		return flows[i].RateOut+flows[i].RateIn > flows[j].RateOut+flows[j].RateIn
	})
	s.flows, s.at, s.prev, s.prevAt = flows, now, cur, now
	return nil
}

// Flows returns the latest sample, fastest first; ip (if valid) keeps the
// flows with that address on either side; limit <= 0 = all. total is the
// count before the limit.
func (s *Sampler) Flows(ip netip.Addr, limit int) (out []Flow, total int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, f := range s.flows {
		if ip.IsValid() && f.Src != ip && f.Dst != ip && f.Device != ip {
			continue
		}
		total++
		if limit <= 0 || len(out) < limit {
			out = append(out, f)
		}
	}
	return out, total
}

// Devices sums the latest sample per local address, busiest first.
func (s *Sampler) Devices() []DeviceRate {
	s.mu.Lock()
	defer s.mu.Unlock()
	by := map[netip.Addr]*DeviceRate{}
	for _, f := range s.flows {
		if !f.Device.IsValid() {
			continue
		}
		d := by[f.Device]
		if d == nil {
			d = &DeviceRate{IP: f.Device}
			if f.SrcDevice != nil {
				d.MAC, d.Name = f.SrcDevice.MAC, f.SrcDevice.Name
			}
			by[f.Device] = d
		}
		d.Flows++
		if f.DeviceIn { // started from outside: the device received what was sent
			d.RxBps += f.RateOut
			d.TxBps += f.RateIn
		} else {
			d.TxBps += f.RateOut
			d.RxBps += f.RateIn
		}
	}
	out := make([]DeviceRate, 0, len(by))
	for _, d := range by {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].RxBps+out[i].TxBps, out[j].RxBps+out[j].TxBps
		if a != b {
			return a > b
		}
		return out[i].IP.Less(out[j].IP)
	})
	return out
}

// Parse reads /proc/net/nf_conntrack lines, for example
//
//	ipv4 2 tcp 6 431999 ESTABLISHED src=192.168.1.10 dst=1.1.1.1 sport=51234 dport=443 packets=10 bytes=900 src=1.1.1.1 dst=203.0.113.2 sport=443 dport=51234 packets=8 bytes=4000 [ASSURED] mark=0 delta-time=12 use=1
//
// The first src/dst/sport/dport/packets/bytes belong to the original
// direction, the second to the reply (after NAT: the reply's src is the
// real target of a port forward).
func Parse(r io.Reader) ([]Flow, error) {
	var out []Flow
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 64*1024)
	for sc.Scan() {
		if len(out) >= maxEntries {
			break
		}
		f := strings.Fields(sc.Text())
		if len(f) < 6 {
			continue
		}
		fl := Flow{Protocol: f[2]}
		if fl.Protocol == "tcp" && !strings.Contains(f[5], "=") {
			fl.State = f[5] // ipv4 2 tcp 6 <timeout> <STATE> ...
		}
		var origDone, replySrcSet bool
		var replySrc netip.Addr
		var pkts [2]uint64
		var bytes [2]uint64
		dir := 0
		for _, tok := range f[4:] {
			k, v, ok := strings.Cut(tok, "=")
			if !ok {
				continue
			}
			switch k {
			case "src":
				if origDone {
					dir = 1
				}
				a, err := netip.ParseAddr(v)
				if err != nil {
					continue
				}
				if dir == 0 {
					fl.Src = a
				} else if !replySrcSet {
					replySrc, replySrcSet = a, true
				}
			case "dst":
				if a, err := netip.ParseAddr(v); err == nil && dir == 0 {
					fl.Dst = a
				}
			case "sport":
				if dir == 0 {
					fl.SrcPort, _ = strconv.Atoi(v)
				}
			case "dport":
				if dir == 0 {
					fl.DstPort, _ = strconv.Atoi(v)
					origDone = true
				}
			case "type": // icmp: type code id instead of ports
				if dir == 0 {
					origDone = true
				}
			case "packets":
				pkts[dir], _ = strconv.ParseUint(v, 10, 64)
			case "bytes":
				bytes[dir], _ = strconv.ParseUint(v, 10, 64)
			case "delta-time":
				fl.Age, _ = strconv.ParseInt(v, 10, 64)
			}
		}
		if !fl.Src.IsValid() || !fl.Dst.IsValid() {
			continue
		}
		// a port forward: the original destination is the box's WAN address,
		// the reply comes from the real target
		if replySrcSet && replySrc != fl.Dst {
			fl.Dst = replySrc
		}
		fl.BytesOut, fl.BytesIn, fl.Packets = bytes[0], bytes[1], pkts[0]+pkts[1]
		fl.key = f[2] + " " + fl.Src.String() + ":" + strconv.Itoa(fl.SrcPort) + " " + fl.Dst.String() + ":" + strconv.Itoa(fl.DstPort)
		out = append(out, fl)
	}
	return out, sc.Err()
}
