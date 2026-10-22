//go:build linux

package nut

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/ups"
)

// TestNUTE2E runs NUT for real with the rendered upsd.conf, upsd.users and
// upsmon.conf, NUT's dummy-ups driver standing in for a USB UPS, and the
// status poller reading it through upsc. Needs root and Debian's nut-server
// and nut-client (linux-test.ps1); skipped otherwise.
func TestNUTE2E(t *testing.T) {
	if os.Getenv("GOBLIN_NUT_E2E") == "" {
		t.Skip("set GOBLIN_NUT_E2E=1 (linux-test.ps1)")
	}
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	c := config.Default()
	c.UPS = config.UPS{Enabled: true, Mode: "usb"}
	files, err := Renderer{Secret: func() (string, error) { return "e2e-secret-0123456789abcdef0123", nil }}.Render(c)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range files {
		base := strings.TrimPrefix(name, Dir)
		switch base {
		case "ups.conf":
			// the dummy driver instead of usbhid-ups
			data = []byte("[ups]\n\tdriver = dummy-ups\n\tport = " + filepath.Join(dir, "ups.dev") + "\n\tdesc = \"e2e\"\n")
		case "upsmon.conf":
			// never shut the test machine down
			data = []byte(strings.Replace(string(data), `SHUTDOWNCMD "/sbin/shutdown -h +0"`, `SHUTDOWNCMD "/bin/true"`, 1))
		}
		if err := os.WriteFile(filepath.Join(dir, base), data, 0o640); err != nil {
			t.Fatal(err)
		}
	}
	dev := filepath.Join(dir, "ups.dev")
	writeDev := func(status, charge string) {
		t.Helper()
		body := "ups.status: " + status + "\nbattery.charge: " + charge + "\nbattery.runtime: 1500\ndevice.model: E2E UPS\n"
		if err := os.WriteFile(dev, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeDev("OL", "100")

	env := append(os.Environ(), "NUT_CONFPATH="+dir, "NUT_STATEPATH="+state)
	run := func(name string, args ...string) {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}
	run("/lib/nut/dummy-ups", "-a", "ups", "-u", "root")
	t.Cleanup(func() { exec.Command("pkill", "-f", "dummy-ups -a ups").Run() })
	// Debian's /sbin/upsd and /sbin/upsmon are wrappers that read
	// /etc/nut/nut.conf; the daemons themselves live in /lib/nut
	os.MkdirAll("/run/nut", 0o755)
	run("/lib/nut/upsd", "-u", "root")
	t.Cleanup(func() {
		cmd := exec.Command("/lib/nut/upsd", "-c", "stop")
		cmd.Env = env
		cmd.Run()
	})
	run("/lib/nut/upsmon", "-u", "root")
	t.Cleanup(func() {
		cmd := exec.Command("/lib/nut/upsmon", "-c", "stop")
		cmd.Env = env
		cmd.Run()
	})

	// upsmon logs in with the rendered MONITOR line and upsd.users account
	waitFor(t, "upsmon to log in", func() bool {
		cmd := exec.Command("upsc", "-c", "ups@localhost")
		cmd.Env = env
		out, _ := cmd.Output()
		return strings.Contains(string(out), "127.0.0.1") || strings.Contains(string(out), "::1")
	})

	var events []string
	m := &ups.Manager{
		Config:  func() (config.UPS, error) { return c.UPS, nil },
		Publish: func(kind, _, _ string) { events = append(events, kind) },
	}
	ctx := context.Background()
	waitFor(t, "an online reading", func() bool {
		m.Poll(ctx)
		st := m.Status()
		// dummy-ups reports its own model name
		return st.Connected && st.State == ups.StateOnline && st.Model != "" && st.Charge != nil
	})
	writeDev("OB DISCHRG", "60")
	waitFor(t, "the on-battery event", func() bool {
		m.Poll(ctx)
		return len(events) > 0 && events[0] == ups.EventOnBattery
	})
	writeDev("OL CHRG", "62")
	waitFor(t, "the back event", func() bool {
		m.Poll(ctx)
		return len(events) > 1 && events[1] == ups.EventBack
	})
	t.Logf("nut_linux: upsmon logged in, events %v", events)
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
