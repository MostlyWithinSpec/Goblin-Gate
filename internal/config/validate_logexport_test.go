package config

import (
	"strings"
	"testing"
)

// TestValidateLogExport covers the log export validation rules.
func TestValidateLogExport(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   []string // all must appear in error output; nil = config must be valid
	}{
		{
			name: "valid syslog udp",
			mutate: func(c *Config) {
				c.LogExport = append(c.LogExport, LogExport{Name: "rsyslog", Enabled: true, Type: "syslog", Address: "10.0.0.2:514", Protocol: "udp"})
			},
			want: nil,
		},
		{
			name: "valid loki",
			mutate: func(c *Config) {
				c.LogExport = append(c.LogExport, LogExport{Name: "loki", Enabled: true, Type: "loki", URL: "https://loki.example.com/loki/api/v1/push"})
			},
			want: nil,
		},
		{
			name: "too many entries",
			mutate: func(c *Config) {
				for i := 0; i < 5; i++ {
					c.LogExport = append(c.LogExport, LogExport{Name: string(rune('a' + i)), Type: "syslog", Address: "10.0.0.2:514"})
				}
			},
			want: []string{"log_export", "at most 4"},
		},
		{
			name: "duplicate names",
			mutate: func(c *Config) {
				c.LogExport = append(c.LogExport,
					LogExport{Name: "dup", Type: "syslog", Address: "10.0.0.2:514"},
					LogExport{Name: "dup", Type: "syslog", Address: "10.0.0.3:514"},
				)
			},
			want: []string{"log_export[1].name", "duplicate"},
		},
		{
			name: "bad name",
			mutate: func(c *Config) {
				c.LogExport = append(c.LogExport, LogExport{Name: "bad name!", Type: "syslog", Address: "10.0.0.2:514"})
			},
			want: []string{"log_export[0].name", "invalid name"},
		},
		{
			name: "unknown type",
			mutate: func(c *Config) {
				c.LogExport = append(c.LogExport, LogExport{Name: "x", Type: "syslog-ng"})
			},
			want: []string{"log_export[0].type", "unknown type"},
		},
		{
			name: "unknown kind",
			mutate: func(c *Config) {
				c.LogExport = append(c.LogExport, LogExport{Name: "x", Type: "syslog", Address: "10.0.0.2:514", Kinds: []string{"bogus"}})
			},
			want: []string{"log_export[0].kinds[0]", "unknown kind"},
		},
		{
			name: "syslog missing address",
			mutate: func(c *Config) {
				c.LogExport = append(c.LogExport, LogExport{Name: "x", Type: "syslog"})
			},
			want: []string{"log_export[0].address", "required"},
		},
		{
			name: "syslog bad address",
			mutate: func(c *Config) {
				c.LogExport = append(c.LogExport, LogExport{Name: "x", Type: "syslog", Address: "not-a-port"})
			},
			want: []string{"log_export[0].address", "invalid address"},
		},
		{
			name: "syslog bad protocol",
			mutate: func(c *Config) {
				c.LogExport = append(c.LogExport, LogExport{Name: "x", Type: "syslog", Address: "10.0.0.2:514", Protocol: "sctp"})
			},
			want: []string{"log_export[0].protocol", "unknown protocol"},
		},
		{
			name: "loki missing url",
			mutate: func(c *Config) {
				c.LogExport = append(c.LogExport, LogExport{Name: "x", Type: "loki"})
			},
			want: []string{"log_export[0].url", "required"},
		},
		{
			name: "loki bad url prefix",
			mutate: func(c *Config) {
				c.LogExport = append(c.LogExport, LogExport{Name: "x", Type: "loki", URL: "ftp://loki.example.com/loki/api/v1/push"})
			},
			want: []string{"log_export[0].url", "must start with"},
		},
		{
			name: "loki bad url suffix",
			mutate: func(c *Config) {
				c.LogExport = append(c.LogExport, LogExport{Name: "x", Type: "loki", URL: "https://loki.example.com/wrong/path"})
			},
			want: []string{"log_export[0].url", "must end in"},
		},
		{
			// read-only users see the url: no password in it
			name: "loki credentials in url",
			mutate: func(c *Config) {
				c.LogExport = append(c.LogExport, LogExport{Name: "x", Type: "loki", URL: "https://pusher:s3cret@loki.example.com/loki/api/v1/push"})
			},
			want: []string{"log_export[0].url", "not in the url"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			tc.mutate(cfg)
			errs := Validate(cfg)
			if tc.want == nil {
				if len(errs) != 0 {
					t.Fatalf("expected valid config, got errors: %v", errs)
				}
				return
			}
			var b strings.Builder
			for _, e := range errs {
				b.WriteString(e.Error())
				b.WriteByte('\n')
			}
			got := b.String()
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("error output missing %q\ngot: %s", w, got)
				}
			}
		})
	}
}
