package api

import (
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/auth"
	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

func validSetup(level string) SetupRequest {
	return SetupRequest{
		AdminPassword: "correct horse battery",
		WAN:           SetupWAN{Device: "enp1s0", Mode: "dhcp"},
		LAN:           SetupLAN{Device: "enp2s0", Address: "10.10.0.1/24", DHCPStart: "10.10.0.100", DHCPEnd: "10.10.0.200"},
		SecurityLevel: level,
	}
}

func TestSetupFlow(t *testing.T) {
	a := newTestAPI(t)
	out := a.expect("GET", "/api/v1/setup/state", nil, 200)
	if out["needed"] != true {
		t.Fatalf("fresh box: %v", out)
	}
	// the wizard pre-fills what the box runs (default config here)
	cur, _ := out["current"].(map[string]any)
	if cur["wan_device"] != "eth0" || cur["lan_device"] != "eth1" || cur["lan_address"] != "192.168.1.1/24" || cur["dhcp_start"] != "192.168.1.100" {
		t.Fatalf("current: %v", out["current"])
	}
	a.expect("GET", "/api/v1/system/devices", nil, 200)

	out = a.expect("POST", "/api/v1/setup", validSetup("basic"), 200)
	if out["revision"] != float64(1) {
		t.Fatalf("setup response %v", out)
	}
	run, _ := a.store.Running()
	if len(run.Admins) != 1 || run.Admins[0].Username != "admin" {
		t.Fatalf("admins %+v", run.Admins)
	}
	if err := auth.VerifyPassword("correct horse battery", run.Admins[0].PasswordHash); err != nil {
		t.Fatalf("stored hash does not verify: %v", err)
	}
	if run.Interfaces[0].Device != "enp1s0" || run.Interfaces[1].Device != "enp2s0" || run.Interfaces[1].Address.String() != "10.10.0.1/24" {
		t.Fatalf("interfaces %+v", run.Interfaces)
	}
	if run.DHCP[0].RangeStart.String() != "10.10.0.100" || run.DHCP[0].Gateway.String() != "10.10.0.1" {
		t.Fatalf("dhcp %+v", run.DHCP[0])
	}
	if a.applier.Calls() == 0 {
		t.Fatal("setup did not apply the config")
	}

	// rejected once setup has completed
	if out := a.expect("GET", "/api/v1/setup/state", nil, 200); out["needed"] != false {
		t.Fatalf("after setup: %v", out)
	}
	if out := a.expect("POST", "/api/v1/setup", validSetup("off"), 409); errCode(out) != "setup_done" {
		t.Fatalf("second setup: %v", out)
	}
}

func TestSetupValidationErrors(t *testing.T) {
	a := newTestAPI(t)
	req := validSetup("paranoid")
	req.AdminPassword = "short"
	req.LAN.Device = req.WAN.Device
	req.LAN.DHCPEnd = "10.20.0.5"
	out := a.expect("POST", "/api/v1/setup", req, 422)
	if errCode(out) != "validation_failed" {
		t.Fatalf("code %v", out)
	}
	details, _ := out["error"].(map[string]any)["details"].([]any)
	paths := map[string]bool{}
	for _, d := range details {
		paths[d.(map[string]any)["path"].(string)] = true
	}
	for _, p := range []string{"admin_password", "lan.device", "lan.dhcp_end", "security_level"} {
		if !paths[p] {
			t.Errorf("missing error for %s in %v", p, details)
		}
	}
	// nothing was committed and no hash was left behind
	if out := a.expect("GET", "/api/v1/setup/state", nil, 200); out["needed"] != true {
		t.Fatalf("state after failed setup: %v", out)
	}
	cand, _ := a.store.Candidate()
	for _, ad := range cand.Admins {
		if ad.PasswordHash != "" {
			t.Fatal("candidate holds a password hash after a failed setup")
		}
	}
	a.expect("POST", "/api/v1/setup", `{"admin_password":"x","extra":1}`, 400)
}

func TestBuildSetupConfigLevels(t *testing.T) {
	for _, tc := range []struct {
		level           string
		ips, web, app   string
		profilesPerKind int
	}{
		{"off", "", "", "", 0},
		{"basic", "basic", "", "", 1},
		{"strict", "strict", "strict", "strict", 1},
	} {
		t.Run(tc.level, func(t *testing.T) {
			cfg, errs := BuildSetupConfig(validSetup(tc.level))
			if len(errs) != 0 {
				t.Fatalf("answer errors: %v", errs)
			}
			cfg.Admins[0].PasswordHash = "$argon2id$placeholder"
			if errs := config.Validate(cfg); len(errs) != 0 {
				t.Fatalf("config invalid: %v", errs)
			}
			p := cfg.Policies[0]
			if p.IPSProfile != tc.ips || p.DNSFilterProfile != tc.ips || p.WebFilterProfile != tc.web || p.AppCtrlProfile != tc.app {
				t.Fatalf("policy profiles %+v", p)
			}
			if len(cfg.Profiles.IPS) != tc.profilesPerKind {
				t.Fatalf("ips profiles %+v", cfg.Profiles.IPS)
			}
		})
	}
}

func TestBuildSetupConfigStaticWAN(t *testing.T) {
	req := validSetup("off")
	req.WAN = SetupWAN{Device: "enp1s0", Mode: "static", Address: "203.0.113.2/24", Gateway: "203.0.113.1"}
	req.LAN.DHCPStart, req.LAN.DHCPEnd = "", ""
	cfg, errs := BuildSetupConfig(req)
	if len(errs) != 0 {
		t.Fatalf("errors %v", errs)
	}
	if w := cfg.Interfaces[0]; w.Mode != "static" || w.Gateway.String() != "203.0.113.1" {
		t.Fatalf("wan %+v", w)
	}
	if cfg.DHCP != nil {
		t.Fatalf("empty range should disable DHCP: %+v", cfg.DHCP)
	}

	req.WAN.Gateway = "198.51.100.1"
	req.LAN.Address = "203.0.113.9/24"
	_, errs = BuildSetupConfig(req)
	if len(errs) != 2 {
		t.Fatalf("want gateway and overlap errors, got %v", errs)
	}
}
