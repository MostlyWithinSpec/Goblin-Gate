package config

import (
	"bytes"
	"encoding/json"
	"net/url"
)

// RedactSecrets blanks the secrets in cfg: what a plugin or a read-only user
// must not see, and what never leaves the box in the git history. Password
// hashes, two-factor secrets, notification credentials (URLs can carry
// webhook secrets), feed download headers (API keys of paid feeds), DDNS
// tokens and update URLs, log export credentials, and credentials or
// tokens inside the mirror URLs. WireGuard private keys are not in the
// config (they live in their own key directory).
func RedactSecrets(cfg *Config) {
	for i := range cfg.Admins {
		cfg.Admins[i].PasswordHash = ""
		cfg.Admins[i].TOTPSecret = ""
	}
	for i := range cfg.Feeds {
		cfg.Feeds[i].AuthHeader = ""
	}
	for i := range cfg.Notifications.Channels {
		c := &cfg.Notifications.Channels[i]
		c.URL, c.Token, c.BotToken, c.Password = "", "", "", ""
	}
	for i := range cfg.DDNS {
		cfg.DDNS[i].Token = ""
		cfg.DDNS[i].URL = ""
	}
	for i := range cfg.LogExport {
		cfg.LogExport[i].Password = ""
		cfg.LogExport[i].Token = ""
	}
	cfg.System.UpdateURL = redactURL(cfg.System.UpdateURL)
	cfg.GeoIP.SourceURL = redactURL(cfg.GeoIP.SourceURL)
	cfg.CrowdSec.APIKey = ""
	cfg.UPS.Password = ""
	cfg.IPS.ETProCode = ""
}

// redactURL drops a URL's password and query (a mirror's token).
func redactURL(s string) string {
	if s == "" {
		return s
	}
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	if u.User == nil && u.RawQuery == "" {
		return s
	}
	if u.User != nil {
		u.User = url.User(u.User.Username())
	}
	u.RawQuery = ""
	return u.String()
}

// KeepRedactedSecrets puts back the secrets a caller who only sees redacted
// configs (RedactSecrets) left blank when it wrote next. An entry keeps its
// stored secrets only when, apart from them and the enabled switch, it is
// exactly what the caller read: whoever changes where a secret goes (a URL,
// a host, a provider) must type the secret again, so a limited token cannot
// redirect one to itself. Admin entries are not touched (such callers cannot
// write them).
func KeepRedactedSecrets(prev, next *Config) {
	red := prev.Clone()
	if red == nil {
		return // keep nothing rather than guess
	}
	RedactSecrets(red)
	keepList(prev.Feeds, red.Feeds, next.Feeds, func(f *Feed) string { return f.Name }, func(f *Feed) *bool { return &f.Enabled })
	keepList(prev.Notifications.Channels, red.Notifications.Channels, next.Notifications.Channels,
		func(c *NotifyChannel) string { return c.Name }, func(c *NotifyChannel) *bool { return &c.Enabled })
	keepList(prev.DDNS, red.DDNS, next.DDNS, func(d *DDNS) string { return d.Name }, func(d *DDNS) *bool { return &d.Enabled })
	keepList(prev.LogExport, red.LogExport, next.LogExport, func(e *LogExport) string { return e.Name }, func(e *LogExport) *bool { return &e.Enabled })
	if next.System.UpdateURL == red.System.UpdateURL {
		next.System.UpdateURL = prev.System.UpdateURL
	}
	if next.GeoIP.SourceURL == red.GeoIP.SourceURL {
		next.GeoIP.SourceURL = prev.GeoIP.SourceURL
	}
	n := next.CrowdSec
	n.Enabled = red.CrowdSec.Enabled
	if n == red.CrowdSec {
		next.CrowdSec.APIKey = prev.CrowdSec.APIKey
	}
	// the code goes to one fixed place (Proofpoint): keep it when left blank
	if next.IPS.ETProCode == "" {
		next.IPS.ETProCode = prev.IPS.ETProCode
	}
	u := next.UPS
	u.Enabled = red.UPS.Enabled
	if u == red.UPS {
		next.UPS.Password = prev.UPS.Password
	}
}

// keepList restores next[i] to its stored entry (prev, with the next entry's
// enabled switch) when it equals the redacted stored entry (red) apart from
// that switch.
func keepList[T any](prev, red, next []T, name func(*T) string, enabled func(*T) *bool) {
	stored := map[string]int{}
	for i := range prev {
		stored[name(&prev[i])] = i
	}
	for i := range next {
		j, ok := stored[name(&next[i])]
		if !ok {
			continue
		}
		r := red[j]
		*enabled(&r) = *enabled(&next[i])
		if sameJSON(r, next[i]) {
			e := *enabled(&next[i])
			next[i] = prev[j]
			*enabled(&next[i]) = e
		}
	}
}

func sameJSON(a, b any) bool {
	x, err1 := json.Marshal(a)
	y, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && bytes.Equal(x, y)
}

// Clone returns a deep copy of c (nil if c does not encode, which a valid
// config always does). Runtime-only fields (json "-") are not copied.
func (c *Config) Clone() *Config {
	b, err := json.Marshal(c)
	if err != nil {
		return nil
	}
	var out Config
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	return &out
}
