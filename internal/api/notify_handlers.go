package api

import (
	"errors"
	"net/http"

	"github.com/networkgoblinlabs/goblin-gate/internal/notify"
)

// POST /api/v1/notifications/test {channel} sends a test message to one
// channel of the running config now (disabled channels too, rules and rate
// limits do not apply) → {status: "sent"}, or 502 with why it failed.
func (s *Server) notifyTest(w http.ResponseWriter, r *http.Request) {
	if s.Notify == nil {
		unavailable(w, "notifications")
		return
	}
	var req struct {
		Channel string `json:"channel"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	err := s.Notify.Test(r.Context(), req.Channel)
	switch {
	case errors.Is(err, notify.ErrUnknownChannel):
		writeError(w, http.StatusNotFound, "unknown_channel", "the running config has no channel "+req.Channel+" (commit it first)", nil)
	case err != nil:
		writeError(w, http.StatusBadGateway, "delivery_failed", "the test message could not be delivered", err.Error())
	default:
		writeJSON(w, http.StatusOK, map[string]any{"status": "sent"})
	}
}
