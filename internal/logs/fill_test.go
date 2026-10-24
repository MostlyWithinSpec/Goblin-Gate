package logs

import (
	"path/filepath"
	"testing"
	"time"
)

func TestFillInterfaces(t *testing.T) {
	s, err := NewStore(filepath.Join(t.TempDir(), "logs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m := time.Now().Add(-10 * time.Minute).Unix()
	m -= m % 60
	s.InsertBatch([]*Event{
		{Time: m + 1, Kind: "traffic", SrcIP: "192.168.2.18", Bytes: 1000},
		{Time: m + 2, Kind: "traffic", SrcIP: "192.168.2.18", Bytes: 500},
		{Time: m + 3, Kind: "traffic", SrcIP: "104.16.1.1", Bytes: 70},      // outside: stays empty
		{Time: m + 4, Kind: "traffic", SrcIP: "192.168.1.5", InIntf: "lan"}, // already set
		{Time: m + 5, Kind: "ips", SrcIP: "192.168.2.18"},                   // not traffic
	})
	if err := s.RollupAt(m); err != nil {
		t.Fatal(err)
	}
	lookup := func(ip string) string {
		return map[string]string{"192.168.2.18": "wifi", "192.168.1.5": "lan"}[ip]
	}
	n, err := s.FillInterfaces(lookup)
	if err != nil || n != 2 {
		t.Fatalf("changed %d, %v; want 2", n, err)
	}
	var bytes int64
	if err := s.db.QueryRow(`SELECT COALESCE(SUM(bytes), 0) FROM rollup_1m WHERE time = ? AND in_intf = 'wifi'`, m).Scan(&bytes); err != nil || bytes != 1500 {
		t.Fatalf("rollup for wifi: %d %v", bytes, err)
	}
	var empty int
	s.db.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'traffic' AND in_intf = ''`).Scan(&empty)
	if empty != 1 {
		t.Fatalf("%d traffic events left without an interface, want 1 (the outside address)", empty)
	}
	if n, _ := s.FillInterfaces(lookup); n != 0 {
		t.Fatalf("second run changed %d", n)
	}
}
