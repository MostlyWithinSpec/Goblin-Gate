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
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/sentry"
	"github.com/networkgoblinlabs/goblin-gate/internal/sentry/goblin"
	"github.com/networkgoblinlabs/goblin-gate/pkg/pluginsdk"
)

const (
	pollEvery    = 10 * time.Second
	networkEvery = 5 * time.Minute
	maxPerPoll   = 5000
)

func main() {
	if len(os.Args) > 1 {
		os.Exit(goblin.Command(os.Args[1:], os.Stdout, os.Stderr))
	}
	pluginsdk.Run(run)
}

func run(ctx context.Context, p *pluginsdk.Plugin) error {
	logger := p.Logger
	client := &sentry.Client{Client: *p.Client}
	caps := p.Self.Capabilities
	dir := p.Env.StateDir
	eng, err := sentry.New(dir, client, sentry.SettingsFrom(p.Self.Settings))
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
	for _, need := range []string{pluginsdk.CapEventsRead, pluginsdk.CapNotifySend, pluginsdk.CapBlocksRead, pluginsdk.CapBlocksWrite} {
		if !p.Self.Has(need) {
			logger.Warn("sentry: capability not approved, the tools that need it fail", "capability", need)
		}
	}
	if id, err := client.LatestAlertID(ctx); err == nil {
		eng.StartAt(id)
	}

	// the admin UI's API
	mux := http.NewServeMux()
	modelAPI := (&goblin.API{Store: models, Examples: filepath.Join(dir, "examples.jsonl"), Remove: eng.RemoveExample}).Handler()
	mux.Handle("/models", modelAPI)
	mux.Handle("/models/", modelAPI)
	mux.Handle("/", sentry.Handler(eng))
	go func() {
		if err := p.ServeAPI(ctx, mux); err != nil {
			logger.Error("sentry: api", "error", err)
		}
	}()

	refreshNetwork := func() {
		if !p.Self.Has(pluginsdk.CapConfigRead) {
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
