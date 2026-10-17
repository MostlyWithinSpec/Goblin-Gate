package plugins

// Routes lists every API route a plugin may call (the pattern as the API
// registers it) and the capability it needs; "" = any plugin. Routes not
// listed here do not exist on the plugin socket.
var Routes = map[string]string{
	"GET /api/v1/plugin/self":     "",
	"POST /api/v1/plugin/notify":  "notify:send",
	"GET /api/v1/logs":            "events:read",
	"GET /api/v1/stats/policies":  "stats:read",
	"GET /api/v1/stats/system":    "stats:read",
	"GET /api/v1/stats/feeds":     "stats:read",
	"POST /api/v1/stats/query":    "stats:read",
	"GET /api/v1/config/running":  "config:read",
	"GET /api/v1/gateways/status": "stats:read",
	"GET /api/v1/blocks":          "blocks:read",
	"POST /api/v1/blocks":         "blocks:write",
	"DELETE /api/v1/blocks/{ip}":  "blocks:write",
}

// RouteCapability returns the capability a route needs and whether
// plugins may call it at all.
func RouteCapability(pattern string) (string, bool) {
	c, ok := Routes[pattern]
	return c, ok
}
