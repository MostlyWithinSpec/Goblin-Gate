package config

import "net/url"

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
