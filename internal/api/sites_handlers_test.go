package api

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/sites"
	"github.com/networkgoblinlabs/goblin-gate/internal/wireguard"
)

// gate is a test API acting as one Goblin Gate with its own LAN.
func gate(t *testing.T, host, lan, endpoint string) (*testAPI, string) {
	t.Helper()
	keys := t.TempDir()
	a := newTestAPIWith(t, func(s *Server) {
		s.WireGuardKeyDir = keys
		s.SiteInvites = &sites.Invites{Path: filepath.Join(t.TempDir(), "invitations.json")}
	})
	cfg, err := a.store.Candidate()
	if err != nil {
		t.Fatal(err)
	}
	cfg.System.Hostname = host
	p := netip.MustParsePrefix(lan)
	for i := range cfg.Interfaces {
		if cfg.Interfaces[i].Name == "lan" {
			cfg.Interfaces[i].Address = &p
		}
	}
	cfg.DHCP = nil
	cfg.VPN.Sites.Endpoint = endpoint
	cfg.VPN.Sites.Networks = []string{"lan"}
	if err := a.store.SetCandidate(cfg); err != nil {
		t.Fatal(err)
	}
	return a, keys
}

func candidateSites(t *testing.T, a *testAPI) config.Sites {
	t.Helper()
	cfg, err := a.store.Candidate()
	if err != nil {
		t.Fatal(err)
	}
	return cfg.VPN.Sites
}

func TestSitePairing(t *testing.T) {
	home, homeKeys := gate(t, "home-gw.example", "192.168.1.1/24", "home.example.net")
	cabin, cabinKeys := gate(t, "Cabin", "192.168.50.1/24", "")

	inv := home.expect("POST", "/api/v1/vpn/sites/invitations", map[string]any{"site": "cabin"}, 201)
	code := inv["code"].(string)
	if !strings.HasPrefix(code, "GGSITE1-") || inv["sites_enabled"] != true || inv["port"].(float64) != config.FirstSitePort {
		t.Fatalf("invitation %v", inv)
	}
	hs := candidateSites(t, home)
	if !hs.Enabled || !config.DefaultSitesNetwork.Contains(hs.Address) || len(hs.Remotes) != 0 {
		t.Fatalf("home after invite %+v", hs)
	}
	if list := home.expect("GET", "/api/v1/vpn/sites/invitations", nil, 200)["invitations"].([]any); len(list) != 1 {
		t.Fatalf("invitations %v", list)
	} else if _, leak := list[0].(map[string]any)["secret"]; leak {
		t.Fatal("the secret is listed")
	}
	// the name is held by the open invitation
	if out := home.expect("POST", "/api/v1/vpn/sites/invitations", map[string]any{"site": "cabin"}, 409); errCode(out) != "name_taken" {
		t.Fatalf("second invite %v", out)
	}

	check := cabin.expect("POST", "/api/v1/vpn/sites/join/check", map[string]any{"code": code}, 200)
	if check["site"] != "home-gw" || check["endpoint"] != "home.example.net:51830" || len(check["problems"].([]any)) != 0 ||
		check["networks"].([]any)[0] != "192.168.1.0/24" {
		t.Fatalf("check %v", check)
	}
	joined := cabin.expect("POST", "/api/v1/vpn/sites/join", map[string]any{"code": code, "site": "home"}, 201)
	reply := joined["reply"].(string)
	cs := candidateSites(t, cabin)
	if !cs.Enabled || len(cs.Remotes) != 1 || cs.Remotes[0].Name != "home" || cs.Remotes[0].Endpoint != "home.example.net:51830" ||
		cs.Remotes[0].Address != hs.Address || cs.Address == hs.Address {
		t.Fatalf("cabin %+v", cs)
	}

	// a reply with a changed network is refused
	r, err := sites.ParseReply(reply)
	if err != nil {
		t.Fatal(err)
	}
	r.Networks = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	if out := home.expect("POST", "/api/v1/vpn/sites/accept", map[string]any{"code": r.Code()}, 403); errCode(out) != "bad_reply" {
		t.Fatalf("tampered %v", out)
	}
	if out := home.expect("POST", "/api/v1/vpn/sites/accept", map[string]any{"code": code}, 400); !strings.Contains(out["error"].(map[string]any)["message"].(string), "invitation code") {
		t.Fatalf("invitation pasted as reply %v", out)
	}

	acc := home.expect("POST", "/api/v1/vpn/sites/accept", map[string]any{"code": reply}, 201)
	site := acc["site"].(map[string]any)
	hs = candidateSites(t, home)
	if site["name"] != "cabin" || len(hs.Remotes) != 1 || hs.Remotes[0].Endpoint != "" || hs.Remotes[0].ListenPort != config.FirstSitePort ||
		hs.Remotes[0].Networks[0].String() != "192.168.50.0/24" || hs.Remotes[0].Address != cs.Address {
		t.Fatalf("home %+v", hs)
	}
	// single use
	if out := home.expect("POST", "/api/v1/vpn/sites/accept", map[string]any{"code": reply}, 404); errCode(out) != "no_invitation" {
		t.Fatalf("reused %v", out)
	}

	// keys: each side knows the other's public key and both have the same preshared key
	homePub, _ := wireguard.EnsureKey(homeKeys, config.SiteKeyName("cabin"))
	cabinPub, _ := wireguard.EnsureKey(cabinKeys, config.SiteKeyName("home"))
	if cs.Remotes[0].PublicKey != homePub.String() || hs.Remotes[0].PublicKey != cabinPub.String() {
		t.Fatal("public keys not exchanged")
	}
	a, _ := os.ReadFile(wireguard.PresharedKeyPath(homeKeys, config.SiteKeyName("cabin")))
	b, _ := os.ReadFile(wireguard.PresharedKeyPath(cabinKeys, config.SiteKeyName("home")))
	if len(a) < 44 || string(a) != string(b) {
		t.Fatalf("preshared keys differ: %q %q", a, b)
	}

	// both candidates are valid and commit
	for _, g := range []*testAPI{home, cabin} {
		cfg, _ := g.store.Candidate()
		if errs := config.Validate(cfg); len(errs) != 0 {
			t.Fatalf("invalid: %v", errs)
		}
	}
}

