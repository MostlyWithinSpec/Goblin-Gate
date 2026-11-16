package pluginsdk

import (
	"context"
	"net/netip"
	"net/url"
	"strconv"
	"time"
)

// Capabilities a plugin may ask for in its manifest.
const (
	CapEventsRead     = "events:read"
	CapStatsRead      = "stats:read"
	CapConfigRead     = "config:read"
	CapNotifySend     = "notify:send"
	CapBlocksRead     = "blocks:read"
	CapBlocksWrite    = "blocks:write"
	CapDevicesRead    = "devices:read"
	CapPoliciesToggle = "policies:toggle"
	CapObjectsWrite   = "objects:write"
)

// --- plugin/self (every plugin)

// Self is the plugin as goblind runs it: GET /api/v1/plugin/self.
type Self struct {
	Name         string         `json:"name"`
	Capabilities []string       `json:"capabilities"` // approved by the admin
	Settings     map[string]any `json:"settings"`     // schema defaults filled in, secrets included
}

// Self returns the plugin's name, approved capabilities and settings.
func (c *Client) Self(ctx context.Context) (Self, error) {
	var out Self
	err := c.Do(ctx, "GET", "/api/v1/plugin/self", nil, &out)
	return out, err
}

// Has reports whether the admin approved capability c.
func (s Self) Has(c string) bool {
	for _, x := range s.Capabilities {
		if x == c {
			return true
		}
	}
	return false
}

// String returns a string setting, def when it is unset or not a string.
func (s Self) String(key, def string) string {
	if v, ok := s.Settings[key].(string); ok {
		return v
	}
	return def
}

// Int returns an integer setting, def when it is unset or not a number.
func (s Self) Int(key string, def int) int {
	if v, ok := s.Settings[key].(float64); ok {
		return int(v)
	}
	return def
}

// Float returns a number setting, def when it is unset or not a number.
func (s Self) Float(key string, def float64) float64 {
	if v, ok := s.Settings[key].(float64); ok {
		return v
	}
	return def
}

// Bool returns a boolean setting, def when it is unset or not a boolean.
func (s Self) Bool(key string, def bool) bool {
	if v, ok := s.Settings[key].(bool); ok {
		return v
	}
	return def
}

// Strings returns an array-of-strings setting (nil when unset).
func (s Self) Strings(key string) []string {
	list, ok := s.Settings[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		if str, ok := v.(string); ok {
			out = append(out, str)
		}
	}
	return out
}

// --- events:read

// Event is one stored event: GET /api/v1/logs.
type Event struct {
	ID        int64  `json:"id"`
	Time      int64  `json:"time"` // unix seconds
	Kind      string `json:"kind"` // traffic, ips, dns, webfilter, system
	Action    string `json:"action"`
	PolicyID  string `json:"policy_id,omitempty"`
	SrcIP     string `json:"src_ip,omitempty"`
	SrcPort   int    `json:"src_port,omitempty"`
	DstIP     string `json:"dst_ip,omitempty"`
	DstPort   int    `json:"dst_port,omitempty"`
	Proto     string `json:"proto,omitempty"`
	InIntf    string `json:"in_intf,omitempty"`
	OutIntf   string `json:"out_intf,omitempty"`
	Bytes     int    `json:"bytes,omitempty"`
	App       string `json:"app,omitempty"`
	Category  string `json:"category,omitempty"`
	Signature string `json:"signature,omitempty"` // IPS: "<sid>: <text>" when Suricata gave a sid
	Severity  string `json:"severity,omitempty"`
	Message   string `json:"message,omitempty"`
}

// LogQuery selects events. The zero value is the newest page of every kind.
type LogQuery struct {
	Kind   string // traffic, ips, dns, system, ...; "" = all
	Text   string // text search
	From   string // RFC 3339, unix time or a relative "-15m"
	To     string
	After  int64 // only events newer than this id, oldest first
	Cursor int64 // the NextCursor of the previous page
	Limit  int   // 0 = goblind's default
}

// LogPage is one page of events. NextCursor is 0 on the last page.
type LogPage struct {
	Events     []Event `json:"events"`
	NextCursor int64   `json:"next_cursor"`
}

