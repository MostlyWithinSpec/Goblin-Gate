package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/capture"
	"github.com/networkgoblinlabs/goblin-gate/internal/render"
)

// Packet capture (task 076). Packets can hold anything that crosses the
// box, so every capture endpoint is for admins only, reading included.

// captureAdmin allows admins (and dev mode without authentication).
func (s *Server) captureAdmin(w http.ResponseWriter, r *http.Request) bool {
	if s.Capture == nil {
		writeError(w, http.StatusServiceUnavailable, "capture_unavailable", "packet capture runs on the firewall itself", nil)
		return false
	}
	if s.Authn == nil {
		return true
	}
	if id, _ := IdentityFrom(r); !id.CanWrite() {
		writeError(w, http.StatusForbidden, "forbidden", "admins only", nil)
		return false
	}
	return true
}

func (s *Server) captureError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, capture.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error(), nil)
	case errors.Is(err, capture.ErrBusy):
		writeError(w, http.StatusConflict, "busy", err.Error(), nil)
	default:
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
	}
}

// POST /api/v1/capture {interface, host?, port?, protocol?, max_packets?,
// max_seconds?, snaplen?} → 201 Status.
func (s *Server) startCapture(w http.ResponseWriter, r *http.Request) {
	if !s.captureAdmin(w, r) {
		return
	}
	var req struct {
		Interface  string `json:"interface"`
		Host       string `json:"host"`
		Port       int    `json:"port"`
		Protocol   string `json:"protocol"`
		MaxPackets int    `json:"max_packets"`
		MaxSeconds int    `json:"max_seconds"`
		Snaplen    int    `json:"snaplen"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	cfg, err := s.Store.Running()
	if err != nil {
		s.internal(w, "read running config", err)
		return
	}
	dev, ok := render.Devices(cfg)[req.Interface]
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("unknown interface %q", req.Interface), nil)
		return
	}
	creq := capture.Request{Interface: req.Interface, Device: dev, Port: req.Port, Protocol: req.Protocol,
		MaxPackets: req.MaxPackets, MaxSeconds: req.MaxSeconds, Snaplen: req.Snaplen}
	if req.Host != "" {
		if creq.Host, err = netip.ParseAddr(req.Host); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "host must be an IP address", nil)
			return
		}
	}
	st, err := s.Capture.Start(creq)
	if err != nil {
		s.captureError(w, err)
		return
	}
	s.audit(r, "capture_start", "", map[string]any{"id": st.ID, "interface": st.Interface, "filter": st.Filter})
	writeJSON(w, http.StatusCreated, st)
}

// GET /api/v1/capture/{id} → Status
func (s *Server) captureStatus(w http.ResponseWriter, r *http.Request) {
	if !s.captureAdmin(w, r) {
		return
	}
	st, err := s.Capture.Status(r.PathValue("id"))
	if err != nil {
		s.captureError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// GET /api/v1/capture/{id}/pcap → the pcap file
func (s *Server) capturePcap(w http.ResponseWriter, r *http.Request) {
	if !s.captureAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	path, err := s.Capture.File(id)
	if err != nil {
		s.captureError(w, err)
		return
	}
	s.audit(r, "capture_download", "", map[string]any{"id": id})
	w.Header().Set("Content-Type", "application/vnd.tcpdump.pcap")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="goblin-capture-%s.pcap"`, id))
	http.ServeFile(w, r, path)
}

// DELETE /api/v1/capture/{id} → 204: stops a running capture (its file is
// kept for download), deletes a finished one.
func (s *Server) stopCapture(w http.ResponseWriter, r *http.Request) {
	if !s.captureAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	if err := s.Capture.Stop(id); err != nil {
		s.captureError(w, err)
		return
	}
	s.audit(r, "capture_stop", "", map[string]any{"id": id})
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/v1/capture/{id}/live → Server-Sent Events: "packet" events with a
// Packet, "dropped" with {count} when packets were left out (at most 50 a
// second are sent), "done" at the end. A finished capture gets "done" at once.
func (s *Server) captureLive(w http.ResponseWriter, r *http.Request) {
	if !s.captureAdmin(w, r) {
		return
	}
	id := r.PathValue("id")
	events, cancel := s.Capture.Subscribe()
	defer cancel()
	st, err := s.Capture.Status(id)
	if err != nil {
		s.captureError(w, err)
		return
	}
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	send := func(event string, v any) {
		data, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		rc.Flush()
	}
	if st.State != "running" {
		send("done", st)
		return
	}
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n")
			rc.Flush()
		case ev := <-events:
			switch {
			case ev.Packet != nil:
				send("packet", ev.Packet)
			case ev.Dropped > 0:
				send("dropped", map[string]int{"count": ev.Dropped})
			case ev.Done:
				st, _ := s.Capture.Status(id)
				send("done", st)
				return
			}
		}
	}
}
