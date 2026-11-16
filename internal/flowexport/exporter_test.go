package flowexport

import (
	"encoding/binary"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/flows"
)

// fakeConn collects what the exporter wrote, standing in for a UDP socket.
type fakeConn struct {
	packets [][]byte
	err     error
}

func (c *fakeConn) Read(b []byte) (int, error) { return 0, net.ErrClosed }
func (c *fakeConn) Write(b []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	b = append([]byte(nil), b...)
	c.packets = append(c.packets, b)
	return len(b), nil
}
func (c *fakeConn) Close() error                     { return nil }
func (c *fakeConn) LocalAddr() net.Addr              { return &net.UDPAddr{} }
func (c *fakeConn) RemoteAddr() net.Addr             { return &net.UDPAddr{} }
func (c *fakeConn) SetDeadline(time.Time) error      { return nil }
func (c *fakeConn) SetReadDeadline(time.Time) error  { return nil }
func (c *fakeConn) SetWriteDeadline(time.Time) error { return nil }

// testClock is a clock the test moves by hand, so timeouts need no real sleep.
type testClock struct{ t time.Time }

func (c *testClock) now() time.Time      { return c.t }
func (c *testClock) add(d time.Duration) { c.t = c.t.Add(d) }

func newTestExporter(t *testing.T, clock *testClock, cfg config.FlowExport) (*Exporter, *fakeConn) {
	t.Helper()
	e := &Exporter{Now: clock.now}
	conn := &fakeConn{}
	e.Dial = func(string) (net.Conn, error) { return conn, nil }
	e.Configure(cfg)
	e.builder.bootTime = clock.now()
	return e, conn
}

// sample feeds one sample through the sampler's observer path.
func sample(e *Exporter, at time.Time, fl ...flows.Flow) {
	e.Observe(at, fl)
}

// tcpFlow is a TCP connection from a LAN client to a server with the given
// counters. flows.Flow keeps its key private (only the conntrack parser fills
// it), so a test builds one through the parser itself.
func tcpFlow(t *testing.T, src, dst string, sport, dport int, out, in, pkts uint64) flows.Flow {
	t.Helper()
	line := fmt.Sprintf("ipv4 2 tcp 6 431999 ESTABLISHED src=%s dst=%s sport=%d dport=%d packets=%d bytes=%d src=%s dst=%s sport=%d dport=%d packets=%d bytes=%d [ASSURED] mark=0 delta-time=12 use=1",
		src, dst, sport, dport, pkts, out, dst, src, dport, sport, in/750, in)
	fs, err := flows.Parse(strings.NewReader(line))
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 {
		t.Fatalf("fixture line parsed into %d flows", len(fs))
	}
	return fs[0]
}

// dataPackets returns only the packets that carry flow records.
func dataPackets(pkts [][]byte) [][]byte {
	var out [][]byte
	for _, p := range pkts {
		if dataSet(p) != 0 {
			out = append(out, p)
		}
	}
	return out
}

// setIDs walks a packet's sets (v9 flowsets) and returns their IDs.
func setIDs(p []byte) []uint16 {
	off := ipfixHeaderSize
	if binUint16(p) == 9 {
		off = v9HeaderSize
	}
	var ids []uint16
	for off+4 <= len(p) {
		ids = append(ids, binUint16(p[off:]))
		n := int(binUint16(p[off+2:]))
		if n < 4 {
			break
		}
		off += n
	}
	return ids
}

// isTemplate reports whether a packet carries the templates (v9 flowset 0,
// IPFIX set 2), alone or in front of data.
func isTemplate(p []byte) bool {
	for _, id := range setIDs(p) {
		if id == v9TemplateFlowset && binUint16(p) == 9 || id == ipfixTemplateSet && binUint16(p) == 10 {
			return true
		}
	}
	return false
}

// dataSet returns the template ID of the packet's data set, 0 if none.
func dataSet(p []byte) uint16 {
	for _, id := range setIDs(p) {
		if id >= 256 {
			return id
		}
	}
	return 0
}

