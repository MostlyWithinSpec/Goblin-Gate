package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/auth"
	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/notify"
	"github.com/networkgoblinlabs/goblin-gate/internal/plugins"
)

// Plugin API (docs/dev/plugins.md). Admins manage plugins through the
// candidate config; plugins call goblind on their own socket, identified by
// their systemd unit, limited to the routes in plugins.Routes and the
// capabilities the admin approved.

type pluginSocketKey struct{}
type pluginKey struct{}

// pluginPeer is who connected to the plugin socket.
type pluginPeer struct {
	name string // plugin name; "" = not a plugin unit
}

// PluginConnContext returns the http.Server.ConnContext for the plugin
// socket: it marks connections as plugin connections and records which
// plugin connected, as peer identifies it (plugins.PeerUnit). The identity
// is fixed when the connection is accepted. Only PluginRoutes should
// serve these connections.
func PluginConnContext(peer func(net.Conn) (string, bool)) func(context.Context, net.Conn) context.Context {
	return func(ctx context.Context, c net.Conn) context.Context {
		name, ok := peer(c)
		if !ok {
			name = ""
		}
		return context.WithValue(ctx, pluginSocketKey{}, pluginPeer{name: name})
	}
}

func pluginFrom(r *http.Request) (plugins.Identity, bool) {
	id, ok := r.Context().Value(pluginKey{}).(plugins.Identity)
	return id, ok
}

// PluginRoutes returns the handler for the plugin socket: only the routes
// plugins.Routes lists exist, each checked against the calling plugin's
// approved capabilities and audited.
func (s *Server) PluginRoutes() http.Handler {
	mux := http.NewServeMux()
	add := func(pattern string, h http.HandlerFunc) {
		capability, ok := plugins.RouteCapability(pattern)
		if !ok {
			return
		}
		mux.Handle(pattern, s.pluginAuth(pattern, capability, h))
	}
	s.register(add)
	add("GET /api/v1/plugin/self", s.pluginSelf)
	add("POST /api/v1/plugin/notify", s.pluginNotify)
	mux.Handle("/", s.pluginAuth("", "-", nil))
	return s.middleware(mux)
}

func (s *Server) pluginAuth(pattern, capability string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer, onSocket := r.Context().Value(pluginSocketKey{}).(pluginPeer)
		if s.Plugins == nil || !onSocket {
			writeError(w, http.StatusNotFound, "not_found", "no such API endpoint", nil)
			return
		}
		if peer.name == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "the caller is not a plugin unit", nil)
			return
		}
		id, running := s.Plugins.Identify(peer.name)
		if !running {
			s.audit(r, "plugin_call", "plugin:"+peer.name, map[string]any{"plugin": peer.name, "method": r.Method, "path": r.URL.Path, "result": "not_enabled"})
			writeError(w, http.StatusUnauthorized, "unauthorized", "plugin "+peer.name+" is not enabled", nil)
			return
		}
		detail := map[string]any{"plugin": id.Name, "method": r.Method, "path": r.URL.Path}
		user := "plugin:" + id.Name
		if next == nil || (capability != "" && !id.Can(capability)) {
			need := capability
			if next == nil {
				need = "(not available to plugins)"
			}
			detail["result"] = "denied"
			detail["needs"] = need
			s.audit(r, "plugin_call", user, detail)
			writeError(w, http.StatusForbidden, "scope_denied", fmt.Sprintf("plugin %s may not call %s %s", id.Name, r.Method, r.URL.Path), map[string]any{"needs": need})
			return
		}
		detail["result"] = "allowed"
		s.audit(r, "plugin_call", user, detail)
		ctx := context.WithValue(r.Context(), pluginKey{}, id)
		ctx = context.WithValue(ctx, identityKey{}, auth.Identity{User: user, Role: "plugin", Method: "plugin"})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GET /api/v1/plugin/self → {name, capabilities, settings}: the calling
