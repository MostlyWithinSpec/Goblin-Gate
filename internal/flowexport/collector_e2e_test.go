package flowexport

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/flows"
)

// TestCollectorE2E sends one IPv4 and one IPv6 connection to a real collector
// (scripts/flow-export-test.ps1 runs GoFlow2 and checks every decoded record). It
// runs only when FLOWEXPORT_COLLECTOR (host:port) and FLOWEXPORT_PROTOCOL are
// set.
func TestCollectorE2E(t *testing.T) {
	collector := os.Getenv("FLOWEXPORT_COLLECTOR")
	if collector == "" {
		t.Skip("FLOWEXPORT_COLLECTOR not set")
	}
	e := &Exporter{}
	e.Configure(config.FlowExport{Enabled: true, Collector: collector, Protocol: os.Getenv("FLOWEXPORT_PROTOCOL")})
	start := time.Now()
	v6, err := flows.Parse(strings.NewReader("ipv6 10 udp 17 29 src=2001:db8::10 dst=2001:db8::53 sport=40000 dport=53 packets=1 bytes=80 src=2001:db8::53 dst=2001:db8::10 sport=53 dport=40000 packets=1 bytes=200 mark=0 use=1"))
	if err != nil || len(v6) != 1 {
		t.Fatalf("ipv6 fixture: %v %d", err, len(v6))
	}
	sample := []flows.Flow{tcpFlow(t, "192.168.1.10", "93.184.216.34", 51234, 443, 900, 12000, 25), v6[0]}
	e.Observe(start, sample)
	e.Observe(start.Add(3*time.Second), nil) // both connections ended
	ctx, cancel := context.WithTimeout(context.Background(), 2*FlushInterval+time.Second)
	defer cancel()
	e.Run(ctx)
	st := e.Status()
	if st.RecordsSent != 4 || st.LastError != "" {
		t.Fatalf("status %+v, want 4 records and no error", st)
	}
}