// v9Data decodes a v9 data packet's header: count and sequence.
func v9Data(t *testing.T, p []byte) (count int, sequence uint32) {
	t.Helper()
	if len(p) < v9HeaderSize {
		t.Fatalf("v9 packet too short: %d bytes", len(p))
	}
	if v := binUint16(p[0:]); v != 9 {
		t.Fatalf("v9 version %d", v)
	}
	return int(binUint16(p[2:])), binUint32(p[12:])
}

// ipfixData decodes an IPFIX packet's header: version, sequence and the number
// of records in its first data set.
func ipfixData(t *testing.T, p []byte) (version int, sequence uint32) {
	t.Helper()
	if len(p) < ipfixHeaderSize {
		t.Fatalf("ipfix packet too short: %d bytes", len(p))
	}
	return int(binUint16(p[0:])), binUint32(p[8:])
}

// v4Records decodes the (octets, packets) of every record in a packet's IPv4
// data set; the field order is the template's (addresses 8, protocol 1, ports
// 4, octets 8, packets 8, then the two timestamps).
func v4Records(t *testing.T, p []byte) [][2]uint64 {
	t.Helper()
	off := ipfixHeaderSize
	size := 45
	if binUint16(p) == 9 {
		off, size = v9HeaderSize, 37
	}
	for off+4 <= len(p) {
		id, n := binUint16(p[off:]), int(binUint16(p[off+2:]))
		if id == TemplateIDIPv4 {
			var out [][2]uint64
			for r := off + 4; r+size <= off+n; r += size {
				out = append(out, [2]uint64{binary.BigEndian.Uint64(p[r+13:]), binary.BigEndian.Uint64(p[r+21:])})
			}
			return out
		}
		off += n
	}
	t.Fatal("no IPv4 data set")
	return nil
}

func binUint16(b []byte) uint16 { return uint16(b[0])<<8 | uint16(b[1]) }
func binUint32(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

var testCfg = config.FlowExport{
	Enabled:   true,
	Collector: "127.0.0.1:2055",
	Protocol:  ProtocolIPFIX,
}

func TestFlowSeenThenGoneGivesOneRecord(t *testing.T) {
	for _, proto := range []string{ProtocolIPFIX, ProtocolNetFlow9} {
		t.Run(proto, func(t *testing.T) {
			clock := &testClock{t: time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)}
			cfg := testCfg
			cfg.Protocol = proto
			e, conn := newTestExporter(t, clock, cfg)
			f := tcpFlow(t, "192.168.1.10", "93.184.216.34", 51234, 443, 900, 4000, 18)
			sample(e, clock.now(), f)
			e.send(conn)
			if got := len(dataPackets(conn.packets)); got != 0 {
				t.Fatalf("a flow first seen now must not produce a record yet, got %d packets", got)
			}
			clock.add(3 * time.Second)
			f2 := f
			f2.BytesOut, f2.BytesIn, f2.Packets, f2.PacketsOut, f2.PacketsIn = 1900, 9000, 42, 30, 12
			sample(e, clock.now(), f2)
			e.send(conn)
			if got := len(dataPackets(conn.packets)); got != 0 {
				t.Fatalf("before the active timeout no record is due, got %d", got)
			}
			// the connection disappears from the next sample
			clock.add(3 * time.Second)
			sample(e, clock.now())
			e.send(conn)
			pkts := dataPackets(conn.packets)
			if len(pkts) != 1 {
				t.Fatalf("a flow that ended gives one data packet, got %d", len(pkts))
			}
			if got := e.Status().RecordsSent; got != 2 {
				t.Errorf("records_sent %d, want 2 (one per direction)", got)
			}
			if got := e.Status().OpenFlows; got != 0 {
				t.Errorf("open flows %d, want 0", got)
			}
			// the records carry the delta since the last one: nothing was
			// reported before, so the whole connection, each direction with
			// its own counters
			recs := v4Records(t, pkts[0])
			if len(recs) != 2 {
				t.Fatalf("expected two records, got %d", len(recs))
			}
			if recs[0] != [2]uint64{1900, 30} || recs[1] != [2]uint64{9000, 12} {
				t.Errorf("records (octets, packets) %v, want [1900 30] [9000 12]", recs)
			}
			if proto == ProtocolNetFlow9 {
				// the template packet went first and is packet 0
				if count, seq := v9Data(t, pkts[0]); count != 2 || seq != 1 {
					t.Errorf("v9 data packet count %d sequence %d, want 2/1", count, seq)
				}
			} else {
				if v, seq := ipfixData(t, pkts[0]); v != 10 || seq != 0 {
					t.Errorf("ipfix version %d sequence %d, want 10/0", v, seq)
				}
			}
		})
	}
}

