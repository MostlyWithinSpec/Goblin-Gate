package nft

import (
	"strings"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

func withNow(t *testing.T, s string) {
	t.Helper()
	at, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	old := Now
	Now = func() time.Time { return at }
	t.Cleanup(func() { Now = old })
}

func schedCfg(tz string, ranges ...config.ScheduleRange) *config.Config {
	cfg := config.Default()
	cfg.System.Timezone = tz
	cfg.Schedules = []config.Schedule{{Name: "s", Ranges: ranges}}
	return cfg
}

func TestScheduleMatches(t *testing.T) {
	r := func(start, end string, days ...string) config.ScheduleRange {
		return config.ScheduleRange{Days: days, Start: start, End: end}
	}
	tests := []struct {
		name, tz, now string
		ranges        []config.ScheduleRange
		want          []string
	}{
		{"utc evening", "UTC", "2026-07-01T12:00:00Z", []config.ScheduleRange{r("21:00", "23:00", "mon", "tue")},
			[]string{`meta day { "Monday", "Tuesday" } meta hour "21:00"-"22:59:59"`}},
		{"whole day needs no hour", "UTC", "2026-07-01T12:00:00Z", []config.ScheduleRange{r("00:00", "24:00", "sat")},
			[]string{`meta day "Saturday"`}},
		// Berlin in summer is UTC+2: 21:00-06:30 local Sunday night = Sunday
		// 19:00-24:00 UTC and Monday 00:00-04:30 UTC
		{"overnight in Berlin summer", "Europe/Berlin", "2026-07-01T12:00:00Z", []config.ScheduleRange{r("21:00", "06:30", "sun")},
			[]string{`meta day "Monday" meta hour "00:00"-"04:29:59"`, `meta day "Sunday" meta hour "19:00"-"23:59:59"`}},
		// winter: UTC+1
		{"Berlin winter", "Europe/Berlin", "2026-01-15T12:00:00Z", []config.ScheduleRange{r("08:00", "17:00", "mon")},
			[]string{`meta day "Monday" meta hour "07:00"-"15:59:59"`}},
		// Chicago (UTC-5 in summer): Saturday 22:00 local = Sunday 03:00 UTC; the
		// week wraps from Saturday to Sunday
		{"Chicago Saturday night wraps the week", "America/Chicago", "2026-07-01T12:00:00Z", []config.ScheduleRange{r("22:00", "24:00", "sat")},
			[]string{`meta day "Sunday" meta hour "03:00"-"04:59:59"`}},
		{"half-hour zone", "Asia/Kolkata", "2026-07-01T12:00:00Z", []config.ScheduleRange{r("09:00", "10:00", "wed")},
			[]string{`meta day "Wednesday" meta hour "03:30"-"04:29:59"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withNow(t, tt.now)
			cfg := schedCfg(tt.tz, tt.ranges...)
			if errs := config.Validate(cfg); len(errs) > 0 {
				t.Fatalf("fixture invalid: %v", errs)
			}
			got, err := scheduleMatches(cfg, "s")
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}
		})
	}
}

// TestScheduledPolicy: every rule of a scheduled policy carries the window;
// the offset helper sees DST.
func TestScheduledPolicy(t *testing.T) {
	withNow(t, "2026-07-01T12:00:00Z")
	cfg := schedCfg("UTC", config.ScheduleRange{Days: []string{"mon"}, Start: "21:00", End: "23:00"})
	cfg.Policies[0].Schedule = "s"
	cfg.Policies[0].Action = "deny"
	files, err := Renderer{}.Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := `iifname "eth1" oifname "eth0" meta day "Monday" meta hour "21:00"-"22:59:59" counter name "policy_1"`
	if !strings.Contains(string(files[Path]), want) {
		t.Errorf("missing %q in\n%s", want, files[Path])
	}
	berlin := schedCfg("Europe/Berlin")
	summer, _ := time.Parse(time.RFC3339, "2026-07-01T12:00:00Z")
	winter, _ := time.Parse(time.RFC3339, "2026-01-01T12:00:00Z")
	if ScheduleOffset(berlin, summer) != 7200 || ScheduleOffset(berlin, winter) != 3600 {
		t.Error("ScheduleOffset does not follow daylight saving time")
	}
}
