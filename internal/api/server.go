// Package api is the Goblin Gate REST API used by the web GUI and the CLI.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"net/netip"
	"os"
	"sync"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/blocks"
	"github.com/networkgoblinlabs/goblin-gate/internal/capture"
	"github.com/networkgoblinlabs/goblin-gate/internal/commit"
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
	"github.com/networkgoblinlabs/goblin-gate/internal/logexport"
	"github.com/networkgoblinlabs/goblin-gate/internal/logs"
	"github.com/networkgoblinlabs/goblin-gate/internal/notify"
	"github.com/networkgoblinlabs/goblin-gate/internal/plugins"
	"github.com/networkgoblinlabs/goblin-gate/internal/stats"
	"github.com/networkgoblinlabs/goblin-gate/internal/tailscale"
	"github.com/networkgoblinlabs/goblin-gate/internal/updates"
	"github.com/networkgoblinlabs/goblin-gate/internal/version"
	"github.com/networkgoblinlabs/goblin-gate/internal/webfilter"
	"github.com/networkgoblinlabs/goblin-gate/internal/wireguard"
)

// MaxBodyBytes limits request bodies.
const MaxBodyBytes = 4 << 20

// Server serves the REST API.
type Server struct {
	Store  *config.Store
	Commit *commit.Engine
	Logger *slog.Logger

	// Authn turns on authentication (sessions, API tokens, the local
	// socket); nil leaves the API open (dev and tests).
	Authn *Authenticator

	// Auth wraps every authenticated /api/v1 route. Routes sets it from
	// Authn; tests may set it directly.
	Auth func(http.Handler) http.Handler

	// UserFrom returns the user name for audit fields; nil means the
	// authenticated identity, or "api".
	UserFrom func(*http.Request) string

	// Static serves everything outside /api/ (the web UI) without
	// authentication; nil answers 404.
	Static http.Handler

	// Feeds serves the threat feed endpoints; nil answers 503.
	Feeds *feeds.Manager
	// DDNS runs dynamic DNS updates (task 087); nil answers 503.
	DDNS *ddns.Manager
	// GeoIP owns the country database (task 094); nil answers 503.
	GeoIP *geoip.Manager
	// GitBackup records revisions in git and pushes them (task 098); nil
	// answers 503.
	GitBackup *gitbackup.Manager
	// CrowdSec is the bouncer (task 104); nil answers 503.
	CrowdSec *crowdsec.Manager
	// LogExport forwards events to remote syslog / Loki (task 096); nil
	// answers 503.
	LogExport *logexport.Exporter
	// Notify sends alerts (task 053); nil sends none.
	Notify *notify.Dispatcher
	// Updates installs package updates (task 051); nil answers 503 (dev mode).
	Updates *updates.Manager
	// Gateways reports the multi-WAN gateway monitor (task 049); nil answers
	// "not enabled".
	Gateways interface{ Report() gateways.Report }
	// UPnPLeaseFile is miniupnpd's lease file (task 059); "" = the box's
	// default (internal/render/upnp.LeaseFile).
	UPnPLeaseFile string
	// DHCPLeaseFile is Kea's lease file; "" = internal/render/kea.LeaseFile.
	DHCPLeaseFile string

	// Logs, Policies and System serve the stats and log endpoints; nil
	// answers 503.
	Logs     *logs.Store
	Policies *stats.PolicyTracker
	System   *stats.SystemSampler
	FeedHits stats.FeedCounterSource
	// CustomHits reads the custom nftables rule counters (task 100).
	CustomHits stats.CustomCounterSource
	// CheckCustom, if set, checks a config's custom nftables rules with
	// `nft -c` (POST /api/v1/config/validate; commits check it too).
	CheckCustom func(ctx context.Context, cfg *config.Config) []config.ValidationError

	// UI stores per-user web UI documents (dashboards); nil answers 503.
	UI *UIStore

	// WebFilter serves the category endpoints; nil answers 503.
	WebFilter *webfilter.Filter

	// Tailscale talks to tailscaled; nil (dev) reports it unavailable.
	Tailscale *tailscale.Client

	// WireGuardKeyDir holds the WireGuard server keys ("" answers 503);
	// WireGuardStats reads live peer stats (nil = none).
	WireGuardKeyDir string
	WireGuardStats  func(ctx context.Context, dev string) (map[string]wireguard.PeerStat, error)

	// Devices keeps the device inventory (task 070); nil answers 503.
	Devices *devices.Store

	// Blocks keeps temporary blocks; nil = not available (dev).
	Blocks *blocks.Manager
	// LiveAddrs returns run-time addresses that must never be blocked
	// (blocks.LiveAddrs); nil = config-based checks only.
	LiveAddrs func() []netip.Addr

	// Plugins runs the optional plugins and checks their tokens; nil = no
	// plugin support (dev).
	Plugins *plugins.Manager
	// Capture runs packet captures (task 076); nil answers 503 (dev mode).
	Capture *capture.Manager
	// Flows samples the connection table (task 078); nil answers 503.
	Flows *flows.Sampler

	// DiagRunner runs ping/traceroute and DiagLookup does DNS lookups for
	// the diagnostics; nil = the real ones (tests replace them).
	DiagRunner diag.Runner
	DiagLookup func(ctx context.Context, name, typ, server string) (diag.DNSResult, error)

	// SQMActive reports whether CAKE shapes a device; nil = ask tc.
	SQMActive func(dev string) bool

	started  time.Time
	setupMu  sync.Mutex
	diagOnce sync.Once
	diagSem  chan struct{}
}

