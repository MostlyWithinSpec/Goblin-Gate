package api

import (
	"net/http"

	"github.com/networkgoblinlabs/goblin-gate/internal/frr"
)

// RoutingStatus is GET /api/v1/routing/status: whether BGP and OSPF are on
// in the running config, and what FRR reports.
type RoutingStatus struct {
	BGPEnabled  bool   `json:"bgp_enabled"`
	OSPFEnabled bool   `json:"ospf_enabled"`
	RouterID    string `json:"router_id,omitempty"`
	frr.Status
}

func (s *Server) routingStatus(w http.ResponseWriter, r *http.Request) {
	if s.FRR == nil {
		unavailable(w, "dynamic routing status")
		return
	}
	cfg, err := s.Store.Running()
	if err != nil {
		s.internal(w, "read running config", err)
		return
	}
	out := RoutingStatus{BGPEnabled: cfg.Routing.BGP.Enabled, OSPFEnabled: cfg.Routing.OSPF.Enabled,
		Status: frr.Status{BGP: []frr.Neighbor{}, OSPF: []frr.OSPFNeighbor{}, Routes: []frr.Route{}}}
	if !out.BGPEnabled && !out.OSPFEnabled {
		writeJSON(w, http.StatusOK, out) // FRR is stopped: nothing to ask
		return
	}
	if id := cfg.RoutingRouterID(); id.IsValid() {
		out.RouterID = id.String()
	}
	if out.Status, err = frr.Read(r.Context(), s.FRR); err != nil {
		s.internal(w, "read FRR status", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