// Logs reads stored events (events:read). Without After they come newest
// first; with After, oldest first.
func (c *Client) Logs(ctx context.Context, q LogQuery) (LogPage, error) {
	v := url.Values{}
	set := func(k, s string) {
		if s != "" {
			v.Set(k, s)
		}
	}
	set("kind", q.Kind)
	set("q", q.Text)
	set("from", q.From)
	set("to", q.To)
	if q.After > 0 {
		v.Set("after", strconv.FormatInt(q.After, 10))
	}
	if q.Cursor > 0 {
		v.Set("cursor", strconv.FormatInt(q.Cursor, 10))
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	path := "/api/v1/logs"
	if len(v) > 0 {
		path += "?" + v.Encode()
	}
	var out LogPage
	err := c.Do(ctx, "GET", path, nil, &out)
	return out, err
}

// --- stats:read

// System is the box's load: GET /api/v1/stats/system.
type System struct {
	CPUPercent    float64 `json:"cpu_percent"`
	MemTotalBytes uint64  `json:"mem_total_bytes"`
	MemUsedBytes  uint64  `json:"mem_used_bytes"`
	UptimeSeconds int64   `json:"uptime_seconds"`
	Sessions      int64   `json:"sessions"` // tracked connections
}

// System reads CPU, memory, uptime and sessions (stats:read).
func (c *Client) System(ctx context.Context) (System, error) {
	var out System
	err := c.Do(ctx, "GET", "/api/v1/stats/system", nil, &out)
	return out, err
}

// WAN is one WAN link: GET /api/v1/wan/status.
type WAN struct {
	Name      string   `json:"name"`
	Device    string   `json:"device"`
	Up        bool     `json:"up"`
	Addresses []string `json:"addresses"` // CIDRs
	Gateway   string   `json:"gateway,omitempty"`
}

// WANs reads the WAN links (stats:read).
func (c *Client) WANs(ctx context.Context) ([]WAN, error) {
	var out struct {
		WANs []WAN `json:"wans"`
	}
	err := c.Do(ctx, "GET", "/api/v1/wan/status", nil, &out)
	return out.WANs, err
}

// Gateway is one monitored gateway.
type Gateway struct {
	Name        string    `json:"name"`
	Interface   string    `json:"interface"`
	Device      string    `json:"device"`
	MonitorIP   string    `json:"monitor_ip,omitempty"`
	State       string    `json:"state"` // up, down, unknown (not measured yet)
	LossPercent int       `json:"loss_percent"`
	LatencyMs   int       `json:"latency_ms"`
	Since       time.Time `json:"since,omitzero"`
	Reason      string    `json:"reason,omitempty"`
}

// GatewayReport is GET /api/v1/gateways/status.
type GatewayReport struct {
	Enabled  bool      `json:"enabled"` // gateway monitoring configured
	Gateways []Gateway `json:"gateways"`
}

// Gateways reads gateway monitoring (stats:read).
func (c *Client) Gateways(ctx context.Context) (GatewayReport, error) {
	var out GatewayReport
	err := c.Do(ctx, "GET", "/api/v1/gateways/status", nil, &out)
	return out, err
}

// PolicyStats reads hits per policy, FeedStats hits per threat feed and
// StatsQuery runs an event statistics query (all stats:read). Their shapes are
// in the API reference (/api/v1/openapi.json); out is decoded as given.
func (c *Client) PolicyStats(ctx context.Context, out any) error {
	return c.Do(ctx, "GET", "/api/v1/stats/policies", nil, out)
}

// FeedStats: see PolicyStats.
func (c *Client) FeedStats(ctx context.Context, out any) error {
	return c.Do(ctx, "GET", "/api/v1/stats/feeds", nil, out)
}

// StatsQuery: see PolicyStats.
func (c *Client) StatsQuery(ctx context.Context, query, out any) error {
	return c.Do(ctx, "POST", "/api/v1/stats/query", query, out)
}

// --- config:read

// RunningConfig decodes the running config (secrets redacted) into out
// (config:read). Declare only the parts you read; goblind may add sections.
func (c *Client) RunningConfig(ctx context.Context, out any) error {
	return c.Do(ctx, "GET", "/api/v1/config/running", nil, out)
}

// --- notify:send

// Notification goes out through the admin's notification rules.
type Notification struct {
	Title   string `json:"title"`   // up to 200 characters
	Message string `json:"message"` // up to 4000
	// Key groups repeats: notifications with the same key are rate-limited
	// together (for example one key per incident).
	Key string `json:"key,omitempty"`
}

// Notify sends a notification (notify:send). Over-long text is cut.
func (c *Client) Notify(ctx context.Context, n Notification) error {
	n.Title = cut(n.Title, 200)
	n.Message = cut(n.Message, 4000)
	return c.Do(ctx, "POST", "/api/v1/plugin/notify", n, nil)
}

func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// do not split a UTF-8 sequence
	for n > 0 && s[n]&0xc0 == 0x80 {
		n--
	}
	return s[:n]
}

// --- blocks:read, blocks:write

// Block is one temporary block.
type Block struct {
	IP        netip.Addr `json:"ip"`
	Until     time.Time  `json:"until"`
	Reason    string     `json:"reason"`
	Source    string     `json:"source"`     // admin or plugin:<name>
	CreatedBy string     `json:"created_by"` // admin user or plugin name
	Created   time.Time  `json:"created"`
	ExpiresIn int        `json:"expires_in"` // seconds
}

