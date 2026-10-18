package api

import (
	"net/http"
	"net/netip"
	"strconv"

	"github.com/networkgoblinlabs/goblin-gate/internal/flows"
)

// Live flows and per-device bandwidth (task 078).

func (s *Server) flowsReady(w http.ResponseWriter) bool {
	if s.Flows == nil {
		writeError(w, http.StatusServiceUnavailable, "flows_unavailable", "live flows are read on the firewall itself", nil)
		return false
	}
	return true
}

// GET /api/v1/flows?ip=&limit= → {flows, total}: the latest sample of the
// connection table, fastest first (limit default 200, at most 2000).
func (s *Server) listFlows(w http.ResponseWriter, r *http.Request) {
	if !s.flowsReady(w) {
		return
	}
	var ip netip.Addr
	if q := r.URL.Query().Get("ip"); q != "" {
		var err error
		if ip, err = netip.ParseAddr(q); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "ip must be an IP address", nil)
			return
		}
	}
	limit := 200
	if q := r.URL.Query().Get("limit"); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 1 || n > 2000 {
			writeError(w, http.StatusBadRequest, "invalid_request", "limit must be 1-2000", nil)
			return
		}
		limit = n
	}
	list, total := s.Flows.Flows(ip, limit)
	if list == nil {
		list = []flows.Flow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"flows": list, "total": total})
}

// GET /api/v1/flows/devices → {devices}: bandwidth per local address.
func (s *Server) flowDevices(w http.ResponseWriter, r *http.Request) {
	if !s.flowsReady(w) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": s.Flows.Devices()})
}
