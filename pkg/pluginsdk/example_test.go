package pluginsdk_test

import (
	"context"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/pkg/pluginsdk"
)

// A plugin that logs the box's uptime and sessions every minute (the example
// in docs/dev/plugins.md).
func ExampleRun() {
	pluginsdk.Run(func(ctx context.Context, p *pluginsdk.Plugin) error {
		greeting := p.Self.String("greeting", "hi")
		tick := time.NewTicker(time.Minute)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-tick.C:
				sys, err := p.Client.System(ctx)
				if pluginsdk.Denied(err) {
					p.Logger.Warn("stats:read is not approved")
					<-ctx.Done()
					return nil
				}
				if err != nil {
					p.Logger.Warn("reading the load failed", "error", err)
					continue
				}
				p.Logger.Info(greeting, "uptime_seconds", sys.UptimeSeconds, "sessions", sys.Sessions)
			}
		}
	})
}
