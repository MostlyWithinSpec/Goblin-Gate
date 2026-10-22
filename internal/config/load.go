package config

import (
	"encoding/json"
	"fmt"
	"io"
)

// Load reads a config document from r. Unknown fields are rejected so that a
// config written by an incompatible version fails loudly instead of silently
// dropping data.
func Load(r io.Reader) (*Config, error) {
	var cfg Config
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("config load: %w", err)
	}
	// There is no nightly channel any more: untested builds don't belong on
	// a firewall. Boxes that followed it now follow beta.
	if cfg.System.UpdateChannel == "nightly" {
		cfg.System.UpdateChannel = "beta"
	}
	return &cfg, nil
}

// Marshal renders the config as indented JSON with stable field order (struct
// declaration order; maps sorted by key). The output ends with a newline.
func (c *Config) Marshal() ([]byte, error) {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("config marshal: %w", err)
	}
	return append(b, '\n'), nil
}
