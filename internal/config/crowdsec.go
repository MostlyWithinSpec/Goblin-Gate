package config

import (
	"net/url"
	"strings"
)

// CrowdSec connects the box to a CrowdSec Local API as a bouncer (task
// 104): the addresses CrowdSec decided to ban are dropped.
type CrowdSec struct {
	Enabled bool `json:"enabled"`
	// URL is the Local API base, e.g. http://192.168.1.20:8080.
	URL string `json:"url,omitempty"`
	// APIKey is the bouncer key from `cscli bouncers add` (a secret;
	// redacted in API responses).
	APIKey string `json:"api_key,omitempty"`
	// PollSeconds between decision pulls; 0 = 10.
	PollSeconds int `json:"poll_seconds,omitempty"`
	// Scope: "wan" (or "") drops banned sources arriving on WAN interfaces;
	// "all" drops them on every interface and also as destinations.
	Scope string `json:"scope,omitempty"`
}

// Poll returns the poll interval in seconds.
func (c CrowdSec) Poll() int {
	if c.PollSeconds == 0 {
		return 10
	}
	return c.PollSeconds
}

func (v *validator) checkCrowdSec() {
	c := v.cfg.CrowdSec
	if c.URL != "" {
		u, err := url.Parse(c.URL)
		switch {
		case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || strings.ContainsAny(c.URL, " \t\n"):
			v.add("crowdsec.url", "want the Local API's http:// or https:// URL, e.g. http://192.168.1.20:8080")
		case u.User != nil:
			v.add("crowdsec.url", "no credentials in the URL: put the bouncer key in api_key")
		}
	}
	if c.Enabled && c.URL == "" {
		v.add("crowdsec.url", "required when CrowdSec is enabled")
	}
	if c.Enabled && c.APIKey == "" {
		v.add("crowdsec.api_key", "required when CrowdSec is enabled (cscli bouncers add goblin-gate)")
	}
	if strings.ContainsAny(c.APIKey, " \t\r\n") || len(c.APIKey) > 256 {
		v.add("crowdsec.api_key", "invalid bouncer key")
	}
	if c.PollSeconds != 0 && (c.PollSeconds < 5 || c.PollSeconds > 300) {
		v.add("crowdsec.poll_seconds", "want 5-300 seconds")
	}
	if !oneOf(c.Scope, "", "wan", "all") {
		v.add("crowdsec.scope", "unknown scope %q: want wan or all", c.Scope)
	}
}
