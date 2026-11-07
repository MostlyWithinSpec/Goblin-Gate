package api

import (
	"github.com/networkgoblinlabs/goblin-gate/internal/appctrl"
	"github.com/networkgoblinlabs/goblin-gate/internal/auth"
	"github.com/networkgoblinlabs/goblin-gate/internal/capture"
	"github.com/networkgoblinlabs/goblin-gate/internal/checks"
	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/crowdsec"
	"github.com/networkgoblinlabs/goblin-gate/internal/ddns"
	"github.com/networkgoblinlabs/goblin-gate/internal/devices"
	"github.com/networkgoblinlabs/goblin-gate/internal/diag"
	"github.com/networkgoblinlabs/goblin-gate/internal/feeds"
	"github.com/networkgoblinlabs/goblin-gate/internal/flows"
	"github.com/networkgoblinlabs/goblin-gate/internal/gateways"
	"github.com/networkgoblinlabs/goblin-gate/internal/geoip"
	"github.com/networkgoblinlabs/goblin-gate/internal/gitbackup"
	"github.com/networkgoblinlabs/goblin-gate/internal/importer/pfsense"
	"github.com/networkgoblinlabs/goblin-gate/internal/ips"
	"github.com/networkgoblinlabs/goblin-gate/internal/logexport"
	"github.com/networkgoblinlabs/goblin-gate/internal/logs"
	"github.com/networkgoblinlabs/goblin-gate/internal/plugins"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/nft"
	"github.com/networkgoblinlabs/goblin-gate/internal/reverseproxy"
	"github.com/networkgoblinlabs/goblin-gate/internal/stats"
	"github.com/networkgoblinlabs/goblin-gate/internal/templates"
	"github.com/networkgoblinlabs/goblin-gate/internal/throughput"
	"github.com/networkgoblinlabs/goblin-gate/internal/updates"
	"github.com/networkgoblinlabs/goblin-gate/internal/upnp"
	"github.com/networkgoblinlabs/goblin-gate/internal/ups"
	"github.com/networkgoblinlabs/goblin-gate/internal/webfilter"
)

// shared response shapes
var (
	candidateSaved = typ[candidateResponse]()
	changeList     = obj{{"count", integer}, {"changes", typ[[]config.Change]()}}
)