// NewServer creates an API server.
func NewServer(store *config.Store, engine *commit.Engine) *Server {
	return &Server{Store: store, Commit: engine, Logger: slog.Default(), started: time.Now()}
}

// Routes returns the HTTP handler with every API route registered.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	if s.Authn != nil {
		s.Auth = s.authenticate
	}
	handle := func(pattern string, h http.HandlerFunc) {
		var handler http.Handler = h
		if s.Auth != nil {
			handler = s.Auth(handler)
		}
		mux.Handle(pattern, handler)
	}
	s.register(handle)
	mux.HandleFunc("POST /api/v1/auth/login", s.login)
	// First-boot setup: open without authentication until an admin exists.
	mux.HandleFunc("GET /api/v1/setup/state", s.setupState)
	mux.HandleFunc("POST /api/v1/setup", s.setup)
	mux.Handle("GET /api/v1/system/devices", s.openDuringSetup(s.devices))
	mux.HandleFunc("GET /plugin-ui/{name}/{path...}", s.pluginUI)
	// Prometheus scrapers have no session: /metrics checks its own allow
	// list and API tokens.
	mux.HandleFunc("GET /metrics", s.metrics)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no such API endpoint", nil)
	})
	if s.Static != nil {
		mux.Handle("/", s.Static)
	}
	return s.middleware(mux)
}

