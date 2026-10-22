package nut

import (
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

func render(t *testing.T, u config.UPS) map[string]string {
	t.Helper()
	c := config.Default()
	c.UPS = u
	files, err := Renderer{Secret: func() (string, error) { return "box-secret", nil }}.Render(c)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for k, v := range files {
		if !strings.HasPrefix(k, Dir) {
			t.Fatalf("file outside %s: %s", Dir, k)
		}
		out[strings.TrimPrefix(k, Dir)] = string(v)
	}
	return out
}

func TestOff(t *testing.T) {
	if f := render(t, config.UPS{Mode: "usb"}); len(f) != 0 {
		t.Fatalf("files while off: %v", f)
	}
}

func TestUSB(t *testing.T) {
	f := render(t, config.UPS{Enabled: true, Mode: "usb", ShutdownPercent: 30})
	if len(f) != 5 || !strings.Contains(f["nut.conf"], "MODE=standalone") {
		t.Fatalf("files %v", f)
	}
	ups := f["ups.conf"]
	for _, want := range []string{"[ups]", "driver = usbhid-ups", "ignorelb", "override.battery.charge.low = 30"} {
		if !strings.Contains(ups, want) {
			t.Errorf("ups.conf lacks %q:\n%s", want, ups)
		}
	}
	if strings.Contains(ups, "runtime.low") {
		t.Error("runtime override without a runtime threshold")
	}
	if !strings.Contains(f["upsd.conf"], "LISTEN 127.0.0.1 3493") || strings.Contains(f["upsd.conf"], "0.0.0.0") {
		t.Errorf("upsd listens beyond loopback:\n%s", f["upsd.conf"])
	}
	if !strings.Contains(f["upsd.users"], "password = box-secret") || !strings.Contains(f["upsmon.conf"], `MONITOR ups@localhost 1 goblin-monitor "box-secret" primary`) {
		t.Errorf("monitor login:\n%s\n%s", f["upsd.users"], f["upsmon.conf"])
	}
	// no thresholds: the UPS's own low-battery flag counts
	if f := render(t, config.UPS{Enabled: true, Mode: "usb"}); strings.Contains(f["ups.conf"], "ignorelb") {
		t.Error("ignorelb without thresholds")
	}
}

func TestRemote(t *testing.T) {
	f := render(t, config.UPS{Enabled: true, Mode: "remote", Host: "192.168.1.30", Port: 3494, Name: "apc", Username: "mon", Password: "pw"})
	if len(f) != 2 || !strings.Contains(f["nut.conf"], "MODE=netclient") {
		t.Fatalf("files %v", f)
	}
	if !strings.Contains(f["upsmon.conf"], `MONITOR apc@192.168.1.30:3494 1 mon "pw" secondary`) {
		t.Errorf("upsmon.conf:\n%s", f["upsmon.conf"])
	}
}

func TestUSBNeedsSecret(t *testing.T) {
	c := config.Default()
	c.UPS = config.UPS{Enabled: true, Mode: "usb"}
	if _, err := (Renderer{}).Render(c); err == nil {
		t.Fatal("rendered without a monitor password")
	}
}
