package api

import (
	"net/http"

	"github.com/networkgoblinlabs/goblin-gate/internal/checks"
)

// GET /api/v1/checks → checks.Report: Goblin Checks run against the running
// config and the box's live state (task 108).
func (s *Server) checks(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.Store.Running()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "cannot read the running config", nil)
		return
	}
	var st checks.State
	st.RebootRequired, _ = rebootRequired()
	if s.Updates != nil {
		st.UpdateAvailable = s.Updates.Cached().Available
	}
	if s.GitBackup != nil && cfg.GitBackup.Enabled {
		st.GitLastPush, st.GitLastError = s.GitBackup.PushState()
	}
	dismissed := map[string]bool{}
	if s.Dismissed != nil {
		if dismissed, err = s.Dismissed.Load(); err != nil {
			s.Logger.Warn("goblin checks: reading dismissed findings", "error", err)
		}
	}
	writeJSON(w, http.StatusOK, checks.Run(cfg, st, dismissed))
}

// POST /api/v1/checks/dismiss {key} hides a finding from the list and the
// score until it is restored. Dismissals are box state, not config.
func (s *Server) dismissFinding(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key string `json:"key"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	s.setDismissed(w, r, req.Key, true)
}

// DELETE /api/v1/checks/dismiss/{key} brings a dismissed finding back.
func (s *Server) restoreFinding(w http.ResponseWriter, r *http.Request) {
	s.setDismissed(w, r, r.PathValue("key"), false)
}

func (s *Server) setDismissed(w http.ResponseWriter, r *http.Request, key string, on bool) {
	if s.Dismissed == nil {
		unavailable(w, "Goblin Checks dismissals")
		return
	}
	if !checks.ValidKey(key) {
		writeError(w, http.StatusBadRequest, "invalid_request", "not a finding key", nil)
		return
	}
	if err := s.Dismissed.Set(key, on); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error(), nil)
		return
	}
	action := "checks.dismiss"
	if !on {
		action = "checks.restore"
	}
	s.audit(r, action, "", map[string]any{"key": key})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
