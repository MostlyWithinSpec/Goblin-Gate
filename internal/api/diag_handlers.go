package api

import (
	"context"
	"net/http"

	"github.com/networkgoblinlabs/goblin-gate/internal/diag"
	"github.com/networkgoblinlabs/goblin-gate/internal/render"
)

// Diagnostics (System → Diagnostics). They are POSTs, so read-only users
// cannot run them (the auth middleware); at most diagSlots run at once.

const diagSlots = 2

func (s *Server) diagRunner() diag.Runner {
	if s.DiagRunner != nil {
		return s.DiagRunner
	}
	return diag.ExecRunner
}

// diagStart takes a slot, or answers 429 when all are busy.
func (s *Server) diagStart(w http.ResponseWriter) (func(), bool) {
	s.diagOnce.Do(func() { s.diagSem = make(chan struct{}, diagSlots) })
	select {
	case s.diagSem <- struct{}{}:
		return func() { <-s.diagSem }, true
	default:
		writeError(w, http.StatusTooManyRequests, "busy", "another diagnostic is running; try again in a moment", nil)
		return nil, false
	}
}

// POST /api/v1/diag/ping {host, count?, interface?}
func (s *Server) diagPing(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host      string `json:"host"`
		Count     int    `json:"count"`
		Interface string `json:"interface"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	dev := ""
	if req.Interface != "" {
		cfg, err := s.Store.Running()
		if err != nil {
			s.internal(w, "read running config", err)
			return
		}
		d, ok := render.Devices(cfg)[req.Interface]
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_request", "unknown interface "+req.Interface, nil)
			return
		}
		dev = d
	}
	done, ok := s.diagStart(w)
	if !ok {
		return
	}
	defer done()
	ctx, cancel := context.WithTimeout(r.Context(), diag.Timeout)
	defer cancel()
	res, err := diag.Ping(ctx, s.diagRunner(), req.Host, req.Count, dev)
	if err != nil {
		writeError(w, http.StatusBadRequest, "diag_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// POST /api/v1/diag/traceroute {host, max_hops?}
func (s *Server) diagTraceroute(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host    string `json:"host"`
		MaxHops int    `json:"max_hops"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	done, ok := s.diagStart(w)
	if !ok {
		return
	}
	defer done()
	ctx, cancel := context.WithTimeout(r.Context(), diag.Timeout)
	defer cancel()
	res, err := diag.Traceroute(ctx, s.diagRunner(), req.Host, req.MaxHops)
	if err != nil {
		writeError(w, http.StatusBadRequest, "diag_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// POST /api/v1/diag/dns {name, type?, server?}
func (s *Server) diagDNS(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name   string `json:"name"`
		Type   string `json:"type"`
		Server string `json:"server"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	done, ok := s.diagStart(w)
	if !ok {
		return
	}
	defer done()
	lookup := diag.Lookup
	if s.DiagLookup != nil {
		lookup = s.DiagLookup
	}
	res, err := lookup(r.Context(), req.Name, req.Type, req.Server)
	if err != nil {
		writeError(w, http.StatusBadRequest, "diag_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
