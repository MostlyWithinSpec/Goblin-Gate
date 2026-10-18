package config

// DDNS is one dynamic DNS entry (task 087). The box pushes its public IP to a
// provider so the hostname always resolves. At most 16 entries.
type DDNS struct {
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	Provider  string `json:"provider"` // cloudflare | duckdns | custom
	Hostname  string `json:"hostname"` // FQDN to update, e.g. host.example.net
	Interface string `json:"interface,omitempty"`
	// Token is the provider API token (a secret; redacted in API responses).
	Token string `json:"token,omitempty"`
	// ZoneID is the Cloudflare zone identifier (required for cloudflare).
	ZoneID string `json:"zone_id,omitempty"`
	// URL is the custom update endpoint (required for custom); it must start
	// with https:// and contain {ip} (and may contain {hostname}).
	URL string `json:"url,omitempty"`
}
