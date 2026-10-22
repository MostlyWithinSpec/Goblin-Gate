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

// GET /api/v1/ips/rules → {ruleset: "ET Open"|"ET Pro", last_update?, rules?,
// last_error?} (task 114).
func (s *Server) ipsRules(w http.ResponseWriter, r *http.Request) {
	if s.IPSRules == nil {
		unavailable(w, "the IPS rule updater")
		return
	}
	writeJSON(w, http.StatusOK, s.IPSRules.Status())
}

// GET /api/v1/ups → ups.Status: power state, charge, runtime, load, model,
// the last power event and error (task 112).
func (s *Server) upsStatus(w http.ResponseWriter, r *http.Request) {
	if s.UPS == nil {
		unavailable(w, "UPS monitoring")
		return
	}
	writeJSON(w, http.StatusOK, s.UPS.Status())
}
