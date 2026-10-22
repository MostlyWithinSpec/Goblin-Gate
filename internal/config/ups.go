package config

import (
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

// UPS watches an uninterruptible power supply through NUT (task 112): a
// UPS on the box's USB port, or one a NUT server elsewhere (a NAS, another
// box) already watches. On battery the box notifies; when the battery runs
// low it shuts down cleanly.
type UPS struct {
	Enabled bool `json:"enabled"`
	// Mode: "usb" runs NUT's driver and server on the box; "remote" only
	// monitors a NUT server elsewhere.
	Mode string `json:"mode,omitempty"`
	// Remote NUT server: host (an IP or a host name), port (0 = 3493), the
	// UPS's name there, and a NUT user with its password (a secret).
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	Name     string `json:"name,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	// ShutdownPercent / ShutdownRuntime: shut down once on battery and the
	// charge (%) or the runtime left (seconds) is at or below this; 0 =
	// when the UPS itself reports a low battery.
	ShutdownPercent int `json:"shutdown_percent,omitempty"`
	ShutdownRuntime int `json:"shutdown_runtime,omitempty"`
}

// UPSDefaultPort is NUT's port.
const UPSDefaultPort = 3493

// UPSLocalName is the name of a USB UPS in NUT's config on the box.
const UPSLocalName = "ups"

// Target returns the NUT address of the monitored UPS: "ups@localhost" or
// "name@host:port".
func (u UPS) Target() string {
	if u.Mode != "remote" {
		return UPSLocalName + "@localhost"
	}
	port := u.Port
	if port == 0 {
		port = UPSDefaultPort
	}
	host := u.Host
	if a, err := netip.ParseAddr(host); err == nil && a.Is6() {
		host = "[" + host + "]"
	}
	return u.Name + "@" + host + ":" + strconv.Itoa(port)
}

var (
	upsNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,63}$`)
	upsHostRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
)

func (v *validator) checkUPS() {
	u := v.cfg.UPS
	if !u.Enabled && u == (UPS{}) {
		return
	}
	if !oneOf(u.Mode, "usb", "remote") {
		v.add("ups.mode", "want usb or remote")
	}
	if u.Mode == "remote" {
		if _, err := netip.ParseAddr(u.Host); err != nil && !upsHostRe.MatchString(u.Host) {
			v.add("ups.host", "want the NUT server's IP address or host name")
		}
		if !upsNameRe.MatchString(u.Name) {
			v.add("ups.name", "want the UPS's name on the NUT server (upsc -l <server> lists them)")
		}
		if !upsNameRe.MatchString(u.Username) {
			v.add("ups.username", "want the NUT user (letters, digits, \"_\", \".\", \"-\")")
		}
		if u.Enabled && u.Password == "" {
			v.add("ups.password", "required for a remote NUT server")
		}
	}
	// the password is written into upsmon.conf: no quotes, no line breaks
	if strings.ContainsAny(u.Password, "\"\\\r\n") || len(u.Password) > 128 {
		v.add("ups.password", "at most 128 characters, without quotes, backslashes or line breaks")
	}
	if u.Port != 0 && (u.Port < 1 || u.Port > 65535) {
		v.add("ups.port", "want 1-65535")
	}
	if u.ShutdownPercent != 0 && (u.ShutdownPercent < 5 || u.ShutdownPercent > 90) {
		v.add("ups.shutdown_percent", "want 5-90 %%, or 0 for the UPS's own low-battery signal")
	}
	if u.ShutdownRuntime != 0 && (u.ShutdownRuntime < 60 || u.ShutdownRuntime > 3600) {
		v.add("ups.shutdown_runtime", "want 60-3600 seconds, or 0 for the UPS's own low-battery signal")
	}
}
