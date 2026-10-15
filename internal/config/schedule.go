package config

import (
	"fmt"
	"strconv"
	"strings"
)

// Schedule is a set of weekly time windows a policy can be limited to
// (policies[].schedule, task 057). Times are in system.timezone.
type Schedule struct {
	Name    string          `json:"name"`
	Comment string          `json:"comment,omitempty"`
	Ranges  []ScheduleRange `json:"ranges"`
}

// ScheduleRange is one weekly window: on each of Days from Start to End
// ("HH:MM"). End before Start runs overnight into the next day; End "24:00"
// means midnight; Start == End is not allowed (use 00:00-24:00 for a day).
type ScheduleRange struct {
	Days  []string `json:"days"` // mon tue wed thu fri sat sun
	Start string   `json:"start"`
	End   string   `json:"end"`
}

// ScheduleDays maps the day names of a range to time.Weekday numbers
// (Sunday = 0).
var ScheduleDays = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}

// ParseClock parses "HH:MM" into minutes after midnight. allow24 accepts
// "24:00" (an end time).
func ParseClock(s string, allow24 bool) (int, error) {
	h, m, ok := strings.Cut(s, ":")
	if !ok || len(h) != 2 || len(m) != 2 {
		return 0, fmt.Errorf("want HH:MM, got %q", s)
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	switch {
	case err1 != nil || err2 != nil || hh < 0 || mm < 0 || mm > 59:
		return 0, fmt.Errorf("want HH:MM, got %q", s)
	case hh == 24 && mm == 0 && allow24:
		return 1440, nil
	case hh > 23:
		return 0, fmt.Errorf("hour out of range in %q", s)
	}
	return hh*60 + mm, nil
}

// FindSchedule returns the schedule with that name.
func (c *Config) FindSchedule(name string) (Schedule, bool) {
	for _, s := range c.Schedules {
		if s.Name == name {
			return s, true
		}
	}
	return Schedule{}, false
}

func (v *validator) checkSchedules() {
	for i, s := range v.cfg.Schedules {
		p := fmt.Sprintf("schedules[%d]", i)
		if len(s.Ranges) == 0 {
			v.add(p+".ranges", "a schedule needs at least one time range")
		}
		for j, r := range s.Ranges {
			rp := fmt.Sprintf("%s.ranges[%d]", p, j)
			if len(r.Days) == 0 {
				v.add(rp+".days", "pick at least one day")
			}
			seen := map[string]bool{}
			for k, d := range r.Days {
				if _, ok := ScheduleDays[d]; !ok {
					v.add(fmt.Sprintf("%s.days[%d]", rp, k), "unknown day %q: want mon, tue, wed, thu, fri, sat or sun", d)
				} else if seen[d] {
					v.add(fmt.Sprintf("%s.days[%d]", rp, k), "day %q is listed twice", d)
				}
				seen[d] = true
			}
			start, err := ParseClock(r.Start, false)
			if err != nil {
				v.add(rp+".start", "%v", err)
			}
			end, err2 := ParseClock(r.End, true)
			if err2 != nil {
				v.add(rp+".end", "%v", err2)
			}
			if err == nil && err2 == nil && start == end {
				v.add(rp+".end", "start and end are equal: use 00:00 to 24:00 for a whole day")
			}
		}
	}
	for i, pol := range v.cfg.Policies {
		if pol.Schedule == "" {
			continue
		}
		if _, ok := v.cfg.FindSchedule(pol.Schedule); !ok {
			v.add(fmt.Sprintf("policies[%d].schedule", i), "unknown schedule %q", pol.Schedule)
		}
	}
}
