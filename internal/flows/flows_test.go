package flows

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// two samples 3 s apart: a LAN download, an outbound DNS query, a port
// forward to 192.168.1.50 and the box's own DNS lookup (no local side)
const sample1 = `ipv4     2 tcp      6 431999 ESTABLISHED src=192.168.1.10 dst=93.184.216.34 sport=51234 dport=443 packets=10 bytes=1000 src=93.184.216.34 dst=203.0.113.2 sport=443 dport=51234 packets=20 bytes=100000 [ASSURED] mark=257 delta-time=40 zone=0 use=2
ipv4     2 udp      17 25 src=192.168.1.10 dst=1.1.1.1 sport=40000 dport=53 packets=1 bytes=70 src=1.1.1.1 dst=203.0.113.2 sport=53 dport=40000 packets=1 bytes=120 mark=0 delta-time=1 zone=0 use=2
ipv4     2 tcp      6 300 ESTABLISHED src=198.51.100.9 dst=203.0.113.2 sport=60000 dport=32400 packets=5 bytes=500 src=192.168.1.50 dst=198.51.100.9 sport=32400 dport=60000 packets=5 bytes=50000 [ASSURED] mark=0 delta-time=100 zone=0 use=2
ipv4     2 udp      17 10 src=203.0.113.2 dst=45.83.251.16 sport=8143 dport=53 packets=1 bytes=60 src=45.83.251.16 dst=203.0.113.2 sport=53 dport=8143 packets=1 bytes=200 mark=0 zone=0 use=2
ipv4     2 icmp     1 29 src=192.168.1.10 dst=8.8.8.8 type=8 code=0 id=7 packets=1 bytes=84 src=8.8.8.8 dst=203.0.113.2 type=0 code=0 id=7 packets=1 bytes=84 mark=0 zone=0 use=2
`

const sample2 = `ipv4     2 tcp      6 431999 ESTABLISHED src=192.168.1.10 dst=93.184.216.34 sport=51234 dport=443 packets=40 bytes=4000 src=93.184.216.34 dst=203.0.113.2 sport=443 dport=51234 packets=320 bytes=3100000 [ASSURED] mark=257 delta-time=43 zone=0 use=2
ipv4     2 tcp      6 300 ESTABLISHED src=198.51.100.9 dst=203.0.113.2 sport=60000 dport=32400 packets=8 bytes=800 src=192.168.1.50 dst=198.51.100.9 sport=32400 dport=60000 packets=50 bytes=350000 [ASSURED] mark=0 delta-time=103 zone=0 use=2
`

func TestParse(t *testing.T) {
	flows, err := Parse(strings.NewReader(sample1))
	if err != nil {
		t.Fatal(err)
	}
	if len(flows) != 5 {
		t.Fatalf("%d flows", len(flows))
	}
	f := flows[0]
	if f.Protocol != "tcp" || f.State != "ESTABLISHED" || f.Src.String() != "192.168.1.10" || f.SrcPort != 51234 || f.Dst.String() != "93.184.216.34" ||
		f.DstPort != 443 || f.BytesOut != 1000 || f.BytesIn != 100000 || f.Packets != 30 || f.Age != 40 {
		t.Fatalf("tcp %+v", f)
	}
	if pf := flows[2]; pf.Dst.String() != "192.168.1.50" || pf.DstPort != 32400 {
		t.Fatalf("port forward keeps the real target: %+v", pf)
	}
	if ic := flows[4]; ic.Protocol != "icmp" || ic.State != "" || ic.Dst.String() != "8.8.8.8" || ic.BytesOut != 84 {
		t.Fatalf("icmp %+v", ic)
	}
	if u := flows[1]; u.State != "" || u.DstPort != 53 {
		t.Fatalf("udp %+v", u)
	}
}

func TestSamplerRates(t *testing.T) {
	dir := t.TempDir()
	table := filepath.Join(dir, "nf_conntrack")
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	lan := netip.MustParsePrefix("192.168.1.0/24")
	s := &Sampler{Table: table, Now: func() time.Time { return now },
		Local: func(netip.Addr) bool { return false }, // replaced by the prefixes below
		Names: func(a netip.Addr) (Device, bool) {
			if a.String() == "192.168.1.50" {
				return Device{MAC: "aa:bb:cc:00:00:50", Name: "plex"}, true
			}
			return Device{}, false
		}}
	s.SetLocalPrefixes([]netip.Prefix{lan})
	os.WriteFile(table, []byte(sample1), 0o644)
	if err := s.Sample(); err != nil {
		t.Fatal(err)
	}
	if f, total := s.Flows(netip.Addr{}, 0); total != 5 || f[0].RateIn != 0 {
		t.Fatalf("first sample has no rates: %d %+v", total, f)
	}
	now = now.Add(3 * time.Second)
	os.WriteFile(table, []byte(sample2), 0o644)
	if err := s.Sample(); err != nil {
		t.Fatal(err)
	}
	flows, total := s.Flows(netip.Addr{}, 1)
	if total != 2 || len(flows) != 1 {
		t.Fatalf("limit: %d %d", total, len(flows))
	}
	// download: 3 000 000 bytes in 3 s = 8 Mbit/s in, 3000 bytes = 8 kbit/s out
	if f := flows[0]; f.Dst.String() != "93.184.216.34" || f.RateIn != 8e6 || f.RateOut != 8000 {
		t.Fatalf("rates %+v", f)
	}
	byIP, _ := s.Flows(netip.MustParseAddr("192.168.1.50"), 0)
	if len(byIP) != 1 || byIP[0].SrcDevice == nil || byIP[0].SrcDevice.Name != "plex" {
		t.Fatalf("ip filter / names: %+v", byIP)
	}

	devs := s.Devices()
	if len(devs) != 2 {
		t.Fatalf("devices %+v", devs)
	}
	// the download is busiest; the port forward counts for its target, its
	// replies (to the internet) are the device's upload
	if d := devs[0]; d.IP.String() != "192.168.1.10" || d.RxBps != 8e6 || d.TxBps != 8000 || d.Flows != 1 {
		t.Fatalf("device 0 %+v", d)
	}
	if d := devs[1]; d.IP.String() != "192.168.1.50" || d.Name != "plex" || d.TxBps != 800000 || d.RxBps != 800 {
		t.Fatalf("device 1 %+v", d)
	}
}

func TestSamplerMissingTable(t *testing.T) {
	s := &Sampler{Table: filepath.Join(t.TempDir(), "none")}
	if err := s.Sample(); err == nil {
		t.Fatal("missing table accepted")
	}
}
