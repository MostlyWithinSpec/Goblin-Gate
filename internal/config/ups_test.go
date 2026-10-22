package config

import (
	"strings"
	"testing"
)

func TestUPSValidation(t *testing.T) {
	ok := []UPS{
		{},
		{Enabled: true, Mode: "usb"},
		{Enabled: true, Mode: "usb", ShutdownPercent: 30, ShutdownRuntime: 300},
		{Enabled: true, Mode: "remote", Host: "nas.lan", Name: "apc", Username: "monuser", Password: "secret"},
		{Enabled: true, Mode: "remote", Host: "fd00::5", Port: 3494, Name: "ups", Username: "m", Password: "p"},
	}
	for _, u := range ok {
		c := Default()
		c.AllowNoAdmin = true
		c.UPS = u
		if errs := Validate(c); len(errs) > 0 {
			t.Errorf("%+v: %v", u, errs)
		}
	}
	bad := map[string]UPS{
		"ups.mode":             {Enabled: true, Mode: "serial"},
		"ups.host":             {Enabled: true, Mode: "remote", Host: "bad host", Name: "u", Username: "m", Password: "p"},
		"ups.name":             {Enabled: true, Mode: "remote", Host: "10.0.0.2", Name: "", Username: "m", Password: "p"},
		"ups.password":         {Enabled: true, Mode: "remote", Host: "10.0.0.2", Name: "u", Username: "m", Password: "a\"b"},
		"ups.shutdown_percent": {Enabled: true, Mode: "usb", ShutdownPercent: 99},
		"ups.shutdown_runtime": {Enabled: true, Mode: "usb", ShutdownRuntime: 10},
	}
	for path, u := range bad {
		c := Default()
		c.AllowNoAdmin = true
		c.UPS = u
		errs := Validate(c)
		found := false
		for _, e := range errs {
			found = found || e.Path == path
		}
		if !found {
			t.Errorf("%s: no error in %v", path, errs)
		}
	}
	// a remote enabled UPS needs its password
	c := Default()
	c.AllowNoAdmin = true
	c.UPS = UPS{Enabled: true, Mode: "remote", Host: "10.0.0.2", Name: "u", Username: "m"}
	if errs := Validate(c); len(errs) == 0 || !strings.Contains(errs[0].Path, "password") {
		t.Errorf("missing password accepted: %v", errs)
	}
}

func TestUPSTarget(t *testing.T) {
	for u, want := range map[UPS]string{
		{Mode: "usb"}: "ups@localhost",
		{Mode: "remote", Host: "nas", Name: "apc"}:               "apc@nas:3493",
		{Mode: "remote", Host: "fd00::5", Port: 4000, Name: "x"}: "x@[fd00::5]:4000",
	} {
		if got := u.Target(); got != want {
			t.Errorf("%+v: %s, want %s", u, got, want)
		}
	}
}