func TestSiteJoinProblems(t *testing.T) {
	home, _ := gate(t, "home", "192.168.1.1/24", "")
	same, _ := gate(t, "other", "192.168.1.1/24", "")
	code := home.expect("POST", "/api/v1/vpn/sites/invitations", map[string]any{"site": "other"}, 201)["code"].(string)
	check := same.expect("POST", "/api/v1/vpn/sites/join/check", map[string]any{"code": code}, 200)
	codes := map[string]bool{}
	for _, p := range check["problems"].([]any) {
		codes[p.(map[string]any)["code"].(string)] = true
	}
	if !codes["overlap"] || !codes["no_endpoint"] {
		t.Fatalf("problems %v", check["problems"])
	}
	if out := same.expect("POST", "/api/v1/vpn/sites/join", map[string]any{"code": code}, 409); len(out["error"].(map[string]any)["details"].([]any)) < 2 {
		t.Fatalf("join %v", out)
	}
	if s := candidateSites(t, same); len(s.Remotes) != 0 {
		t.Fatalf("joined anyway: %+v", s)
	}
	if out := same.expect("POST", "/api/v1/vpn/sites/join/check", map[string]any{"code": "GGSITE1-AAAA"}, 400); errCode(out) != "invalid_code" {
		t.Fatalf("garbage %v", out)
	}
}

