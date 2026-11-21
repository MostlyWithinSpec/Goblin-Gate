package frr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The testdata/recorded answers are Debian 12's FRR 8.4 in the e2e
// (GG_FRR_RECORD=1): BGP up over IPv4 and IPv6, OSPF Full.

func recorded(t *testing.T) Runner {
	files := map[string]string{}
	for name, cmd := range Commands {
		files[cmd] = filepath.Join("testdata", "recorded", name+".json")
	}
	return func(ctx context.Context, args ...string) ([]byte, error) {
		if len(args) != 2 || args[0] != "-c" {
			t.Fatalf("args %v", args)
		}
		return os.ReadFile(files[args[1]])
	}
}

func TestRead(t *testing.T) {
	st, err := Read(context.Background(), recorded(t))
	if err != nil {
		t.Fatal(err)
	}
	if !st.Running || len(st.BGP) != 2 || len(st.OSPF) != 1 {
		t.Fatalf("%+v", st)
	}
	v4 := st.BGP[0]
	if v4.Address != "10.99.0.3" || v4.Family != "ipv4" || !v4.Up || v4.State != "Established" || v4.RemoteAS != 65002 ||
		v4.Description != "lab peer" || v4.PrefixesReceived != 1 || v4.PrefixesSent < 1 || v4.UptimeSeconds <= 0 {
		t.Errorf("ipv4 neighbour %+v", v4)
	}
	if v6 := st.BGP[1]; v6.Address != "fd99::3" || v6.Family != "ipv6" || !v6.Up || v6.PrefixesReceived != 1 {
		t.Errorf("ipv6 neighbour %+v", v6)
	}
	if o := st.OSPF[0]; o.RouterID != "10.99.0.3" || o.Address != "10.99.0.3" || o.Interface != "eth0" || !o.Full {
		t.Errorf("ospf %+v", o)
	}
	byKey := map[string]Route{}
	for _, r := range st.Routes {
		byKey[r.Protocol+" "+r.Prefix] = r
	}
	if r := byKey["bgp 172.20.0.0/24"]; !r.Selected || !r.Installed || r.Distance != 20 || len(r.NextHops) != 1 || r.NextHops[0] != "10.99.0.3" || r.Interface != "eth0" {
		t.Errorf("bgp route %+v", r)
	}
	if _, ok := byKey["bgp 172.21.0.0/24"]; ok {
		t.Error("the filtered route is listed")
	}
	if r := byKey["ospf 172.22.0.0/24"]; !r.Installed || r.Distance != 110 {
		t.Errorf("ospf route %+v", r)
	}
	if _, ok := byKey["bgp fd20::/64"]; !ok {
		t.Errorf("no IPv6 route: %v", st.Routes)
	}
}

func TestReadNotRunning(t *testing.T) {
	st, err := Read(context.Background(), func(context.Context, ...string) ([]byte, error) { return nil, ErrNotRunning })
	if err != nil || st.Running || st.BGP == nil || st.Routes == nil {
		t.Fatalf("%+v %v", st, err)
	}
	if _, err := Read(context.Background(), func(context.Context, ...string) ([]byte, error) { return nil, errors.New("boom") }); err == nil {
		t.Error("an error was swallowed")
	}
}

func TestParseStates(t *testing.T) {
	// a session that is not up, as FRR lists it; and newer FRR's OSPF fields
	ns, err := ParseBGPSummary([]byte(`{"ipv4Unicast":{"peers":{"10.0.0.9":{"remoteAs":64999,"state":"Active","peerUptimeMsec":5000,"pfxRcd":0}}}}`))
	if err != nil || len(ns) != 1 || ns[0].Up || ns[0].UptimeSeconds != 0 || ns[0].State != "Active" {
		t.Errorf("%+v %v", ns, err)
	}
	os, err := ParseOSPFNeighbors([]byte(`{"neighbors":{"1.1.1.1":[{"nbrState":"2-Way/DROther","ifaceAddress":"10.0.0.1","ifaceName":"eth1:10.0.0.2"}]}}`))
	if err != nil || len(os) != 1 || os[0].Full || os[0].Interface != "eth1" {
		t.Errorf("%+v %v", os, err)
	}
	if ns, err := ParseBGPSummary(nil); err != nil || len(ns) != 0 {
		t.Errorf("empty: %v %v", ns, err)
	}
}
