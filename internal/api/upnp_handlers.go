package api

import (
	"net/http"

	renderupnp "github.com/networkgoblinlabs/goblin-gate/internal/render/upnp"
	"github.com/networkgoblinlabs/goblin-gate/internal/upnp"
)

// GET /api/v1/upnp/mappings → {enabled, mappings: [{protocol,
// external_port, internal_ip, internal_port, description?, expires?}]}:
// the port mappings LAN clients opened with UPnP / NAT-PMP / PCP (task 059).
func (s *Server) upnpMappings(w http.ResponseWriter, r *http.Request) {
	enabled := false
	if cfg, err := s.Store.Running(); err == nil {
		enabled = cfg.UPnP.Enabled
	}
	path := s.UPnPLeaseFile
	if path == "" {
		path = renderupnp.LeaseFile
	}
	ms, err := upnp.Read(path)
	if err != nil {
		s.internal(w, "read upnp leases", err)
		return
	}
	if ms == nil {
		ms = []upnp.Mapping{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": enabled, "mappings": ms})
}
