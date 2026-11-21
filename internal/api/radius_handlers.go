package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/devices"
	"github.com/networkgoblinlabs/goblin-gate/internal/radius"
)

// RadiusLog is the recent authentications, newest first.
type RadiusLog struct {
	Events []radius.Event `json:"events"`
}

// RadiusSessions is the devices a switch or access point let in.
type RadiusSessions struct {
	Sessions []radius.Session `json:"sessions"`
}

// deviceVLANRequest moves a device: VLAN name, "" = none of its own.
type deviceVLANRequest struct {
	VLAN string `json:"vlan"`
}

// DeviceVLANResult is the device after a move and what the Disconnect did
// (absent: the device had no RADIUS session; it gets the VLAN next time).
type DeviceVLANResult struct {
	Device          devices.Device            `json:"device"`
	Disconnect      *radius.DisconnectOutcome `json:"disconnect,omitempty"`
	DisconnectError string                    `json:"disconnect_error,omitempty"`
}

type ppskRequest struct {
	PSK string `json:"psk"`
}

type certRequest struct {
	Days int `json:"days,omitempty"`
}

type certRevokeRequest struct {
	Serial string `json:"serial,omitempty"`
}

func (s *Server) radiusReady(w http.ResponseWriter) bool {
	if s.Radius == nil || s.RadiusState == nil {
		unavailable(w, "RADIUS")
		return false
	}
	return true
}

// GET /api/v1/radius/log?limit=N (default 200, at most radius.MaxEvents).
func (s *Server) radiusLog(w http.ResponseWriter, r *http.Request) {
	if !s.radiusReady(w) {
		return
	}
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > radius.MaxEvents {
			writeError(w, http.StatusBadRequest, "invalid_request", "limit: want 1-"+strconv.Itoa(radius.MaxEvents), nil)
			return
		}
		limit = n
	}
	writeJSON(w, http.StatusOK, RadiusLog{Events: s.Radius.Events(limit)})
}

// GET /api/v1/radius/sessions
func (s *Server) radiusSessions(w http.ResponseWriter, r *http.Request) {
	if !s.radiusReady(w) {
		return
	}
	writeJSON(w, http.StatusOK, RadiusSessions{Sessions: s.Radius.Sessions()})
}

func (s *Server) disconnect(ctx context.Context, mac string) (radius.DisconnectOutcome, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.Radius.Disconnect(ctx, mac)
}

// POST /api/v1/radius/sessions/{mac}/disconnect
func (s *Server) radiusDisconnect(w http.ResponseWriter, r *http.Request) {
	if !s.radiusReady(w) {
		return
	}
	out, err := s.disconnect(r.Context(), r.PathValue("mac"))
	switch {
	case errors.Is(err, radius.ErrNoSession):
		writeError(w, http.StatusNotFound, "not_found", err.Error(), nil)
		return
	case errors.Is(err, radius.ErrNoClient):
		writeError(w, http.StatusConflict, "conflict", err.Error(), nil)
		return
	case err != nil:
		writeError(w, http.StatusBadGateway, "disconnect_failed", err.Error(), nil)
		return
	}
	s.audit(r, "radius_disconnect", s.user(r), map[string]any{"mac": out.MAC, "client": out.Client, "result": out.Result})
	writeJSON(w, http.StatusOK, out)
}

// PUT /api/v1/devices/{mac}/vlan {vlan}: the VLAN RADIUS puts the device in
// (a VLAN name of the running configuration, "" = none of its own). A
// device with a RADIUS session is disconnected so it rejoins at once.
func (s *Server) setDeviceVLAN(w http.ResponseWriter, r *http.Request) {
	if s.Devices == nil {
		s.devicesUnavailable(w)
		return
	}
	var req deviceVLANRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.VLAN = strings.TrimSpace(req.VLAN)
	if req.VLAN != "" {
		cfg, err := s.Store.Running()
		if err != nil {
			s.internal(w, "read the configuration", err)
			return
		}
		if cfg.VLANID(req.VLAN) == 0 {
			writeError(w, http.StatusBadRequest, "validation_failed", "unknown VLAN "+strconv.Quote(req.VLAN)+" (commit new VLANs first)", nil)
			return
		}
	}
	d, err := s.Devices.SetVLAN(r.PathValue("mac"), req.VLAN)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	out := DeviceVLANResult{Device: *d}
	if s.Radius != nil {
		res, err := s.disconnect(r.Context(), d.MAC)
		switch {
		case errors.Is(err, radius.ErrNoSession):
		case err != nil:
			out.DisconnectError = err.Error()
		default:
			out.Disconnect = &res
		}
	}
	s.audit(r, "device_vlan", s.user(r), map[string]any{"mac": d.MAC, "vlan": req.VLAN})
	writeJSON(w, http.StatusOK, out)
}

