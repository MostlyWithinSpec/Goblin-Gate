package api

import (
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
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

// POST /api/v1/devices/{mac}/wake → 200 {status: "sent", broadcast}. Sends a
// Wake-on-LAN magic packet to the broadcast address of the subnet the device
// is on (the IPv4 address of its interface or VLAN in the running config).
func (s *Server) wakeDevice(w http.ResponseWriter, r *http.Request) {
	if s.Devices == nil {
		s.devicesUnavailable(w)
		return
	}
	devs, err := s.Devices.List()
	if err != nil {
		s.internal(w, "list devices", err)
		return
	}
	var d *devices.Device
	for i := range devs {
		if devs[i].MAC == r.PathValue("mac") {
			d = &devs[i]
			break
		}
	}
	if d == nil {
		writeError(w, http.StatusNotFound, "not_found", "no such device: "+r.PathValue("mac"), nil)
		return
	}
	run, err := s.Store.Running()
	if err != nil {
		s.internal(w, "read the running config", err)
		return
	}
	bcast, ok := wolBroadcast(run, d.Interface)
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, "no_subnet",
			"the device's interface has no IPv4 subnet to broadcast to", nil)
		return
	}
	mac, err := net.ParseMAC(d.MAC)
	if err != nil {
		s.internal(w, "parse the device MAC", err)
		return
	}
	if err := devices.Wake(mac, bcast); err != nil {
		s.internal(w, "send the wake packet", err)
		return
	}
	s.audit(r, "device_wake", s.user(r), map[string]any{"mac": d.MAC, "broadcast": bcast.String()})
	writeJSON(w, http.StatusOK, map[string]any{"status": "sent", "broadcast": bcast.String()})
}

// wolBroadcast finds the IPv4 address of the named interface or VLAN in cfg
// and returns its subnet broadcast address (the last address of the prefix).
func wolBroadcast(cfg *config.Config, name string) (netip.Addr, bool) {
	if name == "" || cfg == nil {
		return netip.Addr{}, false
	}
	var addr *netip.Prefix
	for i := range cfg.Interfaces {
		if cfg.Interfaces[i].Name == name {
			addr = cfg.Interfaces[i].Address
			break
		}
	}
	if addr == nil {
		for i := range cfg.VLANs {
			if cfg.VLANs[i].Name == name {
				addr = cfg.VLANs[i].Address
				break
			}
		}
	}
	if addr == nil || !addr.IsValid() || !addr.Addr().Is4() {
		return netip.Addr{}, false
	}
	p := *addr
	bits := p.Bits()
	hostBits := 32 - bits
	network := p.Masked()
	bcast, ok := addPrefix(network.Addr(), uint(hostBits))
	if !ok {
		return netip.Addr{}, false
	}
	return bcast, true
}

// addPrefix returns a + (2^hostBits - 1), i.e. the last address of the prefix.
func addPrefix(a netip.Addr, hostBits uint) (netip.Addr, bool) {
	if !a.Is4() || hostBits > 32 {
		return netip.Addr{}, false
	}
	o := a.As4()
	add := ^uint32(0) >> (32 - hostBits) // 2^hostBits - 1
	sum := uint32(o[0])<<24 | uint32(o[1])<<16 | uint32(o[2])<<8 | uint32(o[3])
	if sum+add < sum { // overflow: /0 prefix
		return netip.Addr{}, false
	}
	b := (sum + add) & 0xFFFFFFFF
	return netip.AddrFrom4([4]byte{byte(b >> 24), byte(b >> 16), byte(b >> 8), byte(b)}), true
}