// routeDocs documents every route of register and openRoutes (see
// openapi.go). Keep the summaries short; Desc holds what a client must know.
var routeDocs = map[string]routeDoc{
	// --- open routes
	"POST /api/v1/auth/login": {Tag: "Auth", Open: true, Summary: "Log in",
		Desc: "Sets the session and CSRF cookies. Answers 401 with code totp_required when the user has two-factor login on and no code was sent.",
		Body: typ[loginRequest](), Resp: obj{{"user", str}, {"role", str}, {"csrf_token", str}}},
	"GET /api/v1/setup/state": {Tag: "Setup", Open: true, Summary: "Whether the first-boot setup is still needed",
		Resp: obj{{"needed", boolean}, {"current", ret(currentSetup)}}},
	"POST /api/v1/setup": {Tag: "Setup", Open: true, Summary: "Run the first-boot setup",
		Desc: "Only while no admin exists: creates the first admin and the base configuration.",
		Body: typ[SetupRequest](), Resp: obj{{"revision", integer}}},
	"GET /api/v1/system/devices": {Tag: "Setup", Open: true, Summary: "Network devices of the box",
		Desc: "Open during the first-boot setup, authenticated afterwards.", Resp: obj{{"devices", typ[[]Device]()}}},
	"GET /api/v1/openapi.json": {Tag: "Meta", Open: true, Summary: "This OpenAPI document", Resp: anyObject{}},
	"GET /metrics": {Tag: "Meta", Open: true, Summary: "Prometheus metrics",
		Desc: "Text exposition format. Off (404) unless system.metrics is enabled; then open to the addresses in system.metrics.allow and to API tokens.", Content: "text/plain"},

	// --- configuration and commits
	"GET /api/v1/config/running":   {Tag: "Config", Summary: "The running configuration", Resp: typ[config.Config]()},
	"GET /api/v1/config/candidate": {Tag: "Config", Summary: "The candidate configuration (uncommitted changes included)", Resp: typ[config.Config]()},
	"PUT /api/v1/config/candidate": {Tag: "Config", Summary: "Replace the candidate configuration",
		Desc: "The candidate is stored even when invalid; validation_errors lists what a commit would refuse.",
		Body: typ[config.Config](), Resp: candidateSaved},
	"PATCH /api/v1/config/candidate": {Tag: "Config", Summary: "Change one part of the candidate",
		Desc: "path names a section or element, e.g. `dhcp`, `nat.port_forwards`, `policies[id=3].enabled`; value replaces it, delete removes it. The token scope depends on the path.",
		Body: typ[patchRequest](), Resp: candidateSaved},
	"POST /api/v1/config/validate": {Tag: "Config", Summary: "Validate a configuration (the candidate when the body is empty)",
		Body: typ[config.Config](), Resp: obj{{"valid", boolean}, {"errors", typ[[]config.ValidationError]()}}},
	"GET /api/v1/config/diff":      {Tag: "Config", Summary: "Changes from the running to the candidate configuration", Resp: changeList},
	"POST /api/v1/policy/simulate": {Tag: "Policy", Summary: "Which policy a connection would match", Body: typ[simulateRequest](), Resp: ret(nft.Simulate)},
	"POST /api/v1/commit": {Tag: "Config", Summary: "Commit the candidate",
		Desc: "With confirm_minutes, the commit rolls back by itself unless POST /api/v1/commit/confirm follows in time.",
		Body: typ[commitRequest](), Resp: obj{{"revision", integer}, {"confirm_deadline", when}}},
	"POST /api/v1/commit/confirm":           {Tag: "Config", Summary: "Confirm a commit waiting for confirmation", Resp: status},
	"POST /api/v1/commit/discard":           {Tag: "Config", Summary: "Discard the candidate's changes", Resp: status},
	"GET /api/v1/revisions":                 {Tag: "Config", Summary: "Committed revisions, newest first", Resp: obj{{"revisions", typ[[]config.RevisionInfo]()}}},
	"POST /api/v1/revisions/{rev}/rollback": {Tag: "Config", Summary: "Roll back to a revision (as a new revision)", Resp: obj{{"revision", integer}, {"rolled_back_to", integer}}},
	"GET /api/v1/snapshots":                 {Tag: "Config", Summary: "Named configuration snapshots", Resp: obj{{"snapshots", ret((*config.Store).ListSnapshots)}}},
	"POST /api/v1/snapshots":                {Tag: "Config", Summary: "Save a snapshot of the running or candidate configuration", Body: typ[createSnapshotRequest](), Resp: typ[config.SnapshotInfo](), Status: 201},
	"GET /api/v1/snapshots/{name}":          {Tag: "Config", Summary: "A snapshot with its configuration", Resp: obj{{"name", str}, {"comment", str}, {"created", when}, {"created_by", str}, {"revision", integer}, {"config", typ[config.Config]()}}},
	"POST /api/v1/snapshots/{name}/restore": {Tag: "Config", Summary: "Load a snapshot as the candidate", Resp: obj{{"status", str}, {"changes", integer}}},
	"DELETE /api/v1/snapshots/{name}":       {Tag: "Config", Summary: "Delete a snapshot", Status: 204},
	"GET /api/v1/templates":                 {Tag: "Templates", Summary: "Configuration templates", Resp: obj{{"templates", ret(templates.List)}}},
	"POST /api/v1/templates/{id}/preview":   {Tag: "Templates", Summary: "What a template would change", Body: typ[templateRequest](), Resp: typ[templateResult]()},
	"POST /api/v1/templates/{id}/apply":     {Tag: "Templates", Summary: "Apply a template to the candidate", Desc: "Refused with the preview's errors when the input is not valid.", Body: typ[templateRequest](), Resp: obj{{"status", str}, {"changes", typ[[]config.Change]()}}},
	"GET /api/v1/system/generated":          {Tag: "Config", Summary: "Files the configuration generates", Resp: obj{{"files", arrayOf{obj{{"path", str}, {"renderer", str}, {"size", integer}, {"redacted", boolean}}}}}},
	"GET /api/v1/system/generated/file":     {Tag: "Config", Summary: "One generated file (secrets redacted)", Query: []queryParam{{Name: "path", Desc: "the file's path"}}, Resp: obj{{"path", str}, {"renderer", str}, {"content", str}, {"redacted", boolean}, {"truncated", boolean}}},
	"GET /api/v1/system/generated/diff":     {Tag: "Config", Summary: "How a generated file changes with the candidate", Query: []queryParam{{Name: "path", Desc: "the file's path"}}, Resp: obj{{"path", str}, {"diff", str}}},

	// --- system
	"GET /api/v1/system/status": {Tag: "System", Summary: "Version, uptime and state", Resp: obj{
		{"version", str}, {"hostname", str}, {"uptime_seconds", integer}, {"sqm", typ[[]SQMStatus]()},
		{"reboot_required", boolean}, {"reboot_packages", strs}, {"demo", boolean}, {"pending_confirm", obj{{"deadline", when}}}}},
	"GET /api/v1/system/info": {Tag: "System", Summary: "Host details", Resp: obj{
		{"version", str}, {"time", when}, {"hostname", str}, {"kernel", str}, {"uptime_seconds", integer}, {"timezone", str}, {"ntp_synchronized", boolean}}},
	"GET /api/v1/system/backup": {Tag: "System", Summary: "Download a backup", Desc: "A zip with the running configuration and its history. It contains password hashes and secrets.", Content: "application/zip"},
	"POST /api/v1/system/restore": {Tag: "System", Summary: "Load a backup as the candidate", Desc: "The zip as the body (application/zip) or as the form field `backup`.",
		Upload: "application/zip", Resp: obj{{"status", str}, {"hostname", str}, {"message", str}}},
	"POST /api/v1/system/reboot":              {Tag: "System", Summary: "Restart the box", Resp: status, Status: 202},
	"GET /api/v1/system/updates":              {Tag: "System", Summary: "Installed and available versions", Query: []queryParam{{Name: "refresh", Desc: "1 = check now instead of using the last check (slow)"}}, Resp: ret((*updates.Manager).Check)},
	"POST /api/v1/system/updates/apply":       {Tag: "System", Summary: "Start an update", Desc: "goblind restarts during the update; poll GET /api/v1/system/updates until last.state is no longer running.", Body: typ[updateApplyRequest](), Resp: status, Status: 202},
	"GET /api/v1/admins":                      {Tag: "Admins", Summary: "Admin accounts", Resp: obj{{"admins", typ[[]adminInfo]()}}},
	"POST /api/v1/admins":                     {Tag: "Admins", Summary: "Add an admin", Body: typ[adminRequest](), Resp: obj{{"admin", typ[adminInfo]()}}, Status: 201},
	"PATCH /api/v1/admins/{name}":             {Tag: "Admins", Summary: "Change an admin's role or password", Desc: "A new password signs that admin out everywhere.", Body: typ[adminUpdateRequest](), Resp: status},
	"DELETE /api/v1/admins/{name}":            {Tag: "Admins", Summary: "Delete an admin", Resp: status},
	"POST /api/v1/notifications/test":         {Tag: "System", Summary: "Send a test notification", Body: typ[notifyTestRequest](), Resp: status},
	"GET /api/v1/checks":                      {Tag: "Checks", Summary: "Goblin Checks: the network audit", Resp: ret(checks.Run)},
	"POST /api/v1/checks/dismiss":             {Tag: "Checks", Summary: "Dismiss a finding", Body: typ[dismissRequest](), Resp: status},
	"DELETE /api/v1/checks/dismiss/{key}":     {Tag: "Checks", Summary: "Show a dismissed finding again", Resp: status},
	"GET /api/v1/reverse-proxy":               {Tag: "Network", Summary: "Reverse proxy state and certificates", Desc: "Whether the reverse proxy (Caddy) runs, and for each published domain its certificate and the latest error getting one.", Resp: ret((*reverseproxy.Reader).Status)},
	"GET /api/v1/ups":                         {Tag: "System", Summary: "UPS state", Resp: ret((*ups.Manager).Status)},
	"GET /api/v1/git-backup":                  {Tag: "System", Summary: "Configuration history in git: state and deploy key", Resp: ret((*gitbackup.Manager).Status)},
	"POST /api/v1/git-backup/push":            {Tag: "System", Summary: "Push the configuration history now", Resp: ret((*gitbackup.Manager).Status)},
	"POST /api/v1/git-backup/forget-host-key": {Tag: "System", Summary: "Forget the git server's pinned host key", Resp: ret((*gitbackup.Manager).Status)},
	"GET /api/v1/log-export":                  {Tag: "Logs", Summary: "Log export destinations and their state", Resp: obj{{"entries", ret((*logexport.Exporter).Status)}}},
	"GET /api/v1/ui/dashboards":               {Tag: "UI", Summary: "The caller's dashboards", Desc: "A JSON document the web UI owns.", Resp: anyObject{}},
	"PUT /api/v1/ui/dashboards":               {Tag: "UI", Summary: "Store the caller's dashboards", Body: anyObject{}, Resp: status},
	"GET /api/v1/ui/prefs":                    {Tag: "UI", Summary: "The caller's UI preferences", Resp: obj{{"language", str}}},
	"PUT /api/v1/ui/prefs":                    {Tag: "UI", Summary: "Store the caller's UI preferences", Body: obj{{"language", str}}, Resp: status},

	// --- authentication
	"POST /api/v1/auth/logout":        {Tag: "Auth", Summary: "Log out (ends the session)", Resp: status},
	"GET /api/v1/auth/me":             {Tag: "Auth", Summary: "Who is asking", Resp: typ[meResponse]()},
	"POST /api/v1/auth/password":      {Tag: "Auth", Summary: "Change your password", Body: typ[passwordRequest](), Resp: obj{{"status", str}, {"revision", integer}}},
	"POST /api/v1/auth/2fa/setup":     {Tag: "Auth", Summary: "Start setting up two-factor login", Desc: "Returns a new secret to scan; it takes effect with /auth/2fa/enable.", Resp: obj{{"secret", str}, {"otpauth_url", str}}},
	"POST /api/v1/auth/2fa/enable":    {Tag: "Auth", Summary: "Turn two-factor login on with a code from the app", Body: typ[twoFactorEnableRequest](), Resp: status},
	"POST /api/v1/auth/2fa/disable":   {Tag: "Auth", Summary: "Turn two-factor login off", Body: typ[twoFactorDisableRequest](), Resp: status},
	"GET /api/v1/auth/tokens":         {Tag: "Auth", Summary: "API tokens", Resp: obj{{"tokens", ret((*auth.Tokens).List)}}},
	"POST /api/v1/auth/tokens":        {Tag: "Auth", Summary: "Create an API token", Desc: "token is shown only in this answer.", Body: typ[tokenRequest](), Resp: obj{{"token", str}, {"info", typ[auth.TokenInfo]()}}, Status: 201},
	"DELETE /api/v1/auth/tokens/{id}": {Tag: "Auth", Summary: "Revoke an API token", Resp: status},
	"GET /api/v1/auth/sessions": {Tag: "Auth", Summary: "Active login sessions", Resp: obj{{"sessions", arrayOf{obj{
		{"id", str}, {"user", str}, {"role", str}, {"addr", str}, {"user_agent", str}, {"created", when}, {"last_seen", when}, {"current", boolean}}}}}},
	"DELETE /api/v1/auth/sessions/{id}": {Tag: "Auth", Summary: "Sign a session out", Status: 204},

	// --- network
	"GET /api/v1/dhcp/leases":         {Tag: "Network", Summary: "Active DHCP leases", Resp: obj{{"leases", typ[[]dhcpLease]()}}},
	"GET /api/v1/upnp/mappings":       {Tag: "Network", Summary: "Active UPnP / NAT-PMP port mappings", Resp: obj{{"enabled", boolean}, {"mappings", ret(upnp.Read)}}},
	"GET /api/v1/gateways/status":     {Tag: "Network", Summary: "Gateway monitoring and routing state", Resp: typ[gateways.Report]()},
	"GET /api/v1/wan/status":          {Tag: "Network", Summary: "WAN links, addresses and next hops", Resp: obj{{"wans", typ[[]WANStatus]()}}},
	"GET /api/v1/devices":             {Tag: "Network", Summary: "Devices seen on the LAN", Query: []queryParam{{Name: "interface", Desc: "only devices on this interface"}}, Resp: obj{{"devices", ret((*devices.Store).List)}}},
	"PATCH /api/v1/devices/{mac}":     {Tag: "Network", Summary: "Name a device", Body: typ[deviceNameRequest](), Resp: ret((*devices.Store).SetName)},
	"POST /api/v1/devices/{mac}/wake": {Tag: "Network", Summary: "Wake a device (Wake-on-LAN)", Resp: obj{{"status", str}, {"broadcast", str}}},
	"DELETE /api/v1/devices/{mac}":    {Tag: "Network", Summary: "Forget a device", Status: 204},
	"GET /api/v1/ddns":                {Tag: "Network", Summary: "Dynamic DNS entries and their last update", Resp: obj{{"entries", ret((*ddns.Manager).Status)}}},
	"POST /api/v1/ddns/{name}/update": {Tag: "Network", Summary: "Update a dynamic DNS entry now", Resp: ret((*ddns.Manager).UpdateNow)},
	"POST /api/v1/import/pfsense": {Tag: "Config", Summary: "Import a pfSense or OPNsense config.xml into the candidate",
		Desc:  "The file as the body or as a form field. devices maps the old interface names to devices.",
		Query: []queryParam{{Name: "devices", Desc: "JSON object: old interface name → device"}}, Upload: "application/xml", Resp: ret(pfsense.Import)},
	"POST /api/v1/diag/ping":       {Tag: "Diagnostics", Summary: "Ping a host", Body: typ[pingRequest](), Resp: ret(diag.Ping)},
	"POST /api/v1/diag/traceroute": {Tag: "Diagnostics", Summary: "Trace the route to a host", Body: typ[tracerouteRequest](), Resp: ret(diag.Traceroute)},
	"POST /api/v1/diag/dns":        {Tag: "Diagnostics", Summary: "Look up a DNS name", Body: typ[dnsLookupRequest](), Resp: typ[diag.DNSResult]()},
	"GET /api/v1/diag/nics": {Tag: "Diagnostics", Summary: "Network cards: driver, link speed, offloads and hints",
		Desc: "Per physical interface; hints[].code is stable (no_link, slow_link, half_duplex, offloads_off, slow_driver, single_queue).",
		Resp: obj{{"interfaces", typ[[]NICView]()}}},
	"GET /api/v1/diag/throughput": {Tag: "Diagnostics", Summary: "The throughput test server's state and results", Resp: typ[throughput.Status]()},
	"POST /api/v1/diag/throughput": {Tag: "Diagnostics", Summary: "Start a throughput test server",
		Desc: "Runs iperf3 on the interface's address (port 5201, open only on that interface) for a few minutes. Measure from a LAN computer with `iperf3 -c <address>`, add -R for the download direction. LAN-side interfaces only (code wan_interface otherwise); 409 throughput_running while one runs.",
		Body: typ[throughputRequest](), Resp: typ[throughput.Status](), Status: 201},
	"DELETE /api/v1/diag/throughput": {Tag: "Diagnostics", Summary: "Stop the throughput test server", Resp: typ[throughput.Status]()},
	"GET /api/v1/flows":              {Tag: "Diagnostics", Summary: "Current connections", Query: []queryParam{{Name: "ip", Desc: "only connections of this address"}, {Name: "limit", Type: integer}}, Resp: obj{{"flows", typ[[]flows.Flow]()}, {"total", integer}}},
	"GET /api/v1/flows/devices":      {Tag: "Diagnostics", Summary: "Traffic rates per device", Resp: obj{{"devices", ret((*flows.Sampler).Devices)}}},
	"POST /api/v1/capture":           {Tag: "Diagnostics", Summary: "Start a packet capture", Body: typ[captureRequest](), Resp: ret((*capture.Manager).Status), Status: 201},
	"GET /api/v1/capture/{id}":       {Tag: "Diagnostics", Summary: "A packet capture's state", Resp: ret((*capture.Manager).Status)},
	"GET /api/v1/capture/{id}/live": {Tag: "Diagnostics", Summary: "Packets as they are captured",
		Desc: "Server-Sent Events: `packet` per packet, `dropped` {count} when packets were left out, `done` at the end.", Content: "text/event-stream"},
	"GET /api/v1/capture/{id}/pcap": {Tag: "Diagnostics", Summary: "Download a capture", Content: "application/vnd.tcpdump.pcap"},
	"DELETE /api/v1/capture/{id}":   {Tag: "Diagnostics", Summary: "Stop and delete a capture", Status: 204},

	// --- VPN
	"GET /api/v1/vpn/tailscale/status": {Tag: "VPN", Summary: "Tailscale state", Resp: obj{
		{"available", boolean}, {"error", str}, {"backend_state", str}, {"ips", strs}, {"auth_url", str}, {"hostname", str},
		{"approved_routes", strs}, {"advertised_routes", strs}, {"peers", typ[[]TailscalePeer]()}}},
	"POST /api/v1/vpn/tailscale/login": {Tag: "VPN", Summary: "Log Tailscale in",
		Desc: "With auth_key, logs in with it (the key is not stored). Without, answers auth_url to open in a browser.",
		Body: typ[tailscaleLoginRequest](), Resp: obj{{"status", str}, {"auth_url", str}}},
	"POST /api/v1/vpn/tailscale/logout": {Tag: "VPN", Summary: "Log Tailscale out", Resp: status},
	"GET /api/v1/vpn/wireguard":         {Tag: "VPN", Summary: "WireGuard servers and their peers", Resp: obj{{"interfaces", typ[[]wgView]()}}},
	"POST /api/v1/vpn/wireguard/{name}/peers": {Tag: "VPN", Summary: "Add a peer to a WireGuard server",
		Desc: "Adds the peer to the candidate (commit to activate) and returns the client's config; its private key is not kept.",
		Body: typ[wgPeerRequest](), Resp: obj{{"peer", typ[config.WireGuardPeer]()}, {"config", str}, {"qr_svg", str}}, Status: 201},
	"GET /api/v1/vpn/wireguard-clients/{name}/public-key":  {Tag: "VPN", Summary: "The public key of a VPN-provider tunnel", Desc: "Generated on first use; register it with the provider.", Resp: obj{{"public_key", str}}},
	"PUT /api/v1/vpn/wireguard-clients/{name}/private-key": {Tag: "VPN", Summary: "Set a VPN-provider tunnel's private key", Body: typ[privateKeyRequest](), Resp: obj{{"public_key", str}}},

	// --- security
	"GET /api/v1/feeds":                 {Tag: "Security", Summary: "Threat feeds and their state", Resp: obj{{"feeds", ret((*feeds.Manager).Status)}}},
	"GET /api/v1/feeds/lookup":          {Tag: "Security", Summary: "Which feeds list an address, domain or URL", Query: []queryParam{{Name: "q", Desc: "an IP, domain or URL"}}, Resp: obj{{"query", str}, {"feeds", strs}}},
	"POST /api/v1/feeds/{name}/refresh": {Tag: "Security", Summary: "Download a feed now", Resp: ret((*feeds.Manager).Refresh)},
	"GET /api/v1/geoip":                 {Tag: "Security", Summary: "GeoIP database state and the country list", Resp: obj{{"status", ret((*geoip.Manager).Status)}, {"countries", typ[[]country]()}}},
	"GET /api/v1/geoip/lookup":          {Tag: "Security", Summary: "The country of an address", Query: []queryParam{{Name: "ip"}}, Resp: obj{{"ip", str}, {"country", str}, {"name", str}}},
	"POST /api/v1/geoip/refresh":        {Tag: "Security", Summary: "Download the GeoIP database now", Resp: ret((*geoip.Manager).Status)},
	"GET /api/v1/crowdsec":              {Tag: "Security", Summary: "CrowdSec bouncer state", Resp: ret((*crowdsec.Manager).Status)},
	"GET /api/v1/ips/rules":             {Tag: "Security", Summary: "IPS rule downloads", Resp: typ[ips.UpdateStatus]()},
	"GET /api/v1/webfilter/categories":  {Tag: "Security", Summary: "Web filter categories", Resp: obj{{"categories", strs}, {"entries", integer}}},
	"GET /api/v1/webfilter/lookup": {Tag: "Security", Summary: "A domain's categories and what each profile decides", Query: []queryParam{{Name: "q", Desc: "a domain"}},
		Resp: obj{{"domain", str}, {"categories", strs}, {"profiles", mapOf{typ[webfilter.Decision]()}}}},
	"GET /api/v1/appctrl/apps": {Tag: "Security", Summary: "Applications application control knows", Resp: obj{{"apps", ret(appctrl.Catalog)}}},
	"GET /api/v1/blocks":       {Tag: "Security", Summary: "Temporary blocks", Resp: obj{{"blocks", typ[[]blockView]()}}},
	"POST /api/v1/blocks": {Tag: "Security", Summary: "Block an address for a while",
		Desc: "Addresses of the box's own networks need allow_internal (refused with code protected_target otherwise).",
		Body: typ[blockRequest](), Resp: typ[blockView](), Status: 201},
	"DELETE /api/v1/blocks/{ip}": {Tag: "Security", Summary: "Lift a temporary block", Status: 204},

	// --- logs and statistics
	"GET /api/v1/logs": {Tag: "Logs", Summary: "Stored events, newest first",
		Desc: "Pages with cursor (the next_cursor of the previous page; 0 = last page). after returns events newer than an id, oldest first.",
		Query: []queryParam{{Name: "kind", Desc: "traffic, ips, dns, system, …"}, {Name: "q", Desc: "text search"}, {Name: "from", Desc: "RFC 3339, unix time or -15m"}, {Name: "to"},
			{Name: "cursor", Type: integer}, {Name: "after", Type: integer}, {Name: "limit", Type: integer}},
		Resp: obj{{"events", ret((*logs.Store).ListEvents)}, {"next_cursor", integer}}},
	"GET /api/v1/events": {Tag: "Logs", Summary: "Live events", Desc: "Server-Sent Events: `event: <kind>`, data {event, key, title, message, time, fields}.",
		Query: []queryParam{{Name: "types", Desc: "comma-separated kinds; empty = all"}}, Content: "text/event-stream"},
	"POST /api/v1/stats/query":       {Tag: "Logs", Summary: "Query event statistics", Body: typ[logs.Query](), Resp: ret((*logs.Store).Query)},
	"GET /api/v1/stats/policies":     {Tag: "Logs", Summary: "Hits per policy", Resp: obj{{"policies", ret((*stats.PolicyTracker).Read)}}},
	"GET /api/v1/stats/system":       {Tag: "Logs", Summary: "CPU, memory and uptime", Resp: ret((*stats.SystemSampler).Sample)},
	"GET /api/v1/stats/feeds":        {Tag: "Logs", Summary: "Hits per threat feed", Resp: obj{{"feeds", typ[[]FeedHit]()}}},
	"GET /api/v1/stats/custom-rules": {Tag: "Logs", Summary: "Hits per custom nftables rule", Resp: obj{{"rules", typ[[]CustomHit]()}}},

	// --- plugins
	"GET /api/v1/plugins":                  {Tag: "Plugins", Summary: "Installed plugins", Resp: obj{{"plugins", ret((*plugins.Manager).List)}}},
	"POST /api/v1/plugins/{name}/enable":   {Tag: "Plugins", Summary: "Enable a plugin (in the candidate)", Body: typ[pluginEnableRequest](), Resp: candidateSaved},
	"POST /api/v1/plugins/{name}/disable":  {Tag: "Plugins", Summary: "Disable a plugin (in the candidate)", Resp: candidateSaved},
	"PUT /api/v1/plugins/{name}/settings":  {Tag: "Plugins", Summary: "Change a plugin's settings (in the candidate)", Body: typ[pluginSettingsRequest](), Resp: candidateSaved},
	"PUT /api/v1/plugins/{name}/policies":  {Tag: "Plugins", Summary: "Set the policies a plugin may switch (in the candidate)", Desc: "Needs the capability policies:toggle approved; the plugin can switch only these policies on and off.", Body: typ[pluginPoliciesRequest](), Resp: candidateSaved},
	"/api/v1/plugins/{name}/api/{path...}": {Tag: "Plugins", Summary: "A plugin's own API", Desc: "Passed to the plugin; JSON answers only. Read-only admins may use GET only.", Body: anyJSON{}, Resp: anyJSON{}},
}
