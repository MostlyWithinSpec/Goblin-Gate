package capture

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/netip"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// frames ---------------------------------------------------------------------

func eth(etherType uint16, payload []byte) []byte {
	b := make([]byte, 14, 14+len(payload))
	copy(b[0:6], []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	copy(b[6:12], []byte{0x02, 0, 0, 0, 0, 1})
	binary.BigEndian.PutUint16(b[12:], etherType)
	return append(b, payload...)
}

func ipv4(proto byte, src, dst string, l4 []byte) []byte {
	b := make([]byte, 20, 20+len(l4))
	b[0] = 0x45
	binary.BigEndian.PutUint16(b[2:], uint16(20+len(l4)))
	b[8], b[9] = 64, proto
	copy(b[12:16], netip.MustParseAddr(src).AsSlice())
	copy(b[16:20], netip.MustParseAddr(dst).AsSlice())
	return append(b, l4...)
}

func ipv6(next byte, src, dst string, l4 []byte) []byte {
	b := make([]byte, 40, 40+len(l4))
	b[0] = 0x60
	binary.BigEndian.PutUint16(b[4:], uint16(len(l4)))
	b[6], b[7] = next, 64
	copy(b[8:24], netip.MustParseAddr(src).AsSlice())
	copy(b[24:40], netip.MustParseAddr(dst).AsSlice())
	return append(b, l4...)
}

func tcpSeg(sp, dp uint16, flags byte) []byte {
	b := make([]byte, 20)
	binary.BigEndian.PutUint16(b, sp)
	binary.BigEndian.PutUint16(b[2:], dp)
	b[12], b[13] = 5<<4, flags
	return b
}

func udpSeg(sp, dp uint16) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint16(b, sp)
	binary.BigEndian.PutUint16(b[2:], dp)
	return b
}

// pcapStream writes a little-endian microsecond pcap of the frames.
func pcapStream(link uint32, frames ...[]byte) []byte {
	var b bytes.Buffer
	h := make([]byte, 24)
	binary.LittleEndian.PutUint32(h, 0xa1b2c3d4)
	binary.LittleEndian.PutUint16(h[4:], 2)
	binary.LittleEndian.PutUint16(h[6:], 4)
	binary.LittleEndian.PutUint32(h[16:], 65535)
	binary.LittleEndian.PutUint32(h[20:], link)
	b.Write(h)
	for i, f := range frames {
		r := make([]byte, 16)
		binary.LittleEndian.PutUint32(r, uint32(1760000000+i))
		binary.LittleEndian.PutUint32(r[4:], 250000)
		binary.LittleEndian.PutUint32(r[8:], uint32(len(f)))
		binary.LittleEndian.PutUint32(r[12:], uint32(len(f)))
		b.Write(r)
		b.Write(f)
	}
	return b.Bytes()
}

func TestSummarize(t *testing.T) {
	arpReq := make([]byte, 28)
	binary.BigEndian.PutUint16(arpReq[6:], 1)
	copy(arpReq[14:18], netip.MustParseAddr("192.168.1.10").AsSlice())
	copy(arpReq[24:28], netip.MustParseAddr("192.168.1.1").AsSlice())
	vlan := append([]byte{0, 20, 0x08, 0x00}, ipv4(17, "192.168.20.5", "192.168.20.1", udpSeg(40000, 53))...)
	for _, tc := range []struct {
		name              string
		link              uint32
		frame             []byte
		src, dst, p, info string
	}{
		{"tcp syn", linkEthernet, eth(0x0800, ipv4(6, "192.168.1.10", "93.184.216.34", tcpSeg(51234, 443, 0x02))), "192.168.1.10", "93.184.216.34", "tcp", "51234 → 443 [SYN]"},
		{"vlan dns", linkEthernet, eth(0x8100, vlan), "192.168.20.5", "192.168.20.1", "udp", "40000 → 53 DNS"},
		{"arp", linkEthernet, eth(0x0806, arpReq), "192.168.1.10", "192.168.1.1", "arp", "who has 192.168.1.1? tell 192.168.1.10"},
		{"icmp6 on raw ip", linkRaw, ipv6(58, "fe80::1", "ff02::1", []byte{134, 0, 0, 0}), "fe80::1", "ff02::1", "icmp6", "router advertisement"},
		{"ping", linkRaw, ipv4(1, "10.0.0.1", "10.0.0.2", []byte{8, 0, 0, 0}), "10.0.0.1", "10.0.0.2", "icmp", "echo request"},
		{"truncated tcp", linkEthernet, eth(0x0800, ipv4(6, "10.0.0.1", "10.0.0.2", []byte{1, 2}))[:36], "10.0.0.1", "10.0.0.2", "tcp", "truncated"},
		{"runt", linkEthernet, []byte{1, 2, 3}, "", "", "ethernet", "truncated"},
		{"bad ihl", linkEthernet, eth(0x0800, append([]byte{0x4f}, make([]byte, 19)...)), "", "", "ipv4", "bad header"},
	} {
		s, d, p, i := summarize(tc.link, tc.frame)
		if s != tc.src || d != tc.dst || p != tc.p || i != tc.info {
			t.Errorf("%s: got %q %q %q %q", tc.name, s, d, p, i)
		}
	}
}

