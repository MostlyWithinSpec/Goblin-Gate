package config

// Profiles groups the security profile definitions. Each profile is defined
// once and referenced by name from policies; the concrete settings are filled
// in by their feature tasks (012-015).
type Profiles struct {
	IPS       []IPSProfile       `json:"ips,omitempty"`
	WebFilter []WebFilterProfile `json:"webfilter,omitempty"`
	DNSFilter []DNSFilterProfile `json:"dnsfilter,omitempty"`
	AppCtrl   []AppCtrlProfile   `json:"appctrl,omitempty"`
}

// IPSProfile is a Suricata inline-IPS profile (task 031). Each profile in
// use runs its own Suricata instance on its own NFQUEUE range, with a rule
// set derived from the shared ET Open download.
type IPSProfile struct {
	Name    string `json:"name"`
	Comment string `json:"comment,omitempty"`
	// Mode: prevent (default) drops traffic matching rules at or above
	// BlockSeverity; detect only alerts.
	Mode string `json:"mode,omitempty"`
	// BlockSeverity is the lowest Suricata severity that is dropped in
	// prevent mode: 1 = high only, 2 = high and medium (default), 3 = all.
	BlockSeverity int `json:"block_severity,omitempty"`
	// Categories limits the rules to these ET Open categories (the word
	// after "ET" in a rule's msg, e.g. MALWARE, SCAN, EXPLOIT); empty = the
	// default set (all but POLICY, INFO, GAMES, CHAT and HUNTING).
	Categories []string `json:"categories,omitempty"`
	// Suppress silences rules (false positives), for everyone or one address.
	Suppress []IPSSuppress `json:"suppress,omitempty"`
}

// IPSSuppress turns one rule off, optionally only for one source or
// destination address.
type IPSSuppress struct {
	SID   uint32 `json:"sid"`
	Track string `json:"track,omitempty"` // "" (everywhere) | by_src | by_dst
	IP    string `json:"ip,omitempty"`    // required with track
}

// WebFilterProfile filters web access by category (task 033). It is
// enforced at DNS: clients of the interfaces whose policies use the profile
// (first match per source interface, like DNS filter profiles) resolve
// through goblind's web filter, which answers blocked and warned names with
// the block page. Category names are those of the category database (UT1
// lists: adult, gambling, malware, phishing, social_networks, ...).
type WebFilterProfile struct {
	Name    string `json:"name"`
	Comment string `json:"comment,omitempty"`
	// Categories maps a category to allow (default), monitor (log only),
	// warn (block page with "continue") or block.
	Categories map[string]string `json:"categories,omitempty"`
	// BlockDomains and AllowDomains override the categories (a name covers
	// its subdomains; allow wins).
	BlockDomains []string `json:"block_domains,omitempty"`
	AllowDomains []string `json:"allow_domains,omitempty"`
}

// DNSFilterProfile filters DNS answers for the clients of the policies that
// use it (Unbound RPZ, task 029). A blocked name also blocks its subdomains;
// allow_domains wins over every block list.
type DNSFilterProfile struct {
	Name         string   `json:"name"`
	Comment      string   `json:"comment,omitempty"`
	BlockFeeds   []string `json:"block_feeds,omitempty"`   // domain feed names
	BlockDomains []string `json:"block_domains,omitempty"` // always blocked
	AllowDomains []string `json:"allow_domains,omitempty"` // never blocked
	SafeSearch   bool     `json:"safe_search,omitempty"`   // force Google/Bing/DuckDuckGo/YouTube safe search
	Action       string   `json:"action,omitempty"`        // nxdomain (default) | blockpage
}

// AppCtrlProfile controls applications (task 035), recognized by their
// domains (internal/appctrl catalog) and enforced at DNS for the clients
// of the policies that use the profile (first match per source interface,
// like web filter profiles). Actions: allow (default), monitor (log only),
// block.
type AppCtrlProfile struct {
	Name    string `json:"name"`
	Comment string `json:"comment,omitempty"`
	// Groups sets an action for every app of a catalog group (e.g.
	// "Gaming": "block"); Apps overrides it per app id (e.g. "steam").
	Groups map[string]string `json:"groups,omitempty"`
	Apps   map[string]string `json:"apps,omitempty"`
}

// Feed is a threat feed source.
type Feed struct {
	Name           string `json:"name"`
	Type           string `json:"type"` // ip | domain | url
	URL            string `json:"url"`
	Format         string `json:"format,omitempty"`          // plain (default) | csv:<column> | json:<path>
	RefreshMinutes int    `json:"refresh_minutes,omitempty"` // 0 = 60; at least 15
	Enabled        bool   `json:"enabled"`
	// BlockWAN drops the feed's addresses on every WAN interface (ip feeds),
	// in addition to policies that list the feed in threat_feeds.
	BlockWAN bool `json:"block_wan,omitempty"`
	// AuthHeader is sent with each download, e.g. "Authorization: Bearer x".
	// It is stored in the config like any other field.
	AuthHeader string `json:"auth_header,omitempty"`
}

// Admin is a management-plane user. The default admin ships without a
// password hash; the first-boot wizard (task 022) sets one.
type Admin struct {
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash,omitempty"` // argon2id, set by task 007/022
	Role         string `json:"role"`                    // admin | readonly
	// TOTPSecret turns on two-factor login: base32 (160 bits), set through
	// POST /api/v1/auth/2fa/enable, a secret like PasswordHash.
	TOTPSecret string `json:"totp_secret,omitempty"`
}
