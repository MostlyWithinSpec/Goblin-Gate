package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/portal"
)

// Captive portal: the let-in guests and the voucher batches. The settings
// are config (captive_portals); the preview renders the candidate's page.

// PortalClient is one let-in guest.
type PortalClient struct {
	portal.Session
	SecondsLeft int `json:"seconds_left"`
}

// VoucherCode is a code with its state.
type VoucherCode struct {
	portal.Code
	State string `json:"state"` // unused | in_use | used_up | expired | revoked
}

// VoucherBatch is a batch as the API shows it.
type VoucherBatch struct {
	ID        string         `json:"id"`
	Note      string         `json:"note,omitempty"`
	Interface string         `json:"interface,omitempty"`
	Minutes   int            `json:"minutes"`
	Uses      int            `json:"uses"`
	Created   time.Time      `json:"created"`
	Expires   time.Time      `json:"expires,omitzero"`
	Revoked   bool           `json:"revoked,omitempty"`
	Counts    map[string]int `json:"counts"`
	Codes     []VoucherCode  `json:"codes"`
}

// voucherRequest makes a batch: count codes (1-500), minutes per use
// (1-43200), uses per code (1-100, 0 = 1), valid_days until the codes stop
// working (0 = no end), an optional note and interface ("" = any portal).
type voucherRequest struct {
	Count     int    `json:"count"`
	Minutes   int    `json:"minutes"`
	Uses      int    `json:"uses,omitempty"`
	ValidDays int    `json:"valid_days,omitempty"`
	Note      string `json:"note,omitempty"`
	Interface string `json:"interface,omitempty"`
}

// voucherRevokeRequest: code revokes one code; empty revokes the batch.
type voucherRevokeRequest struct {
	Code string `json:"code,omitempty"`
}

func (s *Server) portalReady(w http.ResponseWriter) bool {
	if s.Portal == nil {
		unavailable(w, "Captive portal")
		return false
	}
	return true
}

func (s *Server) batchView(b portal.Batch) VoucherBatch {
	now := s.now()
	v := VoucherBatch{ID: b.ID, Note: b.Note, Interface: b.Interface, Minutes: b.Minutes, Uses: b.Uses,
		Created: b.Created, Expires: b.Expires, Revoked: b.Revoked, Counts: map[string]int{}, Codes: []VoucherCode{}}
	for _, c := range b.Codes {
		st := b.State(c, now)
		v.Counts[st]++
		v.Codes = append(v.Codes, VoucherCode{Code: c, State: st})
	}
	return v
}

func (s *Server) portalClients(w http.ResponseWriter, r *http.Request) {
	if !s.portalReady(w) {
		return
	}
	now := s.now()
	out := []PortalClient{}
	for _, c := range s.Portal.Clients() {
		out = append(out, PortalClient{Session: c, SecondsLeft: max(0, int(c.Expires.Sub(now).Seconds()))})
	}
	writeJSON(w, http.StatusOK, map[string]any{"clients": out})
}

func (s *Server) portalKick(w http.ResponseWriter, r *http.Request) {
	if !s.portalReady(w) {
		return
	}
	c, err := s.Portal.Kick(r.PathValue("mac"))
	if errors.Is(err, portal.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "no let-in client with that MAC address", nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error(), nil)
		return
	}
	s.audit(r, "portal_client_removed", "", map[string]any{"interface": c.Interface, "mac": c.MAC, "ip": c.IP})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) portalVouchers(w http.ResponseWriter, r *http.Request) {
	if !s.portalReady(w) {
		return
	}
	out := []VoucherBatch{}
	for _, b := range s.Portal.Batches() {
		out = append(out, s.batchView(b))
	}
	writeJSON(w, http.StatusOK, map[string]any{"batches": out})
}

func (s *Server) portalVoucherCreate(w http.ResponseWriter, r *http.Request) {
	if !s.portalReady(w) {
		return
	}
	var req voucherRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Interface != "" {
		cfg, err := s.Store.Candidate()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", err.Error(), nil)
			return
		}
		found := false
		for _, p := range cfg.CaptivePortals {
			found = found || p.Interface == req.Interface
		}
		if !found {
			writeError(w, http.StatusBadRequest, "validation_failed", "no portal on "+req.Interface, nil)
			return
		}
	}
	b, err := s.Portal.NewBatch(req.Count, req.Minutes, req.Uses, req.ValidDays, req.Note, req.Interface)
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", err.Error(), nil)
		return
	}
	s.audit(r, "portal_vouchers_created", "", map[string]any{"batch": b.ID, "count": len(b.Codes), "minutes": b.Minutes, "uses": b.Uses, "interface": b.Interface})
	writeJSON(w, http.StatusCreated, s.batchView(b))
}

func (s *Server) portalVoucherRevoke(w http.ResponseWriter, r *http.Request) {
	if !s.portalReady(w) {
		return
	}
	var req voucherRevokeRequest
	if r.ContentLength != 0 && !decodeJSON(w, r, &req) {
		return
	}
	id := r.PathValue("id")
	if err := s.Portal.Revoke(id, req.Code); err != nil {
		if errors.Is(err, portal.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "no such batch or code", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", err.Error(), nil)
		return
	}
	s.audit(r, "portal_vouchers_revoked", "", map[string]any{"batch": id, "code": req.Code})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) portalVoucherDelete(w http.ResponseWriter, r *http.Request) {
	if !s.portalReady(w) {
		return
	}
	id := r.PathValue("id")
	if err := s.Portal.DeleteBatch(id); err != nil {
		if errors.Is(err, portal.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "no such batch", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", err.Error(), nil)
		return
	}
	s.audit(r, "portal_vouchers_deleted", "", map[string]any{"batch": id})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// portalPreview renders the candidate's portal page of an interface.
func (s *Server) portalPreview(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.Store.Candidate()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error(), nil)
		return
	}
	for _, p := range cfg.CaptivePortals {
		if p.Interface == r.PathValue("interface") {
			portal.Preview(w, p)
			return
		}
	}
	writeError(w, http.StatusNotFound, "not_found", "no portal on that interface in the candidate", nil)
}
