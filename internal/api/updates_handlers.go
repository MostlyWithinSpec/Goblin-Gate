package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/updates"
)

// GET /api/v1/system/updates[?refresh=1] → updates.Status: installed and
// available goblin-gate versions, pending Debian updates, the last update.
// Checks are cached for 10 minutes unless refresh=1.
func (s *Server) updateStatus(w http.ResponseWriter, r *http.Request) {
	if s.Updates == nil {
		unavailable(w, "updates")
		return
	}
	maxAge := 10 * time.Minute
	if r.URL.Query().Get("refresh") == "1" {
		maxAge = 0
	}
	writeJSON(w, http.StatusOK, s.Updates.Check(r.Context(), maxAge))
}

// POST /api/v1/system/updates/apply {version?, include_os?} → 202: the update
// runs in the background (goblind restarts during it); poll GET
// /api/v1/system/updates and read "last" for progress and the result.
func (s *Server) applyUpdate(w http.ResponseWriter, r *http.Request) {
	if s.Updates == nil {
		unavailable(w, "updates")
		return
	}
	var req struct {
		Version   string `json:"version"`
		IncludeOS bool   `json:"include_os"`
	}
	if r.ContentLength != 0 && !decodeJSON(w, r, &req) {
		return
	}
	err := s.Updates.Start(r.Context(), req.Version, req.IncludeOS)
	switch {
	case errors.Is(err, updates.ErrRunning):
		writeError(w, http.StatusConflict, "update_running", "an update is already running", nil)
	case err != nil && strings.Contains(err.Error(), "up to date"):
		writeError(w, http.StatusBadRequest, "up_to_date", "Goblin Gate is already up to date", nil)
	case err != nil:
		s.internal(w, "start update", err)
	default:
		s.audit(r, "update_started", s.user(r), map[string]any{"version": req.Version, "include_os": req.IncludeOS})
		writeJSON(w, http.StatusAccepted, map[string]any{"status": "started"})
	}
}
