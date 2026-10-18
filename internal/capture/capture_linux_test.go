//go:build linux

package capture

import (
	"net"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestRealTcpdump captures UDP on the loopback with the real tcpdump (root
// and tcpdump needed; scripts/linux-test.ps1 runs it in a container).
func TestRealTcpdump(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if _, err := exec.LookPath("tcpdump"); err != nil {
		t.Skip("tcpdump not installed")
	}
	m := &Manager{Dir: t.TempDir()}
	events, cancel := m.Subscribe()
	defer cancel()
	st, err := m.Start(Request{Interface: "lo", Device: "lo", Protocol: "udp", Port: 9999, MaxPackets: 3, MaxSeconds: 20})
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.Dial("udp", "127.0.0.1:9999")
	if err != nil {
		t.Fatal(err)
	}
	// send until tcpdump (which needs a moment to open the device) has its 3
	for s, _ := m.Status(st.ID); s.State == "running"; s, _ = m.Status(st.ID) {
		c.Write([]byte("goblin"))
		time.Sleep(200 * time.Millisecond)
	}
	c.Close()
	m.Wait(st.ID)
	st, _ = m.Status(st.ID)
	if st.State != "done" || st.Packets != 3 {
		t.Fatalf("status %+v", st)
	}
	var first *Packet
	for first == nil {
		select {
		case ev := <-events:
			first = ev.Packet
		case <-time.After(2 * time.Second):
			t.Fatal("no packet event")
		}
	}
	if first.Src != "127.0.0.1" || first.Protocol != "udp" {
		t.Fatalf("packet %+v", first)
	}
}
