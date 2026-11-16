package api

import (
	"net/http"
)

// GET /api/v1/flow-export → flowexport.Status: what the exporter sends and how
// much of it has actually left the box (task 144).
//
// The exporter is the source of truth: goblind calls Configure with the running
// section at start and on every commit, so a commit that changed flow_export is
// already reflected here. Reading the stored config instead would report a
// candidate edit no commit has applied yet.
func (s *Server) flowExportStatus(w http.ResponseWriter, r *http.Request) {
	if s.FlowExport == nil {
		writeError(w, http.StatusServiceUnavailable, "flow_export_unavailable",
			"flow export is not running", nil)
		return
	}
	writeJSON(w, http.StatusOK, s.FlowExport.Status())
}
