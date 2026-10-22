package config

import (
	"strings"
	"testing"
)

func TestValidateCrowdSec(t *testing.T) {
	msgs := func(c CrowdSec) string {
		cfg := Default()
		cfg.CrowdSec = c
		var out []string
		for _, e := range Validate(cfg) {
			out = append(out, e.Path+": "+e.Message)
		}
		return strings.Join(out, "\n")
	}
	if m := msgs(CrowdSec{Enabled: true, URL: "http://192.168.1.20:8080", APIKey: "abc", PollSeconds: 10, Scope: "all"}); m != "" {
		t.Fatalf("valid config refused:\n%s", m)
	}
	m := msgs(CrowdSec{Enabled: true, URL: "https://user:pw@lapi.example", PollSeconds: 2, Scope: "lan"})
	for _, want := range []string{"crowdsec.url: no credentials", "crowdsec.api_key: required", "crowdsec.poll_seconds", `crowdsec.scope: unknown scope "lan"`} {
		if !strings.Contains(m, want) {
			t.Errorf("missing %q in:\n%s", want, m)
		}
	}
	if m := msgs(CrowdSec{Enabled: true, APIKey: "abc"}); !strings.Contains(m, "crowdsec.url: required") {
		t.Errorf("missing url:\n%s", m)
	}
	cfg := Default()
	cfg.CrowdSec.APIKey = "secret"
	RedactSecrets(cfg)
	if cfg.CrowdSec.APIKey != "" {
		t.Error("bouncer key not redacted")
	}
}