func TestSiteInvitationExpires(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	home, _ := gate(t, "home", "192.168.1.1/24", "home.example.net")
	home.server.SiteNow = clock
	home.server.SiteInvites.Now = clock
	cabin, _ := gate(t, "cabin", "192.168.50.1/24", "")
	cabin.server.SiteNow = clock
	code := home.expect("POST", "/api/v1/vpn/sites/invitations", map[string]any{"site": "cabin", "ttl_minutes": 10}, 201)["code"].(string)
	reply := cabin.expect("POST", "/api/v1/vpn/sites/join", map[string]any{"code": code}, 201)["reply"].(string)
	now = now.Add(11 * time.Minute)
	if out := cabin.expect("POST", "/api/v1/vpn/sites/join/check", map[string]any{"code": code}, 400); !strings.Contains(out["error"].(map[string]any)["message"].(string), "expired") {
		t.Fatalf("expired invite %v", out)
	}
	home.expect("POST", "/api/v1/vpn/sites/accept", map[string]any{"code": reply}, 404)
	if list := home.expect("GET", "/api/v1/vpn/sites/invitations", nil, 200)["invitations"].([]any); len(list) != 0 {
		t.Fatalf("expired invitation listed %v", list)
	}
}

func TestSiteInvitationCancel(t *testing.T) {
	home, _ := gate(t, "home", "192.168.1.1/24", "home.example.net")
	id := home.expect("POST", "/api/v1/vpn/sites/invitations", map[string]any{"site": "cabin"}, 201)["id"].(string)
	home.expect("DELETE", "/api/v1/vpn/sites/invitations/"+id, nil, 200)
	home.expect("DELETE", "/api/v1/vpn/sites/invitations/"+id, nil, 404)
	// the name is free again
	home.expect("POST", "/api/v1/vpn/sites/invitations", map[string]any{"site": "cabin"}, 201)
}

// statusServer checks the status view without a commit engine round trip.
func TestSiteStatusView(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	pub, _ := wireguard.GenerateKey()
	pk := pub.Public().String()
	a := newTestAPIWith(t, func(s *Server) {
		s.SiteNow = func() time.Time { return now }
		s.WireGuardStats = func(_ context.Context, dev string) (map[string]wireguard.PeerStat, error) {
			if dev != "cabin" {
				return nil, errors.New("no device " + dev)
			}
			return map[string]wireguard.PeerStat{pk: {Endpoint: "198.51.100.7:51830", LatestHandshake: now.Unix() - 42, RxBytes: 1000, TxBytes: 2000}}, nil
		}
		s.SitePing = func(_ context.Context, dev string, addr netip.Addr) (time.Duration, error) {
			if dev == "cabin" && addr.String() == "10.254.2.2" {
				return 1500 * time.Microsecond, nil
			}
			return 0, errors.New("no answer")
		}
	})
	cfg, _ := a.store.Running()
	cfg.VPN.Sites = config.Sites{Enabled: true, Address: netip.MustParseAddr("10.254.1.1"), Endpoint: "home.example.net", Remotes: []config.Site{
		{Name: "cabin", Enabled: true, PublicKey: pk, ListenPort: 51830, Address: netip.MustParseAddr("10.254.2.2")},
		{Name: "office", Enabled: true, Via: "cabin", Address: netip.MustParseAddr("10.254.3.3")},
		{Name: "flat", Enabled: false, PublicKey: "YAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk=", Endpoint: "flat.example.net:51830", ListenPort: 51831, Address: netip.MustParseAddr("10.254.4.4")},
	}}
	if err := a.store.SetCandidate(cfg); err != nil {
		t.Fatal(err)
	}
	a.expect("POST", "/api/v1/commit", map[string]any{"comment": "sites"}, 200)
	out := a.expect("GET", "/api/v1/vpn/sites/status", nil, 200)
	list := out["sites"].([]any)
	cabin, office, flat := list[0].(map[string]any), list[1].(map[string]any), list[2].(map[string]any)
	if cabin["handshake_age"].(float64) != 42 || cabin["endpoint"] != "198.51.100.7:51830" || cabin["reachable"] != true || cabin["rtt_ms"].(float64) != 1.5 || cabin["rx_bytes"].(float64) != 1000 {
		t.Fatalf("cabin %v", cabin)
	}
	if office["via"] != "cabin" || office["reachable"] != false || office["handshake_age"].(float64) != -1 {
		t.Fatalf("office %v", office)
	}
	if _, checked := flat["reachable"]; checked || flat["enabled"] != false {
		t.Fatalf("flat %v", flat)
	}
	if out["address"] != "10.254.1.1" || out["enabled"] != true {
		t.Fatalf("status %v", out)
	}
}