// GET /api/v1/radius/ppsk → {devices: [{mac, psk, set}]}
func (s *Server) radiusPPSKs(w http.ResponseWriter, r *http.Request) {
	if !s.radiusReady(w) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": s.RadiusState.PPSKs()})
}

// PUT /api/v1/radius/ppsk/{mac} {psk}: the device's own Wi-Fi password.
func (s *Server) radiusSetPPSK(w http.ResponseWriter, r *http.Request) {
	if !s.radiusReady(w) {
		return
	}
	var req ppskRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.PSK == "" {
		writeError(w, http.StatusBadRequest, "validation_failed", radius.ErrBadPSK.Error(), nil)
		return
	}
	if err := s.RadiusState.SetPPSK(r.PathValue("mac"), req.PSK); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed", err.Error(), nil)
		return
	}
	s.audit(r, "radius_ppsk_set", s.user(r), map[string]any{"mac": r.PathValue("mac")})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// DELETE /api/v1/radius/ppsk/{mac}
func (s *Server) radiusDeletePPSK(w http.ResponseWriter, r *http.Request) {
	if !s.radiusReady(w) {
		return
	}
	if err := s.RadiusState.SetPPSK(r.PathValue("mac"), ""); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	s.audit(r, "radius_ppsk_delete", s.user(r), map[string]any{"mac": r.PathValue("mac")})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// GET /api/v1/radius/certificates?user= → {certificates}
func (s *Server) radiusCerts(w http.ResponseWriter, r *http.Request) {
	if !s.radiusReady(w) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"certificates": s.RadiusState.Certs(r.URL.Query().Get("user"))})
}

// POST /api/v1/radius/users/{name}/certificate {days}: a client
// certificate for EAP-TLS. The key is in the answer only; the box does not
// keep it.
func (s *Server) radiusIssueCert(w http.ResponseWriter, r *http.Request) {
	if !s.radiusReady(w) {
		return
	}
	var req certRequest
	if r.ContentLength != 0 && !decodeJSON(w, r, &req) {
		return
	}
	cfg, err := s.Store.Running()
	if err != nil {
		s.internal(w, "read the configuration", err)
		return
	}
	u, ok := cfg.RADIUS.RADIUSUserByName(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "no enabled RADIUS user "+strconv.Quote(r.PathValue("name"))+" (commit new users first)", nil)
		return
	}
	if req.Days < 0 || req.Days > radius.MaxCertDays {
		writeError(w, http.StatusBadRequest, "validation_failed", "days: want 1-"+strconv.Itoa(radius.MaxCertDays)+", or 0 for 365", nil)
		return
	}
	c, err := s.RadiusState.IssueClient(u.Name, req.Days)
	if errors.Is(err, radius.ErrNoCA) {
		writeError(w, http.StatusConflict, "conflict", err.Error(), nil)
		return
	}
	if err != nil {
		s.internal(w, "issue the certificate", err)
		return
	}
	s.audit(r, "radius_cert_issued", s.user(r), map[string]any{"user": u.Name, "serial": c.Serial})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, c)
}

// POST /api/v1/radius/users/{name}/certificates/revoke {serial?}: revokes
// one certificate, or all of the user's; their devices are refused at the
// next login.
func (s *Server) radiusRevokeCerts(w http.ResponseWriter, r *http.Request) {
	if !s.radiusReady(w) {
		return
	}
	var req certRevokeRequest
	if r.ContentLength != 0 && !decodeJSON(w, r, &req) {
		return
	}
	n, err := s.RadiusState.RevokeCerts(r.PathValue("name"), req.Serial)
	if err != nil {
		s.internal(w, "revoke the certificates", err)
		return
	}
	if n == 0 {
		writeError(w, http.StatusNotFound, "not_found", "no valid certificate to revoke", nil)
		return
	}
	s.audit(r, "radius_cert_revoked", s.user(r), map[string]any{"user": r.PathValue("name"), "serial": req.Serial, "count": n})
	writeJSON(w, http.StatusOK, map[string]int{"revoked": n})
}