// Blocks lists the active temporary blocks (blocks:read).
func (c *Client) Blocks(ctx context.Context) ([]Block, error) {
	var out struct {
		Blocks []Block `json:"blocks"`
	}
	err := c.Do(ctx, "GET", "/api/v1/blocks", nil, &out)
	return out.Blocks, err
}

// AddBlock drops all traffic from and to ip for minutes (1-10080)
// (blocks:write). goblind refuses the box's own networks and services, and
// more than 30 blocks per hour.
func (c *Client) AddBlock(ctx context.Context, ip string, minutes int, reason string) (Block, error) {
	var out Block
	err := c.Do(ctx, "POST", "/api/v1/blocks", map[string]any{"ip": ip, "minutes": minutes, "reason": reason}, &out)
	return out, err
}

// RemoveBlock lifts one of this plugin's blocks (blocks:write).
func (c *Client) RemoveBlock(ctx context.Context, ip string) error {
	return c.Do(ctx, "DELETE", "/api/v1/blocks/"+url.PathEscape(ip), nil, nil)
}

// --- devices:read

// Device is one entry of the device inventory: GET /api/v1/devices.
type Device struct {
	MAC        string    `json:"mac"`
	IPs        []string  `json:"ips"` // newest first, IPv4 and IPv6
	Hostname   string    `json:"hostname,omitempty"`
	Vendor     string    `json:"vendor,omitempty"`
	Interface  string    `json:"interface,omitempty"`
	FirstSeen  time.Time `json:"first_seen"`
	LastSeen   time.Time `json:"last_seen"`
	Name       string    `json:"name,omitempty"` // the admin's label
	Randomized bool      `json:"randomized,omitempty"`
}

// Devices reads the device inventory (devices:read).
func (c *Client) Devices(ctx context.Context) ([]Device, error) {
	var out struct {
		Devices []Device `json:"devices"`
	}
	err := c.Do(ctx, "GET", "/api/v1/devices", nil, &out)
	return out.Devices, err
}

// --- policies:toggle

// Policy is one policy the plugin may switch.
type Policy struct {
	ID      uint32 `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// Policies lists the policies the admin lets this plugin switch, as running
// now (policies:toggle).
func (c *Client) Policies(ctx context.Context) ([]Policy, error) {
	var out struct {
		Policies []Policy `json:"policies"`
	}
	err := c.Do(ctx, "GET", "/api/v1/plugin/policies", nil, &out)
	return out.Policies, err
}

// SwitchPolicy turns one of them on or off and returns its state afterwards
// (policies:toggle). goblind commits the switch as a revision of its own;
// it answers 409 while a commit waits for confirmation and 429 after 60
// switches in an hour.
func (c *Client) SwitchPolicy(ctx context.Context, id uint32, enabled bool) (Policy, error) {
	var out Policy
	err := c.Do(ctx, "PUT", "/api/v1/plugin/policies/"+strconv.FormatUint(uint64(id), 10), map[string]bool{"enabled": enabled}, &out)
	return out, err
}

// --- objects:write

// LiveObject is one live address object the admin gave this plugin: an
// address object of type "live" whose members the plugin keeps.
type LiveObject struct {
	Name    string         `json:"name"`
	Plugin  string         `json:"plugin"`
	Members []netip.Prefix `json:"members"`          // hosts as /32 and /128
	Updated time.Time      `json:"updated,omitzero"` // zero until first set
}

// LiveChange is what SetLiveObject did.
type LiveChange struct {
	Name    string `json:"name"`
	Members int    `json:"members"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	Changed bool   `json:"changed"` // false when the members were the same
}

// LiveObjects lists this plugin's live objects with their current members
// (objects:write).
func (c *Client) LiveObjects(ctx context.Context) ([]LiveObject, error) {
	var out struct {
		Objects []LiveObject `json:"objects"`
	}
	err := c.Do(ctx, "GET", "/api/v1/plugin/objects", nil, &out)
	return out.Objects, err
}

// SetLiveObject replaces the members of one of this plugin's live objects
// (objects:write): addresses or prefixes no wider than /16 (IPv4) or /48
// (IPv6), at most 4096; an empty list clears it. goblind loads them into the
// firewall at once, without a commit, and answers 403 for an object that is
// not this plugin's and 429 after 360 updates in an hour. Sending the same
// members again is cheap: nothing is reloaded.
func (c *Client) SetLiveObject(ctx context.Context, name string, members []string) (LiveChange, error) {
	if members == nil {
		members = []string{}
	}
	var out LiveChange
	err := c.Do(ctx, "PUT", "/api/v1/plugin/objects/"+url.PathEscape(name), map[string][]string{"members": members}, &out)
	return out, err
}
