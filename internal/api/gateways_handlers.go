package api

import (
	"net/http"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/gateways"
	"github.com/networkgoblinlabs/goblin-gate/internal/wireguard"
)

// GET /api/v1/gateways/status → gateways.Report: each gateway's state
// (up/down/unknown, loss, latency, since, reason) and which gateways every
// routing target and the default route use now (task 049).
func (s *Server) gatewayStatus(w http.ResponseWriter, r *http.Request) {
	if s.Gateways == nil {
		writeJSON(w, http.StatusOK, gateways.Report{Gateways: []gateways.Status{}, Targets: []gateways.TargetStatus{},
			Default: gateways.TargetStatus{Active: []string{}, Mode: "default"}})
		return
	}
	writeJSON(w, http.StatusOK, s.Gateways.Report())
}

// wireguardClient finds a client in the candidate config (keys may be set
// before the first commit).
func (s *Server) wireguardClient(w http.ResponseWriter, r *http.Request) (config.WireGuardClient, bool) {
	if s.WireGuardKeyDir == "" {
		unavailable(w, "wireguard keys")
		return config.WireGuardClient{}, false
	}
	cfg, err := s.Store.Candidate()
	if err != nil {
		s.internal(w, "read candidate", err)
		return config.WireGuardClient{}, false
	}
	name := r.PathValue("name")
	for _, c := range cfg.VPN.WireGuardClients {
		if c.Name == name {
			return c, true
		}
	}
	writeError(w, http.StatusNotFound, "not_found", "no WireGuard client "+name, nil)
	return config.WireGuardClient{}, false
}

// GET /api/v1/vpn/wireguard-clients/{name}/public-key → {public_key}: the
// key to register with the VPN provider (generated on first use).
func (s *Server) wireguardClientPublicKey(w http.ResponseWriter, r *http.Request) {
	c, ok := s.wireguardClient(w, r)
	if !ok {
		return
	}
	pub, err := wireguard.EnsureKey(s.WireGuardKeyDir, config.ClientKeyName(c.Name))
	if err != nil {
		s.internal(w, "wireguard client key", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"public_key": pub.String()})
}

// PUT /api/v1/vpn/wireguard-clients/{name}/private-key {private_key} →
// {public_key}: stores the private key from the provider's config file
// (never part of the config or the API's answers). Takes effect on the next
// commit.
func (s *Server) wireguardClientSetKey(w http.ResponseWriter, r *http.Request) {
	c, ok := s.wireguardClient(w, r)
	if !ok {
		return
	}
	var req struct {
		PrivateKey string `json:"private_key"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	pub, err := wireguard.SetKey(s.WireGuardKeyDir, config.ClientKeyName(c.Name), req.PrivateKey)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_key", "want the base64 private key from the provider's config", nil)
		return
	}
	s.audit(r, "wireguard_client_key", s.user(r), map[string]any{"client": c.Name})
	writeJSON(w, http.StatusOK, map[string]string{"public_key": pub.String()})
}
