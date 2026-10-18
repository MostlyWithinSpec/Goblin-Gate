package api

import (
	"net/http"
)

// GET /api/v1/log-export → {entries: [logexport.Status]}
func (s *Server) logExportStatus(w http.ResponseWriter, r *http.Request) {
	if s.LogExport == nil {
		writeError(w, http.StatusServiceUnavailable, "log_export_unavailable",
			"log export is not running", nil)
		return
	}
	cfg, err := s.Store.Running()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_error", err.Error(), nil)
		return
	}
	s.LogExport.Sync(r.Context(), cfg.LogExport)
	writeJSON(w, http.StatusOK, map[string]any{"entries": s.LogExport.Status()})
}
