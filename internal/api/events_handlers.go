package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

// eventHeartbeat is how often an idle event stream sends a comment line, so
// proxies and clients see it is alive.
var eventHeartbeat = 30 * time.Second

// GET /api/v1/events?types=a,b → Server-Sent Events, one per published
// event (task 102): "event: <kind>" and "data: {event, key, title, message,
// time, fields}". Kinds are config.NotifyEvents plus "login" and
// "rollback". Events never carry secrets.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	if s.Notify == nil {
		unavailable(w, "event stream")
		return
	}
	var types []string
	if t := r.URL.Query().Get("types"); t != "" {
		types = strings.Split(t, ",")
	}
	events, cancel := s.Notify.Subscribe()
	defer cancel()
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{}) // the server's write timeout would end the stream
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": connected\n\n")
	rc.Flush()
	beat := time.NewTicker(eventHeartbeat)
	defer beat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-beat.C:
			fmt.Fprint(w, ": heartbeat\n\n")
			rc.Flush()
		case ev := <-events:
			if types != nil && !slices.Contains(types, ev.Kind) {
				continue
			}
			data, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Kind, data)
			rc.Flush()
		}
	}
}