func TestFilterAndLimits(t *testing.T) {
	args, err := filterArgs(Request{Protocol: "tcp", Host: netip.MustParseAddr("192.168.1.23"), Port: 8123})
	if err != nil || !reflect.DeepEqual(args, []string{"tcp", "and", "host", "192.168.1.23", "and", "port", "8123"}) {
		t.Fatalf("filter %v %v", args, err)
	}
	for _, bad := range []Request{{Protocol: "tcp; rm -rf /"}, {Protocol: "icmp", Port: 1}, {Port: 70000}} {
		if _, err := filterArgs(bad); err == nil {
			t.Errorf("filter accepted %+v", bad)
		}
	}
	for _, bad := range []Request{{Device: "eth0 -w /etc/x"}, {Device: "eth0", MaxPackets: MaxPackets + 1}, {Device: "eth0", MaxSeconds: 301}, {Device: "eth0", Snaplen: 10}} {
		r := bad
		if err := normalize(&r); err == nil {
			t.Errorf("limits accepted %+v", bad)
		}
	}
	r := Request{Device: "ens224.2"}
	if err := normalize(&r); err != nil || r.MaxPackets != DefaultPackets || r.MaxSeconds != DefaultSeconds || r.Snaplen != DefaultSnaplen {
		t.Fatalf("defaults %+v %v", r, err)
	}
	if got := tcpdumpArgs(r, []string{"arp"}); strings.Join(got, " ") != "-i ens224.2 -n -U -s 262 -c 1000 -w - arp" {
		t.Fatalf("args %v", got)
	}
}

// fakeCommand streams data and records the arguments; block keeps the
// stream open until the context ends (a running capture).
func fakeCommand(data []byte, block bool, gotArgs *[]string) Command {
	return func(ctx context.Context, args []string) (io.ReadCloser, func() error, error) {
		*gotArgs = args
		pr, pw := io.Pipe()
		go func() {
			pw.Write(data)
			if block {
				<-ctx.Done()
			}
			pw.Close()
		}()
		return pr, func() error { return nil }, nil
	}
}

func TestCapture(t *testing.T) {
	var args []string
	frames := [][]byte{
		eth(0x0800, ipv4(6, "192.168.1.10", "93.184.216.34", tcpSeg(51234, 443, 0x02))),
		eth(0x0800, ipv4(17, "192.168.1.10", "192.168.1.1", udpSeg(40000, 53))),
	}
	stream := pcapStream(linkEthernet, frames...)
	m := &Manager{Dir: t.TempDir(), Command: fakeCommand(stream, false, &args)}
	events, cancel := m.Subscribe()
	defer cancel()
	st, err := m.Start(Request{Interface: "lan", Device: "eth1", Protocol: "tcp"})
	if err != nil {
		t.Fatal(err)
	}
	m.Wait(st.ID)
	st, _ = m.Status(st.ID)
	if st.State != "done" || st.Packets != 2 || st.Interface != "lan" || st.Filter != "tcp" || st.Finished == nil {
		t.Fatalf("status %+v", st)
	}
	if args[1] != "eth1" || args[len(args)-1] != "tcp" {
		t.Fatalf("args %v", args)
	}
	path, _ := m.File(st.ID)
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, stream) {
		t.Fatalf("pcap file differs from the stream (%d vs %d bytes)", len(got), len(stream))
	}
	var pkts []Packet
	timeout := time.After(2 * time.Second)
	for done := false; !done; {
		select {
		case ev := <-events:
			if ev.Packet != nil {
				pkts = append(pkts, *ev.Packet)
			}
			done = ev.Done
		case <-timeout:
			t.Fatal("no done event")
		}
	}
	if len(pkts) != 2 || pkts[0].N != 1 || pkts[0].Info != "51234 → 443 [SYN]" || pkts[1].Protocol != "udp" || pkts[0].Length != len(frames[0]) {
		t.Fatalf("live %+v", pkts)
	}

	// deleting a finished capture removes its file
	if err := m.Stop(st.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("file kept")
	}
	if _, err := m.Status(st.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("status of a deleted capture")
	}
}

func TestCaptureBusyAndStop(t *testing.T) {
	var args []string
	m := &Manager{Dir: t.TempDir(), Command: fakeCommand(pcapStream(linkEthernet), true, &args)}
	st, err := m.Start(Request{Interface: "lan", Device: "eth1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(Request{Interface: "lan", Device: "eth1"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("second capture: %v", err)
	}
	if err := m.Stop(st.ID); err != nil {
		t.Fatal(err)
	}
	if st, _ := m.Status(st.ID); st.State != "done" || st.Filter != "all traffic" {
		t.Fatalf("stopped %+v", st)
	}
	// a new capture replaces the old one and its file
	old, _ := m.File(st.ID)
	st2, err := m.Start(Request{Interface: "lan", Device: "eth1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("old capture file kept")
	}
	m.Stop(st2.ID)
}

func TestCaptureBadStream(t *testing.T) {
	var args []string
	m := &Manager{Dir: t.TempDir(), Command: fakeCommand([]byte("not a pcap at all, really not"), false, &args)}
	st, err := m.Start(Request{Interface: "lan", Device: "eth1"})
	if err != nil {
		t.Fatal(err)
	}
	m.Wait(st.ID)
	if st, _ := m.Status(st.ID); st.State != "failed" || !strings.Contains(st.Error, "not a pcap") {
		t.Fatalf("status %+v", st)
	}
}
