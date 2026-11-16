package homelab

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/pkg/pluginsdk"
)

type fakeAPI struct {
	objs    map[string][]netip.Prefix // order: names
	names   []string
	puts    []string
	devices []pluginsdk.Device
	devErr  error
}

func (f *fakeAPI) LiveObjects(context.Context) ([]pluginsdk.LiveObject, error) {
	var out []pluginsdk.LiveObject
	for _, n := range f.names {
		out = append(out, pluginsdk.LiveObject{Name: n, Plugin: "homelab", Members: f.objs[n]})
	}
	return out, nil
}

func (f *fakeAPI) SetLiveObject(_ context.Context, name string, members []string) (pluginsdk.LiveChange, error) {
	f.puts = append(f.puts, name+" "+strings.Join(members, ","))
	var ps []netip.Prefix
	for _, m := range members {
		ps = append(ps, netip.MustParsePrefix(m))
	}
	f.objs[name] = ps
	return pluginsdk.LiveChange{Name: name, Members: len(ps), Changed: true}, nil
}

func (f *fakeAPI) Devices(context.Context) ([]pluginsdk.Device, error) { return f.devices, f.devErr }

type fakeSource struct {
	guests []Guest
	err    error
}

func (f *fakeSource) Name() string                            { return "fake" }
func (f *fakeSource) Guests(context.Context) ([]Guest, error) { return slices.Clone(f.guests), f.err }
func addrs(s ...string) (out []netip.Addr) {
	for _, a := range s {
		out = append(out, netip.MustParseAddr(a))
	}
	return out
}

func TestSync(t *testing.T) {
	api := &fakeAPI{names: []string{"db", "web"}, objs: map[string][]netip.Prefix{},
		devices: []pluginsdk.Device{{MAC: "BC:24:11:AA:BB:01", IPs: []string{"192.168.20.21"}}}}
	src := &fakeSource{guests: []Guest{
		{Name: "web-1", Tags: []string{"gg-web", "prod"}, Addrs: addrs("192.168.20.10", "fe80::1", "172.17.0.1", "fd00:20::10")},
		{Name: "WEB-2", Addrs: addrs("192.168.20.11")},                             // by pattern
		{Name: "db", Tags: []string{"gg-db"}, MACs: []string{"bc:24:11:aa:bb:01"}}, // by lease
		{Name: "nas", Tags: []string{"gg-storage"}, Addrs: addrs("192.168.20.30")}, // no such object
	}}
	var log bytes.Buffer
	s := &Syncer{API: api, Sources: []Source{src}, Leases: true, Logger: slog.New(slog.NewTextHandler(&log, nil)),
		Settings: Settings{Patterns: []Pattern{{Object: "Web", Glob: "web-*"}}, Networks: []netip.Prefix{netip.MustParsePrefix("192.168.20.0/24"), netip.MustParsePrefix("fd00:20::/64")}}}
	ctx := context.Background()
	if err := s.Once(ctx); err != nil {
		t.Fatal(err)
	}
	want := []string{"db 192.168.20.21/32", "web 192.168.20.10/32,192.168.20.11/32,fd00:20::10/128"}
	if !slices.Equal(api.puts, want) {
		t.Fatalf("puts %q, want %q", api.puts, want)
	}
	if !strings.Contains(log.String(), "gg-storage names no live object") {
		t.Errorf("unknown tag not logged:\n%s", log.String())
	}
	// nothing changed: nothing sent
	if err := s.Once(ctx); err != nil || len(api.puts) != 2 {
		t.Fatalf("second sync sent %q (%v)", api.puts, err)
	}
	if strings.Count(log.String(), "gg-storage") != 1 {
		t.Error("the unknown tag was logged twice")
	}
	// a guest goes away
	src.guests = src.guests[1:]
	s.Once(ctx)
	if api.puts[len(api.puts)-1] != "web 192.168.20.11/32" {
		t.Errorf("after web-1 stopped: %q", api.puts)
	}
	// a source fails: the objects stay as they are
	n := len(api.puts)
	src.guests, src.err = nil, errors.New("proxmox: guests: connection refused")
	s.report(s.Once(ctx))
	s.report(s.Once(ctx))
	if len(api.puts) != n || api.objs["web"] == nil {
		t.Errorf("an outage changed the objects: %q", api.puts[n:])
	}
	if strings.Count(log.String(), "connection refused") != 1 {
		t.Errorf("the same failure logged more than once:\n%s", log.String())
	}
	src.err = nil
	s.report(s.Once(ctx))
	if !strings.Contains(log.String(), "sync works again") {
		t.Error("recovery not logged")
	}
}

func TestSyncWithoutDevicesRead(t *testing.T) {
	api := &fakeAPI{names: []string{"db"}, objs: map[string][]netip.Prefix{},
		devErr: &pluginsdk.APIError{Status: 403, Code: "scope_denied"}}
	src := &fakeSource{guests: []Guest{{Name: "db", Tags: []string{"gg-db"}, MACs: []string{"bc:24:11:aa:bb:01"}}}}
	var log bytes.Buffer
	s := &Syncer{API: api, Sources: []Source{src}, Leases: true, Logger: slog.New(slog.NewTextHandler(&log, nil))}
	if err := s.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Leases || !strings.Contains(log.String(), "devices:read is not approved") {
		t.Errorf("denied devices:read: leases %v, log:\n%s", s.Leases, log.String())
	}
	if len(api.puts) != 0 {
		t.Errorf("sent %q for an object that stays empty", api.puts)
	}
}
