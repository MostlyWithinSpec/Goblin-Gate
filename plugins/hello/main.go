// Command hello is the example plugin: the smallest useful thing a plugin can
// do, and the folder to copy when writing your own (docs/dev/plugins.md,
// "Write your own"). It is built only on pkg/pluginsdk, is not built by a
// release and is not published.
//
// It reads one setting (greeting) and one secret (token), asks for stats:read,
// and every minute logs the greeting with the box's uptime and session count.
package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/pkg/pluginsdk"
)

// everyMinute is how often the load is logged.
const everyMinute = time.Minute

// What the plugin logs when stats:read is missing: once at start, and once
// more when the admin takes it away while the plugin runs.
const (
	deniedAtStart      = "stats:read is not approved, there is nothing to log"
	deniedWhileRunning = "stats:read is not approved any more"
)

func main() {
	pluginsdk.Run(run)
}

func run(ctx context.Context, p *pluginsdk.Plugin) error {
	ticker := time.NewTicker(everyMinute)
	defer ticker.Stop()
	return loop(ctx, p.Logger, p.Self, p.Client, ticker.C, nil)
}

// loop is the plugin's whole behaviour, split out from run so a test can drive
// it with a fake goblind and its own ticks. It returns when ctx ends. started
// (when not nil) gets a signal right after the first log line, so a test knows
// the plugin has said something before it reads the log.
func loop(ctx context.Context, log *slog.Logger, self pluginsdk.Self, load loader, tick <-chan time.Time, started chan<- struct{}) error {
	greeting := self.String("greeting", "hello")
	// A secret is never logged, only whether the admin filled it in.
	tokenSet := self.String("token", "") != ""
	if !self.Has(pluginsdk.CapStatsRead) {
		log.Warn(deniedAtStart, "greeting", greeting, "token_set", tokenSet)
		signal(started)
		// Stay up: goblind restarts the plugin when the admin approves the
		// capability, so there is nothing to do but wait to be restarted.
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-tick:
			}
		}
	}
	log.Info("hello: started", "greeting", greeting, "token_set", tokenSet,
		"interval_seconds", int(everyMinute.Seconds()))
	signal(started)
	denied := false
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick:
			sys, err := load.System(ctx)
			if pluginsdk.Denied(err) {
				// The admin took the capability away while the plugin ran:
				// log it once and stay up, goblind restarts the plugin when
				// the settings or capabilities change.
				if !denied {
					log.Warn(deniedWhileRunning, "greeting", greeting, "token_set", tokenSet)
					denied = true
				}
				continue
			}
			if err != nil {
				log.Warn("reading the load failed", "error", err)
				continue
			}
			log.Info(greeting, "uptime_seconds", sys.UptimeSeconds, "sessions", sys.Sessions)
		}
	}
}

// loader is the one SDK call loop makes, so a test can answer it directly.
// *pluginsdk.Client satisfies it.
type loader interface {
	System(ctx context.Context) (pluginsdk.System, error)
}

// signal sends once to a nil-safe start barrier.
func signal(ch chan<- struct{}) {
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}
