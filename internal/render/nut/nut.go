// Package nut renders NUT (Network UPS Tools) for the ups config (task
// 112). With a UPS on USB the box runs the driver, upsd (listening on
// loopback only) and upsmon as the primary; with a remote NUT server it
// runs only upsmon as a secondary. upsmon shuts the box down cleanly when
// the battery runs low; goblind reads the state with upsc for the status
// page and the notifications.
package nut

import (
	"fmt"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// Dir is where NUT reads its configuration (Debian's nut packages).
const Dir = "/etc/nut/"

// MonitorUser is the upsd account upsmon logs in with on the box.
const MonitorUser = "goblin-monitor"

// Renderer renders /etc/nut/*.
type Renderer struct {
	// Secret returns the password of the box's own upsmon account (USB
	// mode). It is box state, not config: it never leaves the box.
	Secret func() (string, error)
}

func (Renderer) Name() string { return "nut" }

const header = "# Managed by Goblin Gate (System → UPS); changes here are overwritten.\n"

// Render returns nothing while the UPS is off: the stock nut.conf then
// comes back from the package on the next install, and the units are
// stopped by the applier.
func (r Renderer) Render(cfg *config.Config) (map[string][]byte, error) {
	u := cfg.UPS
	if !u.Enabled {
		return map[string][]byte{}, nil
	}
	files := map[string][]byte{}
	var mon strings.Builder
	mon.WriteString(header)
	switch u.Mode {
	case "usb":
		secret := ""
		if r.Secret != nil {
			s, err := r.Secret()
			if err != nil {
				return nil, fmt.Errorf("nut: monitor password: %w", err)
			}
			secret = s
		}
		if secret == "" {
			return nil, fmt.Errorf("nut: no monitor password")
		}
		files[Dir+"nut.conf"] = []byte(header + "MODE=standalone\n")
		files[Dir+"ups.conf"] = []byte(upsConf(u))
		files[Dir+"upsd.conf"] = []byte(header + "LISTEN 127.0.0.1 3493\nLISTEN ::1 3493\n")
		files[Dir+"upsd.users"] = []byte(fmt.Sprintf("%s[%s]\n\tpassword = %s\n\tupsmon primary\n", header, MonitorUser, secret))
		fmt.Fprintf(&mon, "MONITOR %s 1 %s \"%s\" primary\n", u.Target(), MonitorUser, secret)
	case "remote":
		files[Dir+"nut.conf"] = []byte(header + "MODE=netclient\n")
		fmt.Fprintf(&mon, "MONITOR %s 1 %s \"%s\" secondary\n", u.Target(), u.Username, u.Password)
	default:
		return nil, fmt.Errorf("nut: unknown mode %q", u.Mode)
	}
	mon.WriteString("MINSUPPLIES 1\n")
	mon.WriteString("SHUTDOWNCMD \"/sbin/shutdown -h +0\"\n")
	mon.WriteString("POWERDOWNFLAG /etc/killpower\n")
	mon.WriteString("POLLFREQ 5\nPOLLFREQALERT 5\nDEADTIME 15\nFINALDELAY 5\n")
	files[Dir+"upsmon.conf"] = []byte(mon.String())
	return files, nil
}

// upsConf describes the USB UPS. With a threshold set, the driver ignores
// the UPS's own low-battery flag and raises it at the threshold instead, so
// upsmon shuts down then.
func upsConf(u config.UPS) string {
	var b strings.Builder
	b.WriteString(header)
	b.WriteString("maxretry = 3\n\n")
	fmt.Fprintf(&b, "[%s]\n\tdriver = usbhid-ups\n\tport = auto\n\tdesc = \"Goblin Gate UPS\"\n", config.UPSLocalName)
	if u.ShutdownPercent > 0 || u.ShutdownRuntime > 0 {
		b.WriteString("\tignorelb\n")
		if u.ShutdownPercent > 0 {
			fmt.Fprintf(&b, "\toverride.battery.charge.low = %d\n", u.ShutdownPercent)
		}
		if u.ShutdownRuntime > 0 {
			fmt.Fprintf(&b, "\toverride.battery.runtime.low = %d\n", u.ShutdownRuntime)
		}
	}
	return b.String()
}
