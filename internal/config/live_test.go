package config

import (
	"fmt"
	"strings"
	"testing"
)

func TestLiveAddressValidation(t *testing.T) {
	base := func() *Config {
		cfg := Default()
		cfg.Plugins = []Plugin{{Name: "homelab", Enabled: true, Capabilities: []string{"objects:write"}}}
		return cfg
	}
	cfg := base()
	cfg.Addresses = []Address{{Name: "containers", Type: "live", Value: "homelab"}}
	if errs := Validate(cfg); len(errs) > 0 {
		t.Fatalf("valid live object refused: %v", errs)
	}
	// a disabled plugin keeps its object
	cfg.Plugins[0].Enabled = false
	if errs := Validate(cfg); len(errs) > 0 {
		t.Fatalf("disabled owner refused: %v", errs)
	}
	for name, c := range map[string]struct {
		value string
		caps  []string
		want  string
	}{
		"not a plugin name": {"Home Lab", []string{"objects:write"}, "not a plugin name"},
		"no such plugin":    {"other", []string{"objects:write"}, "does not have objects:write"},
		"not approved":      {"homelab", []string{"stats:read"}, "does not have objects:write"},
	} {
		cfg := base()
		cfg.Plugins[0].Capabilities = c.caps
		cfg.Addresses = []Address{{Name: "containers", Type: "live", Value: c.value}}
		errs := Validate(cfg)
		if !hasErr(errs, "addresses[0].value") || !strings.Contains(fmt.Sprint(errs), c.want) {
			t.Errorf("%s: %v", name, errs)
		}
	}
	// at most MaxLiveObjectsPerPlugin, reported once
	cfg = base()
	for i := 0; i <= MaxLiveObjectsPerPlugin; i++ {
		cfg.Addresses = append(cfg.Addresses, Address{Name: fmt.Sprintf("l%d", i), Type: "live", Value: "homelab"})
	}
	errs := Validate(cfg)
	n := 0
	for _, e := range errs {
		if strings.Contains(e.Message, "live objects, at most") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("limit reported %d times: %v", n, errs)
	}
}
