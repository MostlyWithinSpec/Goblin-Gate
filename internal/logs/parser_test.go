package logs

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func fixtureLines(t *testing.T, name string) []string {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines
}

func TestParseNftables(t *testing.T) {
	lines := fixtureLines(t, "nftables.json")
	got, err := ParseNftables(lines[0])
	if err != nil {
		t.Fatal(err)
	}
	want := &Event{Time: 1791720001, Kind: "traffic", PolicyID: "3", SrcIP: "192.168.1.50", SrcPort: 51515,
		DstIP: "203.0.113.9", DstPort: 443, Proto: "tcp", InIntf: "eth1", OutIntf: "eth0", Bytes: 60}
	if d := cmp.Diff(want, got); d != "" {
		t.Fatal(d)
	}
	icmp, err := ParseNftables(lines[1])
	if err != nil || icmp.Proto != "icmp" || icmp.PolicyID != "7" || icmp.Bytes != 84 || icmp.DstPort != 0 {
		t.Fatalf("icmp: %v %+v", err, icmp)
	}
	if _, err := ParseNftables(lines[2]); !errors.Is(err, ErrSkip) {
		t.Fatalf("foreign prefix: %v", err)
	}
	// captured from ulogd2 2.0.8 (Debian 12): zoneless timestamp, oob.time.sec
	real, err := ParseNftables(lines[3])
	if err != nil || real.Time != 1791737574 || real.PolicyID != "5" || real.OutIntf != "lo" || real.DstPort != 9 || real.Proto != "tcp" {
		t.Fatalf("real ulogd line: %v %+v", err, real)
	}
	if _, err := ParseNftables("GG:1: not json"); err == nil || errors.Is(err, ErrSkip) {
		t.Fatalf("garbage: %v", err)
	}
}

func TestParseSuricata(t *testing.T) {
	lines := fixtureLines(t, "suricata.json")
	var got []*Event
	skipped := 0
	for _, l := range lines {
		e, err := ParseSuricata(l)
		if errors.Is(err, ErrSkip) {
			skipped++
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", l, err)
		}
		got = append(got, e)
	}
	if skipped != 2 || len(got) != 5 {
		t.Fatalf("skipped %d, parsed %d", skipped, len(got))
	}
	want := &Event{Time: 1791720005, Kind: "ips", Action: "drop", SrcIP: "198.51.100.23", SrcPort: 44321,
		DstIP: "192.168.1.10", DstPort: 22, Proto: "tcp", InIntf: "eth0",
		Signature: "2001219: ET SCAN Potential SSH Scan", Category: "Attempted Information Leak", Severity: "2",
		Message: "ET SCAN Potential SSH Scan"}
	if d := cmp.Diff(want, got[0]); d != "" {
		t.Fatal(d)
	}
	for i, w := range []struct{ kind, action, message string }{
		{"ips", "alert", "ET POLICY curl User-Agent Outbound"},
		{"dns", "allow", "example.com"},
		{"traffic", "allow", "SNI www.example.org"},
		{"traffic", "allow", ""},
	} {
		e := got[i+1]
		if e.Kind != w.kind || e.Action != w.action || e.Message != w.message {
			t.Errorf("event %d: %+v", i+1, e)
		}
	}
	if got[4].Bytes != 25500 || got[4].App != "tls" {
		t.Errorf("flow: %+v", got[4])
	}
	if _, err := ParseSuricata(`{"event_type":"alert","timestamp":"yesterday"}`); err == nil {
		t.Fatal("bad timestamp accepted")
	}
}
