package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/auth"
	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// First-boot setup (task 022). While no admin has a password, the setup
// endpoints are open without authentication; once setup has committed they
// refuse (setup) or fall back to normal authentication (devices).

// SetupRequest is the body of POST /api/v1/setup.
type SetupRequest struct {
	AdminPassword string   `json:"admin_password"`
	Hostname      string   `json:"hostname,omitempty"` // default: keep goblin-gate
	WAN           SetupWAN `json:"wan"`
	LAN           SetupLAN `json:"lan"`
	SecurityLevel string   `json:"security_level"` // off | basic | strict
}

// SetupWAN configures the wan1 interface.
type SetupWAN struct {
	Device  string `json:"device"`
	Mode    string `json:"mode"`              // dhcp | static
	Address string `json:"address,omitempty"` // static: CIDR
	Gateway string `json:"gateway,omitempty"` // static
}

// SetupLAN configures the lan interface and its DHCP server.
type SetupLAN struct {
	Device    string `json:"device"`
	Address   string `json:"address"`              // CIDR, the box's LAN address
	DHCPStart string `json:"dhcp_start,omitempty"` // both empty: no DHCP server
	DHCPEnd   string `json:"dhcp_end,omitempty"`
}

// Device is one entry of GET /api/v1/system/devices.
type Device struct {
	Name string `json:"name"`
	MAC  string `json:"mac,omitempty"`
	Up   bool   `json:"up"`
}

// setupNeeded reports whether no admin has a password yet.
func (s *Server) setupNeeded() (bool, error) {
	cfg, err := s.Store.Running()
	if err != nil {
		return false, err
	}
	for _, a := range cfg.Admins {
		if a.PasswordHash != "" {
			return false, nil
		}
	}
	return true, nil
}

// openDuringSetup serves h without authentication while setup is needed and
// through s.Auth afterwards.
func (s *Server) openDuringSetup(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		needed, err := s.setupNeeded()
		if err != nil {
			s.internal(w, "read setup state", err)
			return
		}
		if needed || s.Auth == nil {
			h(w, r)
			return
		}
		s.Auth(h).ServeHTTP(w, r)
	})
}