func TestActiveTimeoutGivesDeltaRecords(t *testing.T) {
	clock := &testClock{t: time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)}
	cfg := testCfg
	cfg.ActiveTimeoutSeconds = 10
	e, conn := newTestExporter(t, clock, cfg)
	f := tcpFlow(t, "192.168.1.10", "93.184.216.34", 51234, 443, 100, 100, 4)
	sample(e, clock.now(), f)
	e.send(conn)
	// a long download: the same flow, growing counters. A record is due once
	// the flow has been alive a full active timeout and every timeout after, so
	// with 10 s and a 3 s sample at 12 s, 24 s and 36 s (the first sample at or
	// past each multiple of the timeout).
	want := []uint64{0, 0, 0, 2, 0, 0, 0, 2, 0, 0, 0, 2}
	for i, w := range want {
		clock.add(3 * time.Second)
		f.BytesOut += 500
		f.BytesIn += 250
		f.Packets += 5
		before := e.Status().RecordsSent
		sample(e, clock.now(), f)
		e.send(conn)
		if got := e.Status().RecordsSent - before; got != w {
			t.Errorf("sample %d (%d s): %d records, want %d", i+1, (i+1)*3, got, w)
		}
	}
	if got := e.Status().OpenFlows; got != 1 {
		t.Errorf("the flow is still open, open_flows %d", got)
	}
}

func TestTemplatesResend(t *testing.T) {
	clock := &testClock{t: time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)}
	e, conn := newTestExporter(t, clock, testCfg)
	if n := templatesIn(conn.packets); n != 0 {
		t.Fatalf("no packet yet, got %d template packets", n)
	}
	f := tcpFlow(t, "192.168.1.10", "93.184.216.34", 51234, 443, 10, 10, 2)
	// the first batch carries the templates
	sample(e, clock.now(), f)
	clock.add(3 * time.Second)
	sample(e, clock.now(), f)
	e.send(conn)
	if n, all := templatesIn(conn.packets), len(conn.packets); n != all || n == 0 {
		t.Errorf("first batch: %d of %d packets carry the templates, want all", n, all)
	}
	base := len(conn.packets)
	// then not again before the policy allows it
	for i := 0; i < 5; i++ {
		clock.add(3 * time.Second)
		f.BytesOut += 10
		sample(e, clock.now(), f)
		e.send(conn)
	}
	if n := templatesIn(conn.packets[base:]); n != 0 {
		t.Errorf("too early for a resend, got %d", n)
	}
	// the time rule: after 60 s the next batch resends the templates
	clock.add(60 * time.Second)
	sample(e, clock.now(), f)
	e.send(conn)
	if n := templatesIn(conn.packets[base:]); n < 1 {
		t.Errorf("after 60 s want a template resend, total %d", n)
	}
}

func templatesIn(pkts [][]byte) int {
	n := 0
	for _, p := range pkts {
		if isTemplate(p) {
			n++
		}
	}
	return n
}

