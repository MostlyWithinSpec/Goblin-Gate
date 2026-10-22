package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"time"
)

// eventHeartbeat is how often an idle event stream sends a comment line, so
// proxies and clients see it is alive.
var eventHeartbeat = 30 * time.Second

// maxEventStreams caps the open event streams (each holds a goroutine and a
// connection); more get 503.
const maxEventStreams = 32

var openEventStreams atomic.Int32

// eventKindRe: the kinds the stream writes into its "event:" lines.
var eventKindRe = regexp.MustCompile(`^[a-z0-9_]{1,40}$`)

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
	if openEventStreams.Add(1) > maxEventStreams {
		openEventStreams.Add(-1)
		writeError(w, http.StatusServiceUnavailable, "too_many_streams", "too many open event streams", nil)
		return
	}
	defer openEventStreams.Add(-1)
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
			// a revoked or expired token, or an ended session, ends the stream
			if !s.credentialStillValid(r) {
				return
			}
			fmt.Fprint(w, ": heartbeat\n\n")
			rc.Flush()
		case ev := <-events:
			if types != nil && !slices.Contains(types, ev.Kind) {
				continue
			}
			data, err := json.Marshal(ev)
			if err != nil || !eventKindRe.MatchString(ev.Kind) {
				continue // a kind that could break the frame is never written
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Kind, data)
			rc.Flush()
		}
	}
}

// credentialStillValid re-checks the credential a long-lived request came
// with: the token or session still exists and has not expired, its user still
// exists, and a token is still used from an allowed address.
func (s *Server) credentialStillValid(r *http.Request) bool {
	a := s.Authn
	if a == nil || r.Context().Value(socketKey{}) == true {
		return true
	}
	if h := r.Header.Get("Authorization"); h != "" {
		bearer, ok := cutBearer(h)
		ti, valid := a.Tokens.Verify(bearer)
		if !ok || !valid || s.currentRole(ti.User) == "" {
			return false
		}
		ip, err := netip.ParseAddr(clientAddr(r))
		return err == nil && ti.AllowsAddr(ip)
	}
	c, err := r.Cookie(SessionCookie)
	if err != nil || !a.Sessions.Valid(c.Value) {
		return false
	}
	id, _ := IdentityFrom(r)
	return s.currentRole(id.User) != ""
}
