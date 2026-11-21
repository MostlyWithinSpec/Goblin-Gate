package api

import (
	"net/netip"
	"path/filepath"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/devices"
	"github.com/networkgoblinlabs/goblin-gate/internal/radius"
)

func TestRadiusAPI(t *testing.T) {
	dir := t.TempDir()
	st := &radius.State{Path: filepath.Join(dir, "radius.json"), RootCert: filepath.Join(dir, "none.crt"), RootKey: filepath.Join(dir, "none.key")}
	devs := &devices.Store{Path: filepath.Join(dir, "devices.json")}
	var a *testAPI
	m := &radius.Manager{Config: func() *config.Config { c, _ := a.store.Running(); return c }, Devices: devs, State: st}
	a = newTestAPIWith(t, func(s *Server) { s.Radius, s.RadiusState, s.Devices = m, st, devs })

	// a VLAN must be committed before devices move into it
	a.expect("PUT", "/api/v1/devices/aa:bb:cc:00:00:01/vlan", map[string]string{"vlan": "iot"}, 400)
	cfg, _ := a.store.Candidate()
	addr := netip.MustParsePrefix("192.168.20.1/24")
	cfg.VLANs = append(cfg.VLANs, config.VLAN{Name: "iot", Parent: "lan", VLANID: 20, Mode: "static", Address: &addr, Enabled: true})
	if err := a.store.SetCandidate(cfg); err != nil {
		t.Fatal(err)
	}
	a.expect("POST", "/api/v1/commit", nil, 200)
	out := a.expect("PUT", "/api/v1/devices/aa:bb:cc:00:00:01/vlan", map[string]string{"vlan": "iot"}, 200)
	if d := out["device"].(map[string]any); d["mac"] != "aabbcc000001" || d["vlan"] != "iot" || out["disconnect"] != nil {
		t.Errorf("move: %v", out)
	}
	if devs.VLANOf("aabbcc000001") != "iot" {
		t.Error("not stored")
	}
	a.expect("PUT", "/api/v1/devices/nope/vlan", map[string]string{"vlan": ""}, 400)
	a.expect("PUT", "/api/v1/devices/aabbcc000001/vlan", map[string]string{"vlan": ""}, 200)

	a.expect("PUT", "/api/v1/radius/ppsk/aabbcc000001", map[string]string{"psk": "short"}, 400)
	a.expect("PUT", "/api/v1/radius/ppsk/aabbcc000001", map[string]string{"psk": "a-long-wifi-pass"}, 200)
	list := a.expect("GET", "/api/v1/radius/ppsk", nil, 200)["devices"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["psk"] != "a-long-wifi-pass" {
		t.Errorf("ppsk %v", list)
	}
	a.expect("DELETE", "/api/v1/radius/ppsk/aabbcc000001", nil, 200)

	a.expect("GET", "/api/v1/radius/log?limit=0", nil, 400)
	if ev := a.expect("GET", "/api/v1/radius/log", nil, 200)["events"].([]any); len(ev) != 0 {
		t.Errorf("log %v", ev)
	}
	a.expect("GET", "/api/v1/radius/sessions", nil, 200)
	a.expect("POST", "/api/v1/radius/sessions/aabbcc000001/disconnect", nil, 404)
	// certificates: only for committed, enabled users; the CA must exist
	a.expect("POST", "/api/v1/radius/users/alice/certificate", map[string]int{"days": 30}, 404)
	a.expect("POST", "/api/v1/radius/users/alice/certificates/revoke", nil, 404)
	a.expect("GET", "/api/v1/radius/certificates", nil, 200)
}

func TestRadiusAPIUnavailable(t *testing.T) {
	a := newTestAPIWith(t, nil)
	a.expect("GET", "/api/v1/radius/log", nil, 503)
	a.expect("GET", "/api/v1/radius/ppsk", nil, 503)
}

func TestRadiusAnnotationsShown(t *testing.T) {
	cfg := config.Default()
	keep := AnnotatingPlugins(cfg)
	if keep(RadiusAnnotations) {
		t.Error("shown while RADIUS is off")
	}
	cfg.RADIUS = config.RADIUS{Enabled: true, MAB: true, Clients: []config.RADIUSClient{{Name: "s", Address: "192.168.1.2", Secret: "12345678"}}}
	if !AnnotatingPlugins(cfg)(RadiusAnnotations) {
		t.Error("hidden while RADIUS is on")
	}
}
