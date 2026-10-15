package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/commit"
	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/nft"
)

// watchScheduleOffset re-applies the running config when the UTC offset of
// system.timezone changes (daylight saving time): schedules are rendered in
// UTC with the offset at render time (task 057). Only boxes with schedules
// re-apply; the check runs every minute.
func watchScheduleOffset(ctx context.Context, store *config.Store, engine *commit.Engine, logger *slog.Logger) {
	offset := func() (int, bool) {
		cfg, err := store.Running()
		if err != nil || len(cfg.Schedules) == 0 {
			return 0, false
		}
		return nft.ScheduleOffset(cfg, time.Now()), true
	}
	prev, _ := offset()
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		cur, ok := offset()
		if ok && cur != prev {
			logger.Info("schedules: time zone offset changed, re-applying", "from", prev, "to", cur)
			if _, err := engine.ApplyRunning(ctx); err != nil {
				logger.Warn("schedules: re-apply failed", "error", err)
				continue // try again next minute
			}
		}
		prev = cur
	}
}
