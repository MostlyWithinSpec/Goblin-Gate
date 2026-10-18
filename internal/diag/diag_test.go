package diag

import (
	"context"
	"net"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

const pingOut = `PING 1.1.1.1 (1.1.1.1) 56(84) bytes of data.
64 bytes from 1.1.1.1: icmp_seq=1 ttl=57 time=11.2 ms
64 bytes from 1.1.1.1: icmp_seq=2 ttl=57 time=12.9 ms
64 bytes from 1.1.1.1: icmp_seq=4 ttl=57 time=10.8 ms

--- 1.1.1.1 ping statistics ---
4 packets transmitted, 3 received, 25% packet loss, time 1503ms
rtt min/avg/max/mdev = 10.811/11.633/12.901/0.912 ms
`

func TestPing(t *testing.T) {
	var got []string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		got = append([]string{name}, args...)
		return []byte(pingOut), nil
	}
	r, err := Ping(context.Background(), run, "1.1.1.1", 4, "ens192")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ping", "-n", "-c", "4", "-i", "0.5", "-W", "2", "-w", "9", "-I", "ens192", "1.1.1.1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args %q", got)
	}
	if r.Sent != 4 || r.Received != 3 || r.LossPercent != 25 || r.RTTMinMs != 10.811 || r.RTTAvgMs != 11.633 || r.RTTMaxMs != 12.901 || len(r.Lines) != 7 {
		t.Fatalf("%+v", r)
	}
	// no reply at all: summary only
	r = parsePing("10.9.9.9", "PING 10.9.9.9 ...\n\n--- 10.9.9.9 ping statistics ---\n2 packets transmitted, 0 received, 100% packet loss, time 1001ms\n")
	if r.Sent != 2 || r.Received != 0 || r.LossPercent != 100 || r.RTTAvgMs != 0 {
		t.Fatalf("no reply: %+v", r)
	}
}

func TestInputValidation(t *testing.T) {
	run := func(context.Context, string, ...string) ([]byte, error) { t.Fatal("must not run"); return nil, nil }
	for _, h := range []string{"-f", "--help", "", "a b", "x;rm -rf /", "a..b", strings.Repeat("a", 64) + ".com"} {
		if _, err := Ping(context.Background(), run, h, 1, ""); err == nil {
			t.Errorf("ping host %q accepted", h)
		}
		if _, err := Traceroute(context.Background(), run, h, 5); err == nil {
			t.Errorf("traceroute host %q accepted", h)
		}
	}
	if _, err := Ping(context.Background(), run, "1.1.1.1", 11, ""); err == nil {
		t.Error("count 11 accepted")
	}
	if _, err := Ping(context.Background(), run, "1.1.1.1", 1, "-x"); err == nil {
		t.Error("device -x accepted")
	}
	if _, err := Traceroute(context.Background(), run, "1.1.1.1", 31); err == nil {
		t.Error("31 hops accepted")
	}
	for _, h := range []string{"1.1.1.1", "2606:4700::1111", "example.com", "example.com.", "_dmarc.example.com", "localhost"} {
		if !ValidHost(h) {
			t.Errorf("%q refused", h)
		}
	}
}

func TestTraceroute(t *testing.T) {
	out := `traceroute to 1.1.1.1 (1.1.1.1), 20 hops max, 60 byte packets
 1  192.168.1.1  0.512 ms  0.401 ms  0.390 ms
 2  * * *
 3  10.0.0.1  1.204 ms 10.0.0.2  1.388 ms *
 4  1.1.1.1  9.870 ms !H  9.912 ms  10.001 ms
`
	var got []string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		got = append([]string{name}, args...)
		return []byte(out), nil
	}
	r, err := Traceroute(context.Background(), run, "1.1.1.1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != "traceroute -n -q 3 -w 2 -m 20 1.1.1.1" {
		t.Fatalf("args %q", got)
	}
	want := []Hop{
		{1, "192.168.1.1", []float64{0.512, 0.401, 0.390}},
		{2, "*", []float64{}},
		{3, "10.0.0.1", []float64{1.204, 1.388}},
		{4, "1.1.1.1", []float64{9.870, 9.912, 10.001}},
	}
	if !reflect.DeepEqual(r.Hops, want) {
		t.Fatalf("hops %+v", r.Hops)
	}
	Traceroute(context.Background(), run, "2001:db8::1", 5)
	if got[len(got)-2] != "-6" {
		t.Fatalf("v6 args %q", got)
	}
}

// fakeDNS answers every query with one A record (and NXDOMAIN for
// "missing.example.").
func fakeDNS(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	old := dnsPort
	dnsPort = uint16(pc.LocalAddr().(*net.UDPAddr).Port)
	t.Cleanup(func() { dnsPort = old })
	go func() {
		buf := make([]byte, 1500)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			var q dnsmessage.Message
			if q.Unpack(buf[:n]) != nil {
				continue
			}
			resp := dnsmessage.Message{Header: dnsmessage.Header{ID: q.Header.ID, Response: true}, Questions: q.Questions}
			switch {
			case q.Questions[0].Name.String() == "missing.example.":
				resp.Header.RCode = dnsmessage.RCodeNameError
			case q.Questions[0].Type == dnsmessage.TypePTR:
				resp.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: q.Questions[0].Name, Type: dnsmessage.TypePTR, Class: dnsmessage.ClassINET},
					Body: &dnsmessage.PTRResource{PTR: dnsmessage.MustNewName("router.lan.")}}}
			default:
				resp.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: q.Questions[0].Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET},
					Body: &dnsmessage.AResource{A: [4]byte{93, 184, 216, 34}}}}
			}
			b, _ := resp.Pack()
			pc.WriteTo(b, addr)
		}
	}()
}

func TestLookup(t *testing.T) {
	fakeDNS(t)
	r, err := Lookup(context.Background(), "example.com", "", "box")
	if err != nil {
		t.Fatal(err)
	}
	if r.Server != "127.0.0.1" || r.Type != "A" || r.RCode != "NOERROR" || len(r.Answers) != 1 || r.Answers[0] != "93.184.216.34" {
		t.Fatalf("%+v", r)
	}
	if r, err = Lookup(context.Background(), "missing.example", "A", "127.0.0.1"); err != nil || r.RCode != "NXDOMAIN" || len(r.Answers) != 0 {
		t.Fatalf("nxdomain: %+v %v", r, err)
	}
	if r, err = Lookup(context.Background(), "192.168.1.1", "ptr", ""); err != nil || r.Name != "1.1.168.192.in-addr.arpa" || r.Answers[0] != "router.lan." {
		t.Fatalf("ptr: %+v %v", r, err)
	}
	for _, bad := range [][3]string{{"-x", "A", ""}, {"example.com", "ANY", ""}, {"example.com", "A", "dns.google"}} {
		if _, err := Lookup(context.Background(), bad[0], bad[1], bad[2]); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if got := reverseName(netip.MustParseAddr("2001:db8::1")); !strings.HasPrefix(got, "1.0.0.0.") || !strings.HasSuffix(got, ".8.b.d.0.1.0.0.2.ip6.arpa") {
		t.Fatal(got)
	}
}
