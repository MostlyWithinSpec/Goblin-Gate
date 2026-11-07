// Command homeassistant is the Home Assistant plugin (internal/homeassistant):
// it publishes the box's state to an MQTT broker with Home Assistant's MQTT
// discovery and switches the policies the admin allowed.
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/homeassistant"
)

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(logger); err != nil {
		logger.Error("homeassistant: stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	client := homeassistant.NewClient(env("GOBLIN_PLUGIN_SOCKET", "/run/goblin-plugins/api.sock"))
	var caps []string
	var raw map[string]any
	var err error
	for i := 0; ; i++ { // goblind may still be starting
		if caps, raw, err = client.Self(ctx); err == nil {
			break
		}
		if i == 30 || ctx.Err() != nil {
			return err
		}
		time.Sleep(2 * time.Second)
	}
	settings, err := homeassistant.SettingsFrom(raw)
	if err != nil {
		// a setup problem, not a crash: wait for the admin to fix the
		// settings (that restarts the plugin) instead of failing over and over
		logger.Error("homeassistant: not started", "error", err)
		<-ctx.Done()
		return nil
	}
	for _, c := range []string{"stats:read", "devices:read", "policies:toggle"} {
		if !slices.Contains(caps, c) {
			logger.Info("homeassistant: capability not approved, its entities are left out", "capability", c)
		}
	}
	b := &homeassistant.Bridge{
		API: client, Settings: settings, Capabilities: caps, Logger: logger,
		Version: version(), StateDir: env("STATE_DIRECTORY", "."),
	}
	logger.Info("homeassistant: started", "broker", settings.Broker, "base_topic", settings.BaseTopic, "capabilities", caps)
	return b.Run(ctx)
}

// version is the installed plugin's, from its manifest.
func version() string {
	data, err := os.ReadFile(filepath.Join("/usr/share/goblin-gate/plugins", env("GOBLIN_PLUGIN_NAME", "homeassistant"), "plugin.json"))
	if err != nil {
		return ""
	}
	var m struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(data, &m)
	return m.Version
}