func (s *Server) setupState(w http.ResponseWriter, r *http.Request) {
	needed, err := s.setupNeeded()
	if err != nil {
		s.internal(w, "read setup state", err)
		return
	}
	out := map[string]any{"needed": needed}
	if needed {
		// what the box runs now (e.g. ports and LAN address chosen on the
		// console), so the wizard starts from it instead of the defaults
		if cfg, err := s.Store.Running(); err == nil {
			out["current"] = currentSetup(cfg)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// SetupCurrent is the part of the running config the wizard pre-fills.
type SetupCurrent struct {
	WANDevice  string `json:"wan_device,omitempty"`
	LANDevice  string `json:"lan_device,omitempty"`
	LANAddress string `json:"lan_address,omitempty"`
	DHCPStart  string `json:"dhcp_start,omitempty"`
	DHCPEnd    string `json:"dhcp_end,omitempty"`
}

func currentSetup(cfg *config.Config) SetupCurrent {
	var c SetupCurrent
	for _, i := range cfg.Interfaces {
		switch i.Name {
		case "wan1":
			c.WANDevice = i.Device
		case "lan":
			c.LANDevice = i.Device
			if i.Address != nil {
				c.LANAddress = i.Address.String()
			}
		}
	}
	for _, d := range cfg.DHCP {
		if d.Interface == "lan" {
			c.DHCPStart, c.DHCPEnd = d.RangeStart.String(), d.RangeEnd.String()
		}
	}
	return c
}

func (s *Server) devices(w http.ResponseWriter, r *http.Request) {
	ifs, err := net.Interfaces()
	if err != nil {
		s.internal(w, "list network devices", err)
		return
	}
	out := []Device{}
	for _, i := range ifs {
		if i.Flags&net.FlagLoopback != 0 {
			continue
		}
		out = append(out, Device{Name: i.Name, MAC: i.HardwareAddr.String(), Up: i.Flags&net.FlagUp != 0})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	writeJSON(w, http.StatusOK, map[string]any{"devices": out})
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	// One setup at a time, so two browsers cannot both pass the check below.
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	needed, err := s.setupNeeded()
	if err != nil {
		s.internal(w, "read setup state", err)
		return
	}
	if !needed {
		writeError(w, http.StatusConflict, "setup_done", "setup has already been completed", nil)
		return
	}
	var req SetupRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	cfg, errs := BuildSetupConfig(req)
	if len(errs) == 0 {
		errs = config.Validate(cfg)
	}
	if len(errs) > 0 {
		writeError(w, http.StatusUnprocessableEntity, "validation_failed", "the setup answers have errors", errs)
		return
	}
	// Hash only once the answers are valid: hashing is deliberately slow.
	hash, err := auth.HashPassword(req.AdminPassword)
	if err != nil {
		s.internal(w, "hash admin password", err)
		return
	}
	cfg.Admins = []config.Admin{{Username: "admin", PasswordHash: hash, Role: "admin"}}
	if err := s.Store.SetCandidate(cfg); err != nil {
		s.internal(w, "store setup config", err)
		return
	}
	rev, err := s.Commit.Commit(context.WithoutCancel(r.Context()), "setup", "first-boot setup")
	if err != nil {
		// Do not leave the password hash behind in a half-done candidate.
		if derr := s.Store.DiscardCandidate(); derr != nil {
			s.Logger.Warn("setup: could not discard candidate", "error", derr)
		}
		s.commitError(w, err)
		return
	}
	s.audit(r, "setup", "setup", map[string]any{"revision": rev})
	writeJSON(w, http.StatusOK, map[string]any{"revision": rev})
}

// BuildSetupConfig turns setup answers into a config: config.Default() with
// the WAN, LAN, DHCP and security level applied. The admin is left without a
// password hash (the caller adds it). It checks the answers themselves; the
// caller still runs config.Validate on the result.
func BuildSetupConfig(req SetupRequest) (*config.Config, []config.ValidationError) {
	var errs []config.ValidationError
	bad := func(path, format string, a ...any) {
		errs = append(errs, config.ValidationError{Path: path, Message: fmt.Sprintf(format, a...)})
	}
	if err := auth.CheckPassword(req.AdminPassword); err != nil {
		bad("admin_password", "%s", err.Error())
	}
	cfg := config.Default()
	if h := strings.TrimSpace(req.Hostname); h != "" {
		cfg.System.Hostname = h
	}

	wan := &cfg.Interfaces[0]
	lan := &cfg.Interfaces[1]
	wan.Device = strings.TrimSpace(req.WAN.Device)
	lan.Device = strings.TrimSpace(req.LAN.Device)
	if wan.Device == "" {
		bad("wan.device", "choose the WAN network device")
	}
	if lan.Device == "" {
		bad("lan.device", "choose the LAN network device")
	}
	if wan.Device != "" && wan.Device == lan.Device {
		bad("lan.device", "LAN and WAN must use different devices")
	}

	switch req.WAN.Mode {
	case "dhcp":
	case "static":
		wan.Mode = "static"
		wan.Description = "WAN uplink (static)"
		p, err := netip.ParsePrefix(strings.TrimSpace(req.WAN.Address))
		if err != nil || !p.Addr().Is4() {
			bad("wan.address", "WAN address must be IPv4 CIDR, e.g. 203.0.113.2/24")
			break
		}
		wan.Address = &p
		gw, err := netip.ParseAddr(strings.TrimSpace(req.WAN.Gateway))
		switch {
		case err != nil || !gw.Is4():
			bad("wan.gateway", "WAN gateway must be an IPv4 address")
		case !p.Masked().Contains(gw) || gw == p.Addr():
			bad("wan.gateway", "gateway must be another address in %s", p.Masked())
		default:
			wan.Gateway = &gw
		}
	default:
		bad("wan.mode", "WAN mode must be dhcp or static")
	}

	lanAddr, err := netip.ParsePrefix(strings.TrimSpace(req.LAN.Address))
	switch {
	case err != nil || !lanAddr.Addr().Is4():
		bad("lan.address", "LAN address must be IPv4 CIDR, e.g. 192.168.1.1/24")
	case lanAddr.Bits() < 8 || lanAddr.Bits() > 30:
		bad("lan.address", "LAN prefix must be between /8 and /30")
	case lanAddr.Addr() == lanAddr.Masked().Addr():
		bad("lan.address", "use a host address, not the network address %s", lanAddr.Masked().Addr())
	default:
		lan.Address = &lanAddr
		if wan.Address != nil && wan.Address.Masked().Overlaps(lanAddr.Masked()) {
			bad("lan.address", "LAN subnet overlaps the WAN subnet")
		}
	}

	start, end := strings.TrimSpace(req.LAN.DHCPStart), strings.TrimSpace(req.LAN.DHCPEnd)
	switch {
	case start == "" && end == "":
		cfg.DHCP = nil
	case lan.Address == nil:
		// the LAN address error is already reported
	default:
		srv := &cfg.DHCP[0]
		a, errA := netip.ParseAddr(start)
		b, errB := netip.ParseAddr(end)
		okA := errA == nil && a.Is4() && lanAddr.Masked().Contains(a)
		okB := errB == nil && b.Is4() && lanAddr.Masked().Contains(b)
		if !okA {
			bad("lan.dhcp_start", "range start must be an address in %s", lanAddr.Masked())
		}
		if !okB {
			bad("lan.dhcp_end", "range end must be an address in %s", lanAddr.Masked())
		}
		if okA && okB {
			if b.Less(a) {
				bad("lan.dhcp_end", "range end is before range start")
			} else if a.Compare(lanAddr.Addr()) <= 0 && lanAddr.Addr().Compare(b) <= 0 {
				bad("lan.dhcp_start", "range must not include the LAN address %s", lanAddr.Addr())
			}
		}
		gw := lanAddr.Addr()
		srv.RangeStart, srv.RangeEnd = a, b
		srv.Gateway = &gw
		srv.DNS = []netip.Addr{gw}
	}

	switch req.SecurityLevel {
	case "off":
	case "basic", "strict":
		applySecurityLevel(cfg, req.SecurityLevel)
	default:
		bad("security_level", "security level must be off, basic or strict")
	}
	return cfg, errs
}

// applySecurityLevel adds placeholder profiles named after the level and
// attaches them to every accept policy. basic: IPS and DNS filter; strict:
// also web filter and application control. The engines (tasks 029-035) give
// the profiles real settings.
func applySecurityLevel(cfg *config.Config, level string) {
	comment := "created by first-boot setup (" + level + ")"
	cfg.Profiles.IPS = append(cfg.Profiles.IPS, config.IPSProfile{Name: level, Comment: comment})
	cfg.Profiles.DNSFilter = append(cfg.Profiles.DNSFilter, config.DNSFilterProfile{Name: level, Comment: comment})
	if level == "strict" {
		cfg.Profiles.WebFilter = append(cfg.Profiles.WebFilter, config.WebFilterProfile{Name: level, Comment: comment})
		cfg.Profiles.AppCtrl = append(cfg.Profiles.AppCtrl, config.AppCtrlProfile{Name: level, Comment: comment})
	}
	for i := range cfg.Policies {
		p := &cfg.Policies[i]
		if p.Action != "accept" {
			continue
		}
		p.IPSProfile = level
		p.DNSFilterProfile = level
		if level == "strict" {
			p.WebFilterProfile = level
			p.AppCtrlProfile = level
		}
	}
}
