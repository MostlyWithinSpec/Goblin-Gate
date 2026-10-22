package api

import (
	"net/http"
	"time"
)

// rebootDelay lets the answer reach the browser before the box goes down.
var rebootDelay = 2 * time.Second

// POST /api/v1/system/reboot restarts the box (full admins only). It answers
// first and restarts a moment later; the UI asks for confirmation.
func (s *Server) reboot(w http.ResponseWriter, r *http.Request) {
	if s.Reboot == nil {
		unavailable(w, "restarting the box")
		return
	}
	s.audit(r, "system.reboot", "", nil)
	s.Logger.Warn("restart requested from the web UI or API", "user", s.user(r))
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "restarting"})
	go func() {
		time.Sleep(rebootDelay)
		if err := s.Reboot(); err != nil {
			s.Logger.Error("restart failed", "error", err)
		}
	}()
}