// register adds every authenticated API route. Routes wraps each with the
// admin authentication; PluginRoutes keeps the ones plugins may call.
func (s *Server) register(handle func(pattern string, h http.HandlerFunc)) {
	handle("GET /api/v1/config/running", s.getRunning)
	handle("GET /api/v1/config/candidate", s.getCandidate)
	handle("PUT /api/v1/config/candidate", s.putCandidate)
	handle("PATCH /api/v1/config/candidate", s.patchCandidate)
	handle("POST /api/v1/config/validate", s.validate)
	handle("GET /api/v1/config/diff", s.diff)
	handle("POST /api/v1/policy/simulate", s.simulatePolicy)
	handle("POST /api/v1/commit", s.commit)
	handle("POST /api/v1/commit/confirm", s.confirm)
	handle("POST /api/v1/commit/discard", s.discard)
	handle("GET /api/v1/revisions", s.revisions)
	handle("POST /api/v1/revisions/{rev}/rollback", s.rollback)
	handle("GET /api/v1/system/status", s.status)
	handle("GET /api/v1/system/generated", s.generatedList)
	handle("GET /api/v1/system/generated/file", s.generatedFile)
	handle("GET /api/v1/system/generated/diff", s.generatedDiff)
	handle("GET /api/v1/system/info", s.systemInfo)
	handle("GET /api/v1/system/backup", s.backup)
	handle("POST /api/v1/system/restore", s.restore)
	handle("GET /api/v1/dhcp/leases", s.dhcpLeases)
	handle("POST /api/v1/diag/ping", s.diagPing)
	handle("POST /api/v1/diag/traceroute", s.diagTraceroute)
	handle("POST /api/v1/diag/dns", s.diagDNS)
	handle("GET /api/v1/snapshots", s.listSnapshots)
	handle("POST /api/v1/snapshots", s.createSnapshot)
	handle("GET /api/v1/snapshots/{name}", s.getSnapshot)
	handle("POST /api/v1/snapshots/{name}/restore", s.restoreSnapshot)
	handle("DELETE /api/v1/snapshots/{name}", s.deleteSnapshot)
	handle("GET /api/v1/admins", s.listAdmins)
	handle("POST /api/v1/admins", s.createAdmin)
	handle("PATCH /api/v1/admins/{name}", s.updateAdmin)
	handle("DELETE /api/v1/admins/{name}", s.deleteAdmin)
	handle("POST /api/v1/notifications/test", s.notifyTest)
	handle("GET /api/v1/upnp/mappings", s.upnpMappings)
	handle("POST /api/v1/import/pfsense", s.importPfSense)
	handle("GET /api/v1/gateways/status", s.gatewayStatus)
	handle("GET /api/v1/vpn/wireguard-clients/{name}/public-key", s.wireguardClientPublicKey)
	handle("PUT /api/v1/vpn/wireguard-clients/{name}/private-key", s.wireguardClientSetKey)
	handle("GET /api/v1/system/updates", s.updateStatus)
	handle("POST /api/v1/system/updates/apply", s.applyUpdate)
	handle("GET /api/v1/feeds", s.feedStatus)
	handle("GET /api/v1/feeds/lookup", s.feedLookup)
	handle("POST /api/v1/feeds/{name}/refresh", s.feedRefresh)
	handle("GET /api/v1/git-backup", s.gitBackupStatus)
	handle("POST /api/v1/git-backup/push", s.gitBackupPush)
	handle("POST /api/v1/git-backup/forget-host-key", s.gitBackupForgetHostKey)
	handle("GET /api/v1/geoip", s.geoipStatus)
	handle("GET /api/v1/geoip/lookup", s.geoipLookup)
	handle("POST /api/v1/geoip/refresh", s.geoipRefresh)
	handle("GET /api/v1/ddns", s.ddnsStatus)
	handle("POST /api/v1/ddns/{name}/update", s.ddnsUpdateNow)
	handle("GET /api/v1/log-export", s.logExportStatus)
	handle("POST /api/v1/stats/query", s.statsQuery)
	handle("GET /api/v1/stats/policies", s.policyStats)
	handle("GET /api/v1/stats/system", s.systemStats)
	handle("GET /api/v1/stats/feeds", s.feedStats)
	handle("GET /api/v1/stats/custom-rules", s.customRuleStats)
	handle("GET /api/v1/events", s.events)
	handle("GET /api/v1/crowdsec", s.crowdSecStatus)
	handle("GET /api/v1/logs", s.listLogs)
	handle("GET /api/v1/ui/dashboards", s.getDashboards)
	handle("PUT /api/v1/ui/dashboards", s.putDashboards)
	handle("GET /api/v1/webfilter/categories", s.webfilterCategories)
	handle("GET /api/v1/webfilter/lookup", s.webfilterLookup)
	handle("GET /api/v1/appctrl/apps", s.appctrlApps)
	handle("GET /api/v1/vpn/tailscale/status", s.tailscaleStatus)
	handle("POST /api/v1/vpn/tailscale/login", s.tailscaleLogin)
	handle("POST /api/v1/vpn/tailscale/logout", s.tailscaleLogout)
	handle("GET /api/v1/vpn/wireguard", s.wireguardList)
	handle("POST /api/v1/vpn/wireguard/{name}/peers", s.wireguardAddPeer)
	handle("POST /api/v1/auth/logout", s.logout)
	handle("GET /api/v1/auth/me", s.me)
	handle("POST /api/v1/auth/password", s.changePassword)
	handle("POST /api/v1/auth/2fa/setup", s.twoFactorSetup)
	handle("POST /api/v1/auth/2fa/enable", s.twoFactorEnable)
	handle("POST /api/v1/auth/2fa/disable", s.twoFactorDisable)
	handle("GET /api/v1/auth/tokens", s.listTokens)
	handle("POST /api/v1/auth/tokens", s.createToken)
	handle("DELETE /api/v1/auth/tokens/{id}", s.deleteToken)
	handle("GET /api/v1/auth/sessions", s.listSessions)
	handle("DELETE /api/v1/auth/sessions/{id}", s.deleteSession)
	handle("GET /api/v1/flows", s.listFlows)
	handle("GET /api/v1/flows/devices", s.flowDevices)
	handle("POST /api/v1/capture", s.startCapture)
	handle("GET /api/v1/capture/{id}", s.captureStatus)
	handle("GET /api/v1/capture/{id}/live", s.captureLive)
	handle("GET /api/v1/capture/{id}/pcap", s.capturePcap)
	handle("DELETE /api/v1/capture/{id}", s.stopCapture)
	handle("GET /api/v1/blocks", s.listBlocks)
	handle("POST /api/v1/blocks", s.addBlock)
	handle("DELETE /api/v1/blocks/{ip}", s.removeBlock)
	handle("GET /api/v1/devices", s.listDevices)
	handle("PATCH /api/v1/devices/{mac}", s.patchDevice)
	handle("POST /api/v1/devices/{mac}/wake", s.wakeDevice)
	handle("DELETE /api/v1/devices/{mac}", s.deleteDevice)
	handle("GET /api/v1/plugins", s.listPlugins)
	handle("POST /api/v1/plugins/{name}/enable", s.enablePlugin)
	handle("POST /api/v1/plugins/{name}/disable", s.disablePlugin)
	handle("PUT /api/v1/plugins/{name}/settings", s.pluginSettings)
	handle("/api/v1/plugins/{name}/api/{path...}", s.pluginAPI)
}

