package api

import (
	"context"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/qr"
	"github.com/networkgoblinlabs/goblin-gate/internal/wireguard"
)

// WireGuardStats returns live peer stats of a device (`wg show dump`).
func WireGuardStats(ctx context.Context, dev string) (map[string]wireguard.PeerStat, error) {
	out, err := exec.CommandContext(ctx, "wg", "show", dev, "dump").Output()
	if err != nil {
		return nil, err
	}
	return wireguard.ParseDump(string(out)), nil
}

type wgPeerView struct {
	config.WireGuardPeer
	wireguard.PeerStat
}

type wgView struct {
	Name       string       `json:"name"`
	Enabled    bool         `json:"enabled"`
	PublicKey  string       `json:"public_key"`
	ListenPort int          `json:"listen_port"`
	Address    string       `json:"address"`
	Endpoint   string       `json:"endpoint,omitempty"`
	Peers      []wgPeerView `json:"peers"`
}

// GET /api/v1/vpn/wireguard → {interfaces: [...]}: the candidate's
// WireGuard servers with their public keys (keys are created on first
// sight) and live peer stats where the device is up.
func (s *Server) wireguardList(w http.ResponseWriter, r *http.Request) {
	if s.WireGuardKeyDir == "" {
		unavailable(w, "WireGuard")
		return
	}
	cfg, err := s.Store.Candidate()
	if err != nil {
		s.internal(w, "read candidate config", err)
		return
	}
	out := []wgView{}
	for _, wg := range cfg.VPN.WireGuard {
		pub, err := wireguard.EnsureKey(s.WireGuardKeyDir, wg.Name)
		if err != nil {
			s.internal(w, "create WireGuard key", err)
			return
		}
		port := wg.ListenPort
		if port == 0 {
			port = config.DefaultWireGuardPort
		}
		v := wgView{Name: wg.Name, Enabled: wg.Enabled, PublicKey: pub.String(), ListenPort: port, Address: wg.Address.String(), Endpoint: wg.Endpoint, Peers: []wgPeerView{}}
		var stats map[string]wireguard.PeerStat
		if s.WireGuardStats != nil && wg.Enabled {
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			stats, _ = s.WireGuardStats(ctx, wg.Name)
			cancel()
		}
		for _, p := range wg.Peers {
			v.Peers = append(v.Peers, wgPeerView{WireGuardPeer: p, PeerStat: stats[p.PublicKey]})
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"interfaces": out})
}

type wgPeerRequest struct {
	Name       string `json:"name"`
	FullTunnel bool   `json:"full_tunnel,omitempty"`
	Keepalive  int    `json:"keepalive,omitempty"`
}

// POST /api/v1/vpn/wireguard/{name}/peers {name, full_tunnel?} adds a peer
// to the candidate (commit to activate) and returns {peer, config, qr_svg}
// once: the client's private key is in the config and nowhere else.
func (s *Server) wireguardAddPeer(w http.ResponseWriter, r *http.Request) {
	if s.WireGuardKeyDir == "" {
		unavailable(w, "WireGuard")
		return
	}
	var req wgPeerRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	s.setupMu.Lock() // one candidate edit at a time
	defer s.setupMu.Unlock()
	cfg, err := s.Store.Candidate()
	if err != nil {
		s.internal(w, "read candidate config", err)
		return
	}
	idx := -1
	for i, wg := range cfg.VPN.WireGuard {
		if wg.Name == r.PathValue("name") {
			idx = i
		}
	}
	if idx < 0 {
		writeError(w, http.StatusNotFound, "not_found", "no such WireGuard interface in the candidate config", nil)
		return
	}
	wg := &cfg.VPN.WireGuard[idx]
	addr, err := wireguard.NextAddress(*wg)
	if err != nil {
		writeError(w, http.StatusConflict, "no_address", err.Error(), nil)
		return
	}
	clientKey, err := wireguard.GenerateKey()
	if err != nil {
		s.internal(w, "generate key", err)
		return
	}
	serverPub, err := wireguard.EnsureKey(s.WireGuardKeyDir, wg.Name)
	if err != nil {
		s.internal(w, "create WireGuard key", err)
		return
	}
	peer := config.WireGuardPeer{Name: req.Name, PublicKey: clientKey.Public().String(), Address: addr, Keepalive: req.Keepalive}
	wg.Peers = append(wg.Peers, peer)
	for _, e := range config.Validate(cfg) {
		if strings.HasPrefix(e.Path, "vpn.wireguard") {
			writeError(w, http.StatusUnprocessableEntity, "validation_failed", "the peer is not valid", []config.ValidationError{e})
			return
		}
	}
	if err := s.Store.SetCandidate(cfg); err != nil {
		s.internal(w, "store candidate", err)
		return
	}
	text := wireguard.ClientConfig(cfg, *wg, peer, clientKey, serverPub, wireguard.ClientOptions{FullTunnel: req.FullTunnel})
	resp := map[string]any{"peer": peer, "config": text}
	if code, err := qr.Encode([]byte(text)); err == nil {
		resp["qr_svg"] = code.SVG(4)
	}
	s.audit(r, "wireguard_peer_added", "", map[string]any{"interface": wg.Name, "peer": peer.Name})
	writeJSON(w, http.StatusCreated, resp)
}
