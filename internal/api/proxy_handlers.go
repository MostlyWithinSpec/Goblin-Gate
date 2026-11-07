package api

import (
	"net/http"

	"github.com/networkgoblinlabs/goblin-gate/internal/reverseproxy"
)

// GET /api/v1/reverse-proxy → {enabled, state, hosts: [{name, enabled,
// upstream, domains: [{domain, certificate?: {issuer, not_before, not_after,
// obtained, staging?}, error?: {time, message}}]}]}: whether Caddy runs and
// each published domain's certificate, per the running config.
func (s *Server) reverseProxyStatus(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.Store.Running()
	if err != nil {
		s.internal(w, "read running config", err)
		return
	}
	rd := s.ReverseProxy
	if rd == nil {
		rd = &reverseproxy.Reader{}
	}
	writeJSON(w, http.StatusOK, rd.Status(r.Context(), cfg))
}
