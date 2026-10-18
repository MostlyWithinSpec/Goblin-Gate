package config

// LogExport is one remote log destination: syslog or Loki. Events are
// filtered by kind (empty kinds = all) and forwarded as RFC 5424 syslog
// lines or Loki push batches.
type LogExport struct {
	Name    string   `json:"name"`
	Enabled bool     `json:"enabled"`
	Type    string   `json:"type"` // "syslog" or "loki"
	Kinds   []string `json:"kinds,omitempty"`

	// Syslog fields.
	Address  string `json:"address,omitempty"`  // host:port, e.g. "10.0.0.2:514"
	Protocol string `json:"protocol,omitempty"` // "udp", "tcp" or "tls"; default udp

	// Loki fields.
	URL      string `json:"url,omitempty"` // must end in /loki/api/v1/push
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"` // a secret; redacted in API responses
	Token    string `json:"token,omitempty"`    // a secret; bearer auth, redacted in API responses
}
