// Package main is the Goblin Gate management daemon.
package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// Settings are the daemon-wide settings, loaded from a file then overridden
// by command-line flags.
type Settings struct {
	Listen    string `json:"listen"`
	ConfigDir string `json:"config_dir"`
	LogLevel  string `json:"log_level"`
	DataDir   string `json:"data_dir"`
}

// DefaultSettings returns a Settings with sensible defaults.
func DefaultSettings() Settings {
	return Settings{
		Listen:    ":8443",
		ConfigDir: "/etc/goblingate",
		LogLevel:  "info",
		DataDir:   "/var/lib/goblind",
	}
}

// LoadSettingsFile reads a JSON settings file and returns a Settings. An
// empty path returns the defaults.
func LoadSettingsFile(path string) (Settings, error) {
	s := DefaultSettings()
	if path == "" {
		return s, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return s, fmt.Errorf("read settings %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("parse settings %s: %w", path, err)
	}
	return s, nil
}
