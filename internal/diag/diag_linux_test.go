package diag

import (
	"context"
	"os/exec"
	"testing"
)

// The real tools (scripts/linux-test.ps1 installs them): their output must
// parse.
func TestRealTools(t *testing.T) {
	ctx := context.Background()
	if _, err := exec.LookPath("ping"); err != nil {
		t.Skip("ping not installed")
	}
	p, err := Ping(ctx, ExecRunner, "127.0.0.1", 2, "")
	if err != nil || p.Sent != 2 || p.Received != 2 || p.RTTAvgMs <= 0 {
		t.Fatalf("ping: %+v %v", p, err)
	}
	if _, err := exec.LookPath("traceroute"); err != nil {
		t.Skip("traceroute not installed")
	}
	tr, err := Traceroute(ctx, ExecRunner, "127.0.0.1", 3)
	if err != nil || len(tr.Hops) != 1 || tr.Hops[0].Address != "127.0.0.1" || len(tr.Hops[0].RTTMs) == 0 {
		t.Fatalf("traceroute: %+v %v", tr, err)
	}
}
