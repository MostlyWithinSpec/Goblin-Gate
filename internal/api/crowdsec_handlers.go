package api

import "net/http"

// GET /api/v1/crowdsec → crowdsec.Status: enabled, connected, last pull,
// active decisions, dropped packets, last error (task 104).
func (s *Server) crowdSecStatus(w http.ResponseWriter, r *http.Request) {
	if s.CrowdSec == nil {
		unavailable(w, "CrowdSec")
		return
	}
	writeJSON(w, http.StatusOK, s.CrowdSec.Status(r.Context()))
}
