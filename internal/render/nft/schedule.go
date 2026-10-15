package nft

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// Schedules (task 057). The kernel evaluates `meta day` in UTC, and nft
// converts the `meta hour` given in a rule from its own local time zone
// once, when the ruleset is loaded. So the renderer converts every window
// from system.timezone to UTC itself (splitting at UTC midnight) and the
// applier loads the ruleset with TZ=UTC. The conversion uses the zone's
// offset at render time: goblind re-applies the running config when the
// offset changes (daylight saving time), see ScheduleOffset.

// Now is the render clock (tests replace it).
var Now = time.Now

var dayNames = [7]string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}

const week = 7 * 1440 // minutes

// ScheduleOffset returns the UTC offset (seconds) of system.timezone at t.
// goblind compares it over time and re-applies when it changes.
func ScheduleOffset(cfg *config.Config, t time.Time) int {
	_, off := t.In(location(cfg)).Zone()
	return off
}

func location(cfg *config.Config) *time.Location {
	if tz := cfg.System.Timezone; tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil {
			return loc
		}
	}
	return time.UTC
}

// scheduleMatches returns the time matches of a schedule: one per distinct
// UTC hour window, each with the days it applies to. An empty string means
// "always" (never returned for a valid schedule).
func scheduleMatches(cfg *config.Config, name string) ([]string, error) {
	s, ok := cfg.FindSchedule(name)
	if !ok {
		return nil, fmt.Errorf("unknown schedule %q", name)
	}
	if len(s.Ranges) == 0 {
		return nil, fmt.Errorf("schedule %q has no time ranges", name)
	}
	offMin := ScheduleOffset(cfg, Now()) / 60
	type window struct{ start, end int } // minutes after UTC midnight, end exclusive
	days := map[window]map[int]bool{}
	add := func(day, start, end int) {
		w := window{start, end}
		if days[w] == nil {
			days[w] = map[int]bool{}
		}
		days[w][day] = true
	}
	for _, r := range s.Ranges {
		start, err := config.ParseClock(r.Start, false)
		if err != nil {
			return nil, fmt.Errorf("schedule %q: %w", name, err)
		}
		end, err := config.ParseClock(r.End, true)
		if err != nil {
			return nil, fmt.Errorf("schedule %q: %w", name, err)
		}
		dur := end - start
		if dur <= 0 {
			dur += 1440 // overnight
		}
		for _, d := range r.Days {
			wd, ok := config.ScheduleDays[d]
			if !ok {
				return nil, fmt.Errorf("schedule %q: unknown day %q", name, d)
			}
			at := ((wd*1440+start-offMin)%week + week) % week
			for left := dur; left > 0; {
				m := at % 1440
				chunk := min(left, 1440-m)
				add(at/1440, m, m+chunk)
				at = (at + chunk) % week
				left -= chunk
			}
		}
	}
	windows := make([]window, 0, len(days))
	for w := range days {
		windows = append(windows, w)
	}
	sort.Slice(windows, func(i, j int) bool {
		if windows[i].start != windows[j].start {
			return windows[i].start < windows[j].start
		}
		return windows[i].end < windows[j].end
	})
	var out []string
	for _, w := range windows {
		var names []string
		for d := 0; d < 7; d++ {
			if days[w][d] {
				names = append(names, quote(dayNames[d]))
			}
		}
		m := "meta day " + names[0]
		if len(names) > 1 {
			m = "meta day { " + strings.Join(names, ", ") + " }"
		}
		if w.start != 0 || w.end != 1440 {
			// nft hour ranges are inclusive: end one second before the window ends
			m += fmt.Sprintf(` meta hour "%02d:%02d"-"%02d:%02d:%02d"`, w.start/60, w.start%60, (w.end*60-1)/3600, (w.end*60-1)/60%60, (w.end*60-1)%60)
		}
		out = append(out, m)
	}
	return out, nil
}
