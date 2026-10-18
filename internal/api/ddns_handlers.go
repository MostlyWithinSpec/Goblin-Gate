package api

import (
	"net/http"
	"strings"
)

func (s *Server) ddnsReady(w http.ResponseWriter) bool {
	if s.DDNS == nil {
		writeError(w, http.StatusServiceUnavailable, "ddns_unavailable", "the DDNS manager is not running", nil)
		return false
	}
	return true
}

// syncDDNS loads the current DDNS entries from the running config so new
// entries are visible without waiting for the next tick.
func (s *Server) syncDDNS() {
	cfg, err := s.Store.Running()
	if err != nil {
		return
	}
	s.DDNS.Sync(cfg.DDNS)
}

// GET /api/v1/ddns → {entries: [ddns.Status]}
func (s *Server) ddnsStatus(w http.ResponseWriter, r *http.Request) {
	if !s.ddnsReady(w) {
		return
	}
	s.syncDDNS()
	writeJSON(w, http.StatusOK, map[string]any{"entries": s.DDNS.Status()})
}

// POST /api/v1/ddns/{name}/update runs one update now and returns the status.
// A failed update answers 502 with the status in details; an unknown name
// answers 404.
func (s *Server) ddnsUpdateNow(w http.ResponseWriter, r *http.Request) {
	if !s.ddnsReady(w) {
		return
	}
	s.syncDDNS()
	st, err := s.DDNS.UpdateNow(r.Context(), r.PathValue("name"))
	switch {
	case err != nil && strings.Contains(err.Error(), "no such entry"):
		writeError(w, http.StatusNotFound, "ddns_not_found", "no such DDNS entry in the running config", nil)
	case err != nil:
		writeError(w, http.StatusBadGateway, "update_failed", err.Error(), st)
	default:
		writeJSON(w, http.StatusOK, st)
	}
}
