package api

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDHCPLeases(t *testing.T) {
	leases := filepath.Join(t.TempDir(), "kea-leases4.csv")
	a := newTestAPIWith(t, func(s *Server) { s.DHCPLeaseFile = leases })
	if out := a.expect("GET", "/api/v1/dhcp/leases", nil, 200); len(out["leases"].([]any)) != 0 {
		t.Fatalf("no lease file yet: %v", out)
	}
	future, past := time.Now().Unix()+600, time.Now().Unix()-600
	csv := "address,hwaddr,client_id,valid_lifetime,expire,subnet_id,fqdn_fwd,fqdn_rev,hostname,state,user_context\n" +
		fmt.Sprintf("192.168.1.20,AA:BB:CC:00:00:20,,3600,%d,1,0,0,printer,0,\n", future) +
		fmt.Sprintf("192.168.1.9,aa:bb:cc:00:00:09,,3600,%d,1,0,0,desktop,0,\n", future) +
		fmt.Sprintf("192.168.1.30,aa:bb:cc:00:00:30,,3600,%d,1,0,0,gone,0,\n", past)
	os.WriteFile(leases, []byte(csv), 0o644)
	ls := a.expect("GET", "/api/v1/dhcp/leases", nil, 200)["leases"].([]any)
	if len(ls) != 2 {
		t.Fatalf("leases %v", ls)
	}
	first := ls[0].(map[string]any)
	want := time.Unix(future, 0).UTC().Format(time.RFC3339)
	if first["ip"] != "192.168.1.9" || first["mac"] != "aa:bb:cc:00:00:09" || first["hostname"] != "desktop" || first["expires"] != want {
		t.Errorf("first lease %v", first)
	}
	if ls[1].(map[string]any)["ip"] != "192.168.1.20" {
		t.Errorf("not sorted by address: %v", ls)
	}
}