// middleware adds request logging (no bodies), body limits, content-type
// checks and the security headers.
func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		h := w.Header()
		h.Set("X-Frame-Options", "SAMEORIGIN") // only the UI may frame its pages (plugin UIs)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		// uploads are the non-JSON bodies: a backup (zip) and a pfSense
		// config.xml, raw or as a form
		upload := r.Method == http.MethodPost && (r.URL.Path == "/api/v1/system/restore" || r.URL.Path == "/api/v1/import/pfsense")
		if r.Body != nil {
			limit := int64(MaxBodyBytes)
			if upload {
				limit = MaxBackupSize + 1<<20
			}
			r.Body = http.MaxBytesReader(rec, r.Body, limit)
		}
		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch {
			if r.ContentLength != 0 && !isJSON(r.Header.Get("Content-Type")) && !(upload && isUpload(r.Header.Get("Content-Type"))) {
				writeError(rec, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json", nil)
				s.logRequest(r, rec.status, start)
				return
			}
		}
		next.ServeHTTP(rec, r)
		s.logRequest(r, rec.status, start)
	})
}

func (s *Server) logRequest(r *http.Request, status int, start time.Time) {
	s.Logger.Info("api", "method", r.Method, "path", r.URL.Path, "status", status, "duration", time.Since(start).Round(time.Microsecond))
}

func isJSON(ct string) bool {
	mt, _, err := mime.ParseMediaType(ct)
	return err == nil && mt == "application/json"
}

// isUpload accepts the content types a backup upload may use.
func isUpload(ct string) bool {
	mt, _, err := mime.ParseMediaType(ct)
	return err == nil && (mt == "multipart/form-data" || mt == "application/zip" || mt == "application/octet-stream" ||
		mt == "application/xml" || mt == "text/xml")
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the real writer (flushing of
// event streams).
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (s *Server) user(r *http.Request) string {
	if s.UserFrom != nil {
		if u := s.UserFrom(r); u != "" {
			return u
		}
	}
	if id, ok := IdentityFrom(r); ok && id.User != "" {
		return id.User
	}
	return "api"
}

// ErrorBody is the JSON shape of every error response.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail describes one API error.
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string, details any) {
	writeJSON(w, status, ErrorBody{Error: ErrorDetail{Code: code, Message: message, Details: details}})
}

// decodeJSON reads one JSON value from the body into v, rejecting unknown
// fields and trailing data.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body is too large", nil)
			return false
		}
		writeError(w, http.StatusBadRequest, "invalid_json", "request body is not valid JSON for this endpoint", err.Error())
		return false
	}
	if dec.More() {
		writeError(w, http.StatusBadRequest, "invalid_json", "unexpected data after the JSON body", nil)
		return false
	}
	return true
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	host, _ := os.Hostname()
	resp := map[string]any{
		"version":        version.Version,
		"hostname":       host,
		"uptime_seconds": int64(time.Since(s.started).Seconds()),
	}
	if cfg, err := s.Store.Running(); err == nil && cfg.System.Hostname != "" {
		resp["hostname"] = cfg.System.Hostname
		resp["sqm"] = s.sqmStatus(cfg)
	}
	// a kernel (or libc, systemd) security update waits for a restart
	resp["reboot_required"], resp["reboot_packages"] = rebootRequired()
	if deadline, ok := s.Commit.PendingConfirm(); ok {
		resp["pending_confirm"] = map[string]any{"deadline": deadline.UTC().Format(time.RFC3339)}
	} else {
		resp["pending_confirm"] = nil
	}
	writeJSON(w, http.StatusOK, resp)
}
