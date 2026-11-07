package api

import (
	"errors"
	"net/http"
	"net/netip"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/nicinfo"
	"github.com/networkgoblinlabs/goblin-gate/internal/render"
	"github.com/networkgoblinlabs/goblin-gate/internal/throughput"
)

// NICView is one interface of GET /api/v1/diag/nics: the configured side
// (name, role, offload overrides) and what the card reports.
type NICView struct {
	Name     string           `json:"name"`
	Role     string           `json:"role"`
	Enabled  bool             `json:"enabled"`
	Override *config.Offloads `json:"offload_overrides,omitempty"`
	nicinfo.Info
}

// GET /api/v1/diag/nics → {interfaces: [NICView]}: driver, link speed,
// duplex, offloads, queues and hints for slow throughput, per physical
// interface of the running config.
func (s *Server) diagNICs(w http.ResponseWriter, r *http.Request) {
	if s.NICs == nil {
		unavailable(w, "network card info")
		return
	}
	cfg, err := s.Store.Running()
	if err != nil {
		s.internal(w, "read running config", err)
		return
	}
	devs := render.Devices(cfg)
	out := []NICView{}
	for _, in := range cfg.Interfaces {
		out = append(out, NICView{Name: in.Name, Role: in.Role, Enabled: in.Enabled, Override: in.Offloads, Info: s.NICs.Read(r.Context(), devs[in.Name])})
	}
	writeJSON(w, http.StatusOK, map[string]any{"interfaces": out})
}

// GET /api/v1/diag/throughput → the test server's state and results.
func (s *Server) throughputStatus(w http.ResponseWriter, r *http.Request) {
	if s.Throughput == nil {
		unavailable(w, "the throughput test")
		return
	}
	writeJSON(w, http.StatusOK, s.Throughput.Status())
}

type throughputRequest struct {
	Interface string `json:"interface"`         // an interface or VLAN on the LAN side
	Minutes   int    `json:"minutes,omitempty"` // how long it stays open; 0 = 5, at most 15
}

// POST /api/v1/diag/throughput {interface, minutes?} → 201 with the state:
// an iperf3 server on the interface's address, open only there, for a few
// minutes. Clients run `iperf3 -c <address>` (add -R to measure download).
func (s *Server) throughputStart(w http.ResponseWriter, r *http.Request) {
	if s.Throughput == nil {
		unavailable(w, "the throughput test")
		return
	}
	var req throughputRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Minutes < 0 || time.Duration(req.Minutes)*time.Minute > throughput.MaxDuration {
		writeError(w, http.StatusBadRequest, "invalid_minutes", "minutes must be between 1 and 15", nil)
		return
	}
	cfg, err := s.Store.Running()
	if err != nil {
		s.internal(w, "read running config", err)
		return
	}
	dev, addr, code, msg := throughputTarget(cfg, req.Interface)
	if code != "" {
		writeError(w, http.StatusBadRequest, code, msg, nil)
		return
	}
	st, err := s.Throughput.Start(req.Interface, dev, addr, time.Duration(req.Minutes)*time.Minute)
	switch {
	case errors.Is(err, throughput.ErrRunning):
		writeError(w, http.StatusConflict, "throughput_running", err.Error(), nil)
		return
	case err != nil:
		s.internal(w, "start the throughput test", err)
		return
	}
	s.audit(r, "throughput_test", s.user(r), map[string]any{"interface": req.Interface, "minutes": req.Minutes})
	writeJSON(w, http.StatusCreated, st)
}

// throughputTarget finds the device and address to serve on: an enabled
// LAN-side interface or VLAN with a static IPv4 address. Never a WAN.
func throughputTarget(cfg *config.Config, name string) (dev, addr, code, msg string) {
	wan := map[string]bool{}
	for _, in := range cfg.Interfaces {
		if in.Role == "wan" {
			wan[in.Name] = true
		}
	}
	var (
		found, isWAN, enabled bool
		mode                  string
		prefix                *netip.Prefix
	)
	for _, in := range cfg.Interfaces {
		if in.Name == name {
			found, isWAN, enabled, mode, prefix = true, wan[name], in.Enabled, in.Mode, in.Address
		}
	}
	for _, v := range cfg.VLANs {
		if v.Name == name {
			found, isWAN, enabled, mode, prefix = true, wan[v.Parent], v.Enabled, v.Mode, v.Address
		}
	}
	switch {
	case !found:
		return "", "", "unknown_interface", "no interface or VLAN named " + name
	case isWAN:
		return "", "", "wan_interface", "the throughput test runs on LAN-side interfaces only"
	case !enabled:
		return "", "", "interface_disabled", name + " is disabled"
	case mode != "static" || prefix == nil || !prefix.Addr().Is4():
		return "", "", "no_address", name + " has no static IPv4 address to serve on"
	}
	return render.Devices(cfg)[name], prefix.Addr().String(), "", ""
}

// DELETE /api/v1/diag/throughput stops the server and closes the port →
// the final state with the results.
func (s *Server) throughputStop(w http.ResponseWriter, r *http.Request) {
	if s.Throughput == nil {
		unavailable(w, "the throughput test")
		return
	}
	s.Throughput.Stop()
	writeJSON(w, http.StatusOK, s.Throughput.Status())
}
