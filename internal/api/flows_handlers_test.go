package api

import (
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/flows"
)

func TestFlowsAPI(t *testing.T) {
	plain := newTestAPI(t)
	if out := plain.expect("GET", "/api/v1/flows", nil, 503); errCode(out) != "flows_unavailable" {
		t.Fatalf("no sampler: %v", out)
	}

	table := filepath.Join(t.TempDir(), "nf_conntrack")
	os.WriteFile(table, []byte(`ipv4     2 tcp      6 431999 ESTABLISHED src=192.168.1.10 dst=93.184.216.34 sport=51234 dport=443 packets=10 bytes=1000 src=93.184.216.34 dst=203.0.113.2 sport=443 dport=51234 packets=20 bytes=100000 [ASSURED] mark=0 delta-time=40 zone=0 use=2
ipv4     2 udp      17 25 src=192.168.1.11 dst=1.1.1.1 sport=40000 dport=53 packets=1 bytes=70 src=1.1.1.1 dst=203.0.113.2 sport=53 dport=40000 packets=1 bytes=120 mark=0 zone=0 use=2
`), 0o644)
	sampler := &flows.Sampler{Table: table}
	sampler.SetLocalPrefixes([]netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")})
	if err := sampler.Sample(); err != nil {
		t.Fatal(err)
	}
	a := newTestAPIWith(t, func(s *Server) { s.Flows = sampler })

	out := a.expect("GET", "/api/v1/flows", nil, 200)
	list, _ := out["flows"].([]any)
	if out["total"].(float64) != 2 || len(list) != 2 {
		t.Fatalf("flows: %v", out)
	}
	f := list[0].(map[string]any)
	if f["protocol"] == nil || f["bytes_out"] == nil || f["rate_in_bps"] == nil {
		t.Fatalf("flow fields: %v", f)
	}
	out = a.expect("GET", "/api/v1/flows?ip=192.168.1.11&limit=5", nil, 200)
	if out["total"].(float64) != 1 || out["flows"].([]any)[0].(map[string]any)["dst_port"].(float64) != 53 {
		t.Fatalf("ip filter: %v", out)
	}
	if out := a.expect("GET", "/api/v1/flows?ip=10.9.9.9", nil, 200); len(out["flows"].([]any)) != 0 {
		t.Fatalf("no match should be an empty list: %v", out)
	}
	a.expect("GET", "/api/v1/flows?ip=nope", nil, 400)
	a.expect("GET", "/api/v1/flows?limit=0", nil, 400)

	devs := a.expect("GET", "/api/v1/flows/devices", nil, 200)["devices"].([]any)
	if len(devs) != 2 || devs[0].(map[string]any)["ip"] == nil {
		t.Fatalf("devices: %v", devs)
	}
}
