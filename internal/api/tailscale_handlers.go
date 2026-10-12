package api

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/tailscale"
)

// TailscalePeer is one row of the status peers.
type TailscalePeer struct {
	Name           string    `json:"name"`
	DNSName        string    `json:"dns_name,omitempty"`
	OS             string    `json:"os,omitempty"`
	IPs            []string  `json:"ips"`
	Online         bool      `json:"online"`
	LastSeen       time.Time `json:"last_seen,omitzero"`
	ExitNode       bool      `json:"exit_node"`        // we use it as exit node
	ExitNodeOption bool      `json:"exit_node_option"` // it offers itself as one
	Routes         []string  `json:"routes,omitempty"` // subnets it serves
}

func (s *Server) tailscaleReady() bool { return s.Tailscale != nil && s.Tailscale.Available() }

// GET /api/v1/vpn/tailscale/status → {available, backend_state, auth_url,
// hostname, ips, advertised_routes, approved_routes, peers}. available is
// false when tailscaled is not running (not installed, or --dev).
func (s *Server) tailscaleStatus(w http.ResponseWriter, r *http.Request) {
	if !s.tailscaleReady() {
		writeJSON(w, http.StatusOK, map[string]any{"available": false})
		return
	}
	st, err := s.Tailscale.Status(r.Context())
	if err != nil {
		s.Logger.Warn("api: tailscale status", "error", err)
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "error": err.Error()})
		return
	}
	out := map[string]any{"available": true, "backend_state": st.BackendState, "auth_url": st.AuthURL, "ips": st.TailscaleIPs}
	if st.Self != nil {
		out["hostname"] = st.Self.HostName
		out["approved_routes"] = st.Self.PrimaryRoutes
	}
	if p, err := s.Tailscale.GetPrefs(r.Context()); err == nil {
		out["advertised_routes"] = p.AdvertiseRoutes
	}
	peers := []TailscalePeer{}
	for _, p := range st.Peer {
		peers = append(peers, TailscalePeer{
			Name: p.HostName, DNSName: strings.TrimSuffix(p.DNSName, "."), OS: p.OS, IPs: p.TailscaleIPs,
			Online: p.Online, LastSeen: p.LastSeen, ExitNode: p.ExitNode, ExitNodeOption: p.ExitNodeOption, Routes: p.PrimaryRoutes,
		})
	}
	sort.Slice(peers, func(i, j int) bool { return peers[i].Name < peers[j].Name })
	out["peers"] = peers
	writeJSON(w, http.StatusOK, out)
}

type tailscaleLoginRequest struct {
	AuthKey string `json:"auth_key,omitempty"`
}

// POST /api/v1/vpn/tailscale/login {auth_key?}: with a key, log in with it
// (the key is not stored); without, start a browser login and return
// {auth_url} for the user to open.
func (s *Server) tailscaleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.tailscaleReady() {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "tailscaled is not running; enable Tailscale and commit first", nil)
		return
	}
	var req tailscaleLoginRequest
	if r.ContentLength != 0 && !decodeJSON(w, r, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if key := strings.TrimSpace(req.AuthKey); key != "" {
		if !strings.HasPrefix(key, "tskey-") {
			writeError(w, http.StatusBadRequest, "invalid_request", "an auth key starts with tskey-", nil)
			return
		}
		if err := s.Tailscale.LoginAuthKey(ctx, key); err != nil {
			writeError(w, http.StatusBadGateway, "login_failed", "tailscaled refused the auth key", err.Error())
			return
		}
		s.audit(r, "tailscale_login", "", map[string]any{"method": "auth_key"})
		writeJSON(w, http.StatusOK, map[string]any{"status": "logging_in"})
		return
	}
	if err := s.Tailscale.LoginInteractive(ctx); err != nil {
		writeError(w, http.StatusBadGateway, "login_failed", "tailscaled could not start a login", err.Error())
		return
	}
	for {
		st, err := s.Tailscale.Status(ctx)
		if err == nil && st.AuthURL != "" {
			s.audit(r, "tailscale_login", "", map[string]any{"method": "browser"})
			writeJSON(w, http.StatusOK, map[string]any{"auth_url": st.AuthURL})
			return
		}
		if err == nil && st.BackendState == "Running" {
			writeJSON(w, http.StatusOK, map[string]any{"status": "running"})
			return
		}
		select {
		case <-ctx.Done():
			writeError(w, http.StatusGatewayTimeout, "login_timeout", "tailscaled did not return a login URL in time", nil)
			return
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// POST /api/v1/vpn/tailscale/logout
func (s *Server) tailscaleLogout(w http.ResponseWriter, r *http.Request) {
	if !s.tailscaleReady() {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "tailscaled is not running", nil)
		return
	}
	if err := s.Tailscale.Logout(r.Context()); err != nil && !errors.Is(err, tailscale.ErrUnavailable) {
		writeError(w, http.StatusBadGateway, "logout_failed", "tailscaled refused to log out", err.Error())
		return
	}
	s.audit(r, "tailscale_logout", "", nil)
	writeJSON(w, http.StatusOK, map[string]any{"status": "logged_out"})
}
