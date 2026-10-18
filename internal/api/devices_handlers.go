package api

import (
	"net/http"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/devices"
)

// Device inventory (task 070): GET lists the machines on the network,
// PATCH labels one, DELETE forgets it. The store is wired in by goblind;
// without it (dev mode) every endpoint answers 503 devices_unavailable.

func (s *Server) devicesUnavailable(w http.ResponseWriter) {
	writeError(w, http.StatusServiceUnavailable, "devices_unavailable", "the device inventory is not available on this system", nil)
}

// GET /api/v1/devices → {devices: [Device]} sorted by last_seen, newest
// first; optional ?interface=lan filters.
func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	if s.Devices == nil {
		s.devicesUnavailable(w)
		return
	}
	devs, err := s.Devices.List()
	if err != nil {
		s.internal(w, "list devices", err)
		return
	}
	if ifc := r.URL.Query().Get("interface"); ifc != "" {
		filtered := make([]devices.Device, 0, len(devs))
		for _, d := range devs {
			if d.Interface == ifc {
				filtered = append(filtered, d)
			}
		}
		devs = filtered
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": devs})
}

// PATCH /api/v1/devices/{mac} {name} → the updated device. A name of at most
// 63 characters labels it; "" clears the label.
func (s *Server) patchDevice(w http.ResponseWriter, r *http.Request) {
	if s.Devices == nil {
		s.devicesUnavailable(w)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if len(req.Name) > 63 {
		writeError(w, http.StatusBadRequest, "invalid_request", "name must be at most 63 characters", nil)
		return
	}
	d, err := s.Devices.SetName(r.PathValue("mac"), req.Name)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, "not_found", "no such device: "+r.PathValue("mac"), nil)
			return
		}
		s.internal(w, "rename the device", err)
		return
	}
	s.audit(r, "device_rename", s.user(r), map[string]any{"mac": d.MAC, "name": req.Name})
	writeJSON(w, http.StatusOK, d)
}

// DELETE /api/v1/devices/{mac} → 204. The device comes back as new if it is
// seen again.
func (s *Server) deleteDevice(w http.ResponseWriter, r *http.Request) {
	if s.Devices == nil {
		s.devicesUnavailable(w)
		return
	}
	mac := r.PathValue("mac")
	if err := s.Devices.Remove(mac); err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, "not_found", "no such device: "+mac, nil)
			return
		}
		s.internal(w, "delete the device", err)
		return
	}
	s.audit(r, "device_delete", s.user(r), map[string]any{"mac": mac})
	w.WriteHeader(http.StatusNoContent)
}