// plugin's approved capabilities and its settings with defaults filled in.
func (s *Server) pluginSelf(w http.ResponseWriter, r *http.Request) {
	id, _ := pluginFrom(r)
	cfg, err := s.Store.Running()
	if err != nil {
		s.internal(w, "read running config", err)
		return
	}
	settings := map[string]any{}
	for _, p := range cfg.Plugins {
		if p.Name == id.Name {
			settings = p.Settings
		}
	}
	if in, ok := s.Plugins.Installed(id.Name); ok && in.Manifest != nil {
		settings = in.Manifest.Effective(settings)
	}
	if settings == nil {
		settings = map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": id.Name, "capabilities": orEmpty(id.Capabilities), "settings": settings})
}

// POST /api/v1/plugin/notify {title, message, key?} → sends a "plugin"
// event through the admin's notification rules.
func (s *Server) pluginNotify(w http.ResponseWriter, r *http.Request) {
	id, _ := pluginFrom(r)
	var req struct {
		Title   string `json:"title"`
		Message string `json:"message"`
		Key     string `json:"key"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Title, req.Message = strings.TrimSpace(req.Title), strings.TrimSpace(req.Message)
	if req.Title == "" || len(req.Title) > 200 || len(req.Message) > 4000 || len(req.Key) > 200 {
		writeError(w, http.StatusBadRequest, "invalid_request", "want a title (at most 200 characters) and a message (at most 4000)", nil)
		return
	}
	if s.Notify != nil {
		s.Notify.Publish(notify.Event{Kind: "plugin", Key: id.Name + "/" + req.Key, Title: "[" + id.Name + "] " + req.Title,
			Message: req.Message, Time: time.Now(), Fields: map[string]string{"plugin": id.Name}})
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "queued"})
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// GET /api/v1/plugins → {plugins: [plugins.Status]}: installed plugins with
// their manifest, the candidate's settings for them and the unit state.
func (s *Server) listPlugins(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.Store.Candidate()
	if err != nil {
		s.internal(w, "read candidate config", err)
		return
	}
	list := []plugins.Status{}
	if s.Plugins != nil {
		list = append(list, s.Plugins.List(r.Context(), cfg)...)
	}
	writeJSON(w, http.StatusOK, map[string]any{"plugins": list})
}

// editPlugin changes one plugin's entry in the candidate and refuses the
// change when that entry does not validate.
func (s *Server) editPlugin(w http.ResponseWriter, r *http.Request, change func(p *config.Plugin, in *plugins.Installed) bool) {
	if s.Plugins == nil {
		unavailable(w, "plugins")
		return
	}
	name := r.PathValue("name")
	in, ok := s.Plugins.Rescan()[name]
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "plugin "+name+" is not installed", nil)
		return
	}
	cfg, err := s.Store.Candidate()
	if err != nil {
		s.internal(w, "read candidate config", err)
		return
	}
	idx := slices.IndexFunc(cfg.Plugins, func(p config.Plugin) bool { return p.Name == name })
	if idx < 0 {
		cfg.Plugins = append(cfg.Plugins, config.Plugin{Name: name})
		idx = len(cfg.Plugins) - 1
	}
	if !change(&cfg.Plugins[idx], in) {
		return
	}
	prefix := fmt.Sprintf("plugins[%d]", idx)
	var mine []config.ValidationError
	for _, e := range config.ValidateCandidate(cfg) {
		if e.Path == prefix || strings.HasPrefix(e.Path, prefix+".") {
			mine = append(mine, e)
		}
	}
	if len(mine) > 0 {
		writeError(w, http.StatusBadRequest, "validation_failed", "the plugin settings are not valid", mine)
		return
	}
	s.audit(r, "plugin_change", s.user(r), map[string]any{"plugin": name, "enabled": cfg.Plugins[idx].Enabled, "capabilities": cfg.Plugins[idx].Capabilities})
	s.storeCandidate(w, cfg)
}

// POST /api/v1/plugins/{name}/enable {capabilities?} → enables the plugin in
// the candidate with the approved capabilities (default: all it asks for).
func (s *Server) enablePlugin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Capabilities *[]string `json:"capabilities"`
	}
	if r.ContentLength != 0 && !decodeJSON(w, r, &req) {
		return
	}
	s.editPlugin(w, r, func(p *config.Plugin, in *plugins.Installed) bool {
		if in.Problem != "" {
			writeError(w, http.StatusConflict, "plugin_invalid", "plugin "+p.Name+" cannot run: "+in.Problem, nil)
			return false
		}
		p.Enabled = true
		if req.Capabilities != nil {
			p.Capabilities = slices.Clone(*req.Capabilities)
		} else {
			p.Capabilities = slices.Clone(in.Manifest.Capabilities)
		}
		return true
	})
}

// POST /api/v1/plugins/{name}/disable → disables it in the candidate.
func (s *Server) disablePlugin(w http.ResponseWriter, r *http.Request) {
	s.editPlugin(w, r, func(p *config.Plugin, _ *plugins.Installed) bool {
		p.Enabled = false
		return true
	})
}

// PUT /api/v1/plugins/{name}/settings {settings} → checks them against the
// manifest's schema and stores them in the candidate.
func (s *Server) pluginSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Settings map[string]any `json:"settings"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	s.editPlugin(w, r, func(p *config.Plugin, in *plugins.Installed) bool {
		if in.Manifest == nil {
			writeError(w, http.StatusConflict, "plugin_invalid", "plugin "+p.Name+" cannot run: "+in.Problem, nil)
			return false
		}
		if msgs := in.Manifest.CheckSettings(req.Settings); len(msgs) > 0 {
			writeError(w, http.StatusBadRequest, "validation_failed", "the plugin settings are not valid", msgs)
			return false
		}
		p.Settings = req.Settings
		return true
	})
}

// GET /plugin-ui/{name}/{path...} serves a plugin's static pages from its ui/
// directory. The CSP sandbox gives them an opaque origin, so plugin scripts
// cannot use the admin's session or read the main UI; os.Root keeps the
// path (and symlinks) inside ui/.
func (s *Server) pluginUI(w http.ResponseWriter, r *http.Request) {
	if s.Plugins == nil {
		http.NotFound(w, r)
		return
	}
	name := r.PathValue("name")
	in, ok := s.Plugins.Installed(name)
	if !ok {
		in, ok = s.Plugins.Rescan()[name]
	}
	if !ok || in.Problem != "" || in.Manifest == nil || in.Manifest.UI == nil {
		http.NotFound(w, r)
		return
	}
	root, err := os.OpenRoot(filepath.Join(in.Dir, "ui"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()
	path := r.PathValue("path")
	if path == "" || strings.HasSuffix(path, "/") {
		path += "index.html"
	}
	f, err := root.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Security-Policy", "sandbox allow-scripts allow-forms; default-src 'self' 'unsafe-inline' data:; connect-src 'none'; frame-ancestors 'self'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	http.ServeContent(w, r, st.Name(), st.ModTime(), f)
}

// pluginAPI proxies /api/v1/plugins/{name}/api/{path...} to the plugin's
// own API socket (Manager.AdminSocket) for the admin UI. goblind has
// authenticated the admin (read-only admins only get safe methods); the
// plugin learns who asks from X-Goblin-User and X-Goblin-Role and never
// sees cookies, tokens or the CSRF header. Only JSON answers pass, with
// cookies stripped and a locked-down CSP, so a plugin cannot serve pages
// on the admin UI's origin. Changes are audited (plugin_admin_call).
func (s *Server) pluginAPI(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if s.Plugins == nil {
		unavailable(w, "plugins")
		return
	}
	if _, running := s.Plugins.Identify(name); !running {
		writeError(w, http.StatusServiceUnavailable, "plugin_not_running", "plugin "+name+" is not running", nil)
		return
	}
	sock := s.Plugins.AdminSocket(name)
	user := s.user(r)
	role := "admin"
	if id, ok := IdentityFrom(r); ok && id.Role != "" {
		role = id.Role
	}
	if !safeMethod(r.Method) {
		s.audit(r, "plugin_admin_call", user, map[string]any{"plugin": name, "method": r.Method, "path": "/" + r.PathValue("path")})
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme, pr.Out.URL.Host = "http", "plugin"
			pr.Out.URL.Path, pr.Out.URL.RawPath = "/"+pr.In.PathValue("path"), ""
			pr.Out.Host = "plugin"
			for _, h := range []string{"Cookie", "Authorization", CSRFHeader, "X-Goblin-User", "X-Goblin-Role", "Forwarded", "X-Forwarded-For"} {
				pr.Out.Header.Del(h)
			}
			pr.Out.Header.Set("X-Goblin-User", user)
			pr.Out.Header.Set("X-Goblin-Role", role)
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", sock)
			},
			ResponseHeaderTimeout: 30 * time.Second,
		},
		ModifyResponse: func(resp *http.Response) error {
			if ct := resp.Header.Get("Content-Type"); resp.StatusCode != http.StatusNoContent && !isJSON(ct) {
				return fmt.Errorf("plugin %s answered with content type %q, not JSON", name, ct)
			}
			resp.Header.Del("Set-Cookie")
			resp.Header.Set("X-Content-Type-Options", "nosniff")
			resp.Header.Set("Content-Security-Policy", "default-src 'none'; sandbox")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			s.Logger.Warn("plugin api: proxy failed", "plugin", name, "error", err)
			writeError(w, http.StatusBadGateway, "plugin_error", "plugin "+name+" did not answer properly", nil)
		},
	}
	proxy.ServeHTTP(w, r)
}