func TestPacketStaysUnderTheLimit(t *testing.T) {
	clock := &testClock{t: time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)}
	e, conn := newTestExporter(t, clock, testCfg)
	// one flow per record, each with a distinct key, so every flow is due once
	// the active timeout passes and their records cannot fit one datagram
	var list []flows.Flow
	for i := 0; i < 200; i++ {
		list = append(list, tcpFlow(t, fmt.Sprintf("10.%d.%d.%d", i/65536, (i/256)%256, i%256), "93.184.216.34", 50000+i, 443, 10, 10, 2))
	}
	sample(e, clock.now(), list...)
	clock.add(time.Duration(len(list)+1) * time.Second)
	sample(e, clock.now(), list...)
	e.send(conn)
	pkts := dataPackets(conn.packets)
	if len(pkts) < 2 {
		t.Fatalf("%d flows should need several packets, got %d", len(list), len(pkts))
	}
	for i, p := range conn.packets {
		if len(p) > maxUDPPayload {
			t.Errorf("packet %d is %d bytes, over the %d limit", i, len(p), maxUDPPayload)
		}
	}
	if got := e.Status().LastError; got != "" {
		t.Errorf("last_error %q, want none", got)
	}
	// the records survived the split: each flow gives two (both directions)
	if got := e.Status().RecordsSent; got != uint64(2*len(list)) {
		t.Errorf("records_sent %d, want %d", got, 2*len(list))
	}
}

func TestMixedFamiliesUseSeparatePackets(t *testing.T) {
	clock := &testClock{t: time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)}
	e, conn := newTestExporter(t, clock, testCfg)
	v4 := tcpFlow(t, "192.168.1.10", "93.184.216.34", 51234, 443, 10, 10, 2)
	v6 := tcpFlow(t, "2001:db8::10", "2001:db8::20", 51234, 443, 10, 10, 2)
	sample(e, clock.now(), v4, v6)
	clock.add(70 * time.Second)
	sample(e, clock.now(), v4, v6)
	e.send(conn)
	pkts := dataPackets(conn.packets)
	if len(pkts) != 2 {
		t.Fatalf("one packet per family, got %d", len(pkts))
	}
	if v := binUint16(pkts[0]); v != 10 {
		t.Fatalf("version %d", v)
	}
	// each packet's data set ID is the template it used
	if id := dataSet(pkts[0]); id != TemplateIDIPv4 {
		t.Errorf("first packet uses template %d, want the IPv4 template %d", id, TemplateIDIPv4)
	}
	if id := dataSet(pkts[1]); id != TemplateIDIPv6 {
		t.Errorf("second packet uses template %d, want the IPv6 template %d", id, TemplateIDIPv6)
	}
}

func TestStatusShape(t *testing.T) {
	clock := &testClock{t: time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)}
	e, _ := newTestExporter(t, clock, testCfg)
	s := e.Status()
	if !s.Enabled || !s.Running {
		t.Errorf("configured exporter: %+v", s)
	}
	if s.Collector != "127.0.0.1:2055" || s.Protocol != ProtocolIPFIX {
		t.Errorf("status echoes the section: %+v", s)
	}
	e.Configure(config.FlowExport{})
	if s := e.Status(); s.Enabled || s.Running {
		t.Errorf("a disabled section must stop the exporter: %+v", s)
	}
}

// TestUDPToRealCollector sends to a listener on 127.0.0.1 over a real socket
// and decodes the headers the collector received.
func TestUDPToRealCollector(t *testing.T) {
	for _, tc := range []struct {
		proto   string
		version uint16
	}{
		{ProtocolNetFlow9, 9},
		{ProtocolIPFIX, 10},
	} {
		t.Run(tc.proto, func(t *testing.T) {
			ln, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			clock := &testClock{t: time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)}
			cfg := config.FlowExport{Enabled: true, Collector: ln.LocalAddr().String(), Protocol: tc.proto}
			e := &Exporter{Now: clock.now}
			e.Configure(cfg)
			e.builder.bootTime = clock.now()
			// a real socket, so the datagrams really cross the loopback
			conn, err := net.Dial("udp", ln.LocalAddr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			f := tcpFlow(t, "192.168.1.10", "93.184.216.34", 51234, 443, 900, 400, 12)
			sample(e, clock.now(), f)
			clock.add(70 * time.Second)
			sample(e, clock.now(), f)
			e.send(conn)
			got := e.Status()
			if got.PacketsSent == 0 {
				t.Fatalf("nothing reached the collector: %+v", got)
			}
			recv := readAll(t, ln, int(got.PacketsSent))
			var data [][]byte
			for i, p := range recv {
				if v := binUint16(p); v != tc.version {
					t.Errorf("packet %d version %d, want %d", i, v, tc.version)
				}
				if len(p) > maxUDPPayload {
					t.Errorf("packet %d is %d bytes", i, len(p))
				}
				if dataSet(p) != 0 {
					data = append(data, p)
				}
			}
			if len(data) == 0 {
				t.Fatal("no data packet arrived")
			}
			switch tc.proto {
			case ProtocolNetFlow9:
				// v9: count = template and data records in the packet (the
				// first one carries both templates), sequence = packets
				// sent before it.
				if count, seq := v9Data(t, data[0]); count != 4 || seq != 0 {
					t.Errorf("v9 count %d sequence %d, want 4/0", count, seq)
				}
				if len(data[0]) < v9HeaderSize {
					t.Fatalf("v9 packet shorter than its header")
				}
				if id := binUint32(data[0][16:]); id != ObservationDomain {
					t.Errorf("v9 source_id %d, want %d", id, ObservationDomain)
				}
			default:
				if v, seq := ipfixData(t, data[0]); v != 10 || seq != 0 {
					t.Errorf("ipfix version %d sequence %d, want 10/0", v, seq)
				}
				if id := binUint32(data[0][12:]); id != ObservationDomain {
					t.Errorf("ipfix observation domain %d, want %d", id, ObservationDomain)
				}
				// the packet's own length field must match what arrived
				if l := int(binUint16(data[0][2:])); l != len(data[0]) {
					t.Errorf("ipfix length field %d, packet is %d bytes", l, len(data[0]))
				}
			}
		})
	}
}

