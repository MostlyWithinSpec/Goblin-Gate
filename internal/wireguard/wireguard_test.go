package wireguard

import (
	"net/netip"
	"os"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

func TestKeys(t *testing.T) {
	// RFC 7748 section 6.1 test vector (Alice)
	priv, _ := ParseKey("dwdtCnMYpX08FsFyUbJmRd9ML4frwJkqsXf7pR25LCo=")
	if got := priv.Public().String(); got != "hSDwCYkwp1R0i33ctD73Wg2/Og0mOBr066SpjqqbTmo=" {
		t.Fatalf("public key %s", got)
	}
	k, err := GenerateKey()
	if err != nil || k[0]&7 != 0 || k[31]&128 != 0 || k[31]&64 == 0 {
		t.Fatalf("not clamped: %x %v", k, err)
	}
	if _, err := ParseKey("short"); err == nil {
		t.Fatal("bad key accepted")
	}
}

func TestEnsureKey(t *testing.T) {
	dir := t.TempDir() + "/wg"
	a, err := EnsureKey(dir, "wg0")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := EnsureKey(dir, "wg0")
	if a != b {
		t.Fatal("key changed on second call")
	}
	raw, _ := os.ReadFile(KeyPath(dir, "wg0"))
	if strings.Contains(string(raw), a.String()) {
		t.Fatal("the file must hold the private key")
	}
	cfg := config.Default()
	cfg.VPN.WireGuard = []config.WireGuard{{Name: "wg1"}}
	if err := EnsureKeys(dir, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(KeyPath(dir, "wg1")); err != nil {
		t.Fatal(err)
	}
}

func TestNextAddressAndClientConfig(t *testing.T) {
	w := config.WireGuard{Name: "wg0", Address: netip.MustParsePrefix("10.66.0.1/30"), Endpoint: "gate.example.net",
		Peers: []config.WireGuardPeer{}}
	a, err := NextAddress(w)
	if err != nil || a.String() != "10.66.0.2" {
		t.Fatalf("%v %v", a, err)
	}
	w.Peers = append(w.Peers, config.WireGuardPeer{Address: a})
	if _, err := NextAddress(w); err == nil {
		t.Fatal("/30 has room for one client only")
	}
	cfg := config.Default()
	k, _ := GenerateKey()
	text := ClientConfig(cfg, w, config.WireGuardPeer{Name: "phone", Address: a}, k, k.Public(), ClientOptions{})
	for _, want := range []string{"PrivateKey = " + k.String(), "Address = 10.66.0.2/32", "DNS = 10.66.0.1", "Endpoint = gate.example.net:51820", "AllowedIPs = 10.66.0.0/30, 192.168.1.0/24"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
	if full := ClientConfig(cfg, w, config.WireGuardPeer{Address: a}, k, k, ClientOptions{FullTunnel: true}); !strings.Contains(full, "AllowedIPs = 0.0.0.0/0") {
		t.Error(full)
	}
}

func TestParseDump(t *testing.T) {
	out := "PRIV\tPUB\t51820\toff\n" +
		"peerA=\t(none)\t203.0.113.5:4444\t10.66.0.2/32\t1791740000\t1200\t3400\t25\n" +
		"peerB=\t(none)\t(none)\t10.66.0.3/32\t0\t0\t0\toff\n"
	st := ParseDump(out)
	if st["peerA="].Endpoint != "203.0.113.5:4444" || st["peerA="].RxBytes != 1200 || st["peerA="].LatestHandshake != 1791740000 || st["peerB="].Endpoint != "" {
		t.Fatalf("%+v", st)
	}
}
