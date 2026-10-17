// Command sentry is the Goblin Sentry plugin (internal/sentry): it reads
// IPS alerts from goblind, turns them into incidents and decisions, and
// serves its API for the admin UI on GOBLIN_PLUGIN_API_SOCKET. The
// accepted G.O.B.L.I.N. model (internal/sentry/goblin) advises it.
//
// With arguments it runs the model's offline tools instead:
// run fixture|train|eval|llm-data (see goblin.Command).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/sentry"
	"github.com/networkgoblinlabs/goblin-gate/internal/sentry/goblin"
)

const (
	pollEvery    = 10 * time.Second
	networkEvery = 5 * time.Minute
	maxPerPoll   = 5000
)

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func main() {
	if len(os.Args) > 1 {
		os.Exit(goblin.Command(os.Args[1:], os.Stdout, os.Stderr))
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(logger); err != nil {
		logger.Error("sentry: stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	client := sentry.NewClient(env("GOBLIN_PLUGIN_SOCKET", "/run/goblin-plugins/api.sock"))

	var caps []string
	var settings map[string]any
	var err error
	for i := 0; ; i++ { // goblind may still be starting
		if caps, settings, err = client.Self(ctx); err == nil {
			break
		}
		if i == 30 || ctx.Err() != nil {
			return err
		}
		time.Sleep(2 * time.Second)
	}
	dir := env("STATE_DIRECTORY", ".")
	eng, err := sentry.New(dir, client, sentry.SettingsFrom(settings))
	if err != nil {
		return err
	}
	eng.Logger = logger
	models := &goblin.Store{Dir: dir}
	if idx, err := models.Index(); err != nil {
		logger.Warn("sentry: the decision model is unavailable", "error", err)
	} else {
		eng.Advisor = models
		logger.Info("sentry: decision model", "version", idx.Current(), "use", idx.Policy.Use)
	}
	logger.Info("sentry: started", "mode", eng.Mode(), "capabilities", caps)
	for _, need := range []string{"events:read", "notify:send", "blocks:read", "blocks:write"} {
		if !slices.Contains(caps, need) {
			logger.Warn("sentry: capability not approved, the tools that need it fail", "capability", need)
		}
	}
	if id, err := client.LatestAlertID(ctx); err == nil {
		eng.StartAt(id)
	}

	// the admin UI's API
	sock := env("GOBLIN_PLUGIN_API_SOCKET", "")
	if sock != "" {
		_ = os.Remove(sock)
		ln, err := net.Listen("unix", sock)
		if err != nil {
			return err
		}
		mux := http.NewServeMux()
		modelAPI := (&goblin.API{Store: models, Examples: filepath.Join(dir, "examples.jsonl"), Remove: eng.RemoveExample}).Handler()
		mux.Handle("/models", modelAPI)
		mux.Handle("/models/", modelAPI)
		mux.Handle("/", sentry.Handler(eng))
		srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
		go func() {
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("sentry: api", "error", err)
			}
		}()
		defer srv.Close()
	}

	refreshNetwork := func() {
		if !slices.Contains(caps, "config:read") {
			return
		}
		cfg, err := client.Config(ctx)
		if err != nil {
			logger.Warn("sentry: reading the config failed", "error", err)
			return
		}
		eng.SetNetwork(sentry.NetworkFrom(cfg))
	}
	refreshNetwork()
	poll := time.NewTicker(pollEvery)
	defer poll.Stop()
	netTick := time.NewTicker(networkEvery)
	defer netTick.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info("sentry: stopping")
			return nil
		case <-netTick.C:
			refreshNetwork()
		case <-poll.C:
			alerts, err := client.Alerts(ctx, eng.Cursor(), maxPerPoll)
			if err != nil {
				logger.Warn("sentry: reading alerts failed", "error", err)
				continue
			}
			eng.Ingest(ctx, alerts)
		}
	}
}