// readAll reads n datagrams already in the listener's queue (the exporter
// wrote them before this call, so none of them block).
func readAll(t *testing.T, ln *net.UDPConn, n int) [][]byte {
	t.Helper()
	if err := ln.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var out [][]byte
	buf := make([]byte, maxUDPPayload)
	for i := 0; i < n; i++ {
		m, _, err := ln.ReadFrom(buf)
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		out = append(out, append([]byte(nil), buf[:m]...))
	}
	return out
}

// A commit calls Configure with the same section: the open flows and their
// counters stay, so the next record is a delta, not the whole connection again.
func TestConfigureSameSectionKeepsCounters(t *testing.T) {
	clock := &testClock{t: time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)}
	e, conn := newTestExporter(t, clock, testCfg)
	sample(e, clock.now(), tcpFlow(t, "192.168.1.10", "93.184.216.34", 51234, 443, 1000, 5000, 10))
	clock.add(61 * time.Second)
	sample(e, clock.now(), tcpFlow(t, "192.168.1.10", "93.184.216.34", 51234, 443, 1000, 5000, 10))
	e.send(conn)         // active timeout: 1000 out / 5000 in reported
	e.Configure(testCfg) // a commit that did not touch flow_export
	clock.add(3 * time.Second)
	sample(e, clock.now(), tcpFlow(t, "192.168.1.10", "93.184.216.34", 51234, 443, 1500, 5000, 14))
	clock.add(3 * time.Second)
	sample(e, clock.now()) // the connection ended
	e.mu.Lock()
	pending := append([]Record(nil), e.pending...)
	e.mu.Unlock()
	if len(pending) != 1 || pending[0].Octets != 500 || pending[0].Packets != 4 {
		t.Fatalf("pending %+v, want one record of 500 octets and 4 packets (the rest was reported)", pending)
	}
}

// A changed section keeps the open flows too: only templates and sequence
// numbers start over.
func TestConfigureChangedSectionKeepsOpenFlows(t *testing.T) {
	clock := &testClock{t: time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)}
	e, _ := newTestExporter(t, clock, testCfg)
	sample(e, clock.now(), tcpFlow(t, "192.168.1.10", "93.184.216.34", 51234, 443, 1000, 5000, 10))
	cfg := testCfg
	cfg.Protocol = ProtocolNetFlow9
	e.Configure(cfg)
	if got := e.Status().OpenFlows; got != 1 {
		t.Errorf("open flows after a protocol change: %d, want 1", got)
	}
	cfg.Enabled = false
	e.Configure(cfg)
	if got := e.Status().OpenFlows; got != 0 {
		t.Errorf("open flows after disabling: %d, want 0", got)
	}
}
