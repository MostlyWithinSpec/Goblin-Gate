package api

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWANStatus(t *testing.T) {
	dir := t.TempDir()
	// eth0's default route via 24.101.68.1 (little-endian hex, as the kernel writes it)
	route := "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n" +
		"eth0\t00000000\t01446518\t0003\t0\t0\t0\t00000000\t0\t0\t0\n" +
		"eth0\t00446518\t00000000\t0001\t0\t0\t0\t00FFFFFF\t0\t0\t0\n" +
		"eth1\t0001A8C0\t00000000\t0001\t0\t0\t0\t00FFFFFF\t0\t0\t0\n"
	rt := filepath.Join(dir, "route")
	os.WriteFile(rt, []byte(route), 0o644)
	oldRT, oldLink := routeTable, wanLink
	routeTable = rt
	wanLink = func(dev string) (bool, []string) { return dev == "eth0", []string{"24.101.68.119/24"} }
	t.Cleanup(func() { routeTable, wanLink = oldRT, oldLink })

	a := newTestAPI(t)
	out := a.expect("GET", "/api/v1/wan/status", nil, 200)
	wans := out["wans"].([]any)
	if len(wans) != 1 {
		t.Fatalf("%v", out)
	}
	w := wans[0].(map[string]any)
	if w["name"] != "wan1" || w["device"] != "eth0" || w["up"] != true || w["gateway"] != "24.101.68.1" {
		t.Fatalf("%v", w)
	}
}
