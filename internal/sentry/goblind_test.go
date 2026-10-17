package sentry_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/api"
	"github.com/networkgoblinlabs/goblin-gate/internal/apply"
	"github.com/networkgoblinlabs/goblin-gate/internal/blocks"
	"github.com/networkgoblinlabs/goblin-gate/internal/commit"
	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/logs"
	"github.com/networkgoblinlabs/goblin-gate/internal/plugins"
	"github.com/networkgoblinlabs/goblin-gate/internal/sentry"
)

type units struct{}

func (units) Restart(context.Context, string) error { return nil }
func (units) Stop(context.Context, string) error    { return nil }
func (units) State(context.Context, string) string  { return "active" }

// TestWithGoblind runs Sentry's client and engine against goblind's real
// plugin API: alerts come from the log store, blocks go through the
// temporary block API with its protected targets and plugin scopes.
func TestWithGoblind(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, err := config.New(filepath.Join(dir, "config"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.DNS.Forwarders = []netip.Addr{netip.MustParseAddr("9.9.9.9")}
	cfg.Plugins = []config.Plugin{{Name: "sentry", Enabled: true, Capabilities: []string{"events:read", "config:read", "notify:send", "blocks:read", "blocks:write"}}}
	if _, err := store.SaveRevision(cfg, "test", "sentry"); err != nil {
		t.Fatal(err)
	}
	engine := commit.New(store, nil, apply.NewFake())
	engine.Logger = logger
	srv := api.NewServer(store, engine)
	srv.Logger = logger
	if srv.Logs, err = logs.NewStore(filepath.Join(dir, "logs.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Logs.Close() })
	srv.Blocks = &blocks.Manager{Path: filepath.Join(dir, "blocks.json"), Run: func(context.Context, []byte) error { return nil }}

	// install the real manifest
	mgr := &plugins.Manager{ManifestDir: filepath.Join(dir, "share"), ProgramDir: filepath.Join(dir, "lib"), Systemd: units{}, Logger: logger}
	manifest, err := os.ReadFile("../../plugins/sentry/plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string][]byte{"share/sentry/plugin.json": manifest, "lib/sentry/run": []byte("#!/bin/sh\n")} {
		p := filepath.Join(dir, filepath.FromSlash(path))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, data, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if in := mgr.Rescan()["sentry"]; in == nil || in.Problem != "" {
		t.Fatalf("manifest: %+v", in)
	}
	mgr.Sync(context.Background(), cfg)
	srv.Plugins = mgr

	sock := httptest.NewUnstartedServer(srv.PluginRoutes())
	sock.Config.ConnContext = api.PluginConnContext(func(net.Conn) (string, bool) { return "sentry", true })
	sock.Start()
	t.Cleanup(sock.Close)
	client := &sentry.Client{HTTP: sock.Client(), Base: sock.URL}
	ctx := context.Background()

	caps, settings, err := client.Self(ctx)
	if err != nil || len(caps) != 5 || settings["mode"] != "approve" {
		t.Fatalf("self: %v %v %v", err, caps, settings)
	}
	live, err := client.Config(ctx)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := sentry.New(filepath.Join(dir, "state"), client, sentry.SettingsFrom(settings))
	if err != nil {
		t.Fatal(err)
	}
	eng.SetNetwork(sentry.NetworkFrom(live))

	now := time.Now().Unix()
	ips := func(src string) *logs.Event {
		return &logs.Event{Time: now, Kind: "ips", Action: "alert", SrcIP: src, DstIP: "192.168.1.10", DstPort: 445,
			Signature: "2024217: ET EXPLOIT SMB", Category: "Attempted Administrator Privilege Gain", Severity: "1", Message: "ET EXPLOIT SMB"}
	}
	if err := srv.Logs.InsertBatch([]*logs.Event{ips("198.51.100.7"), ips("9.9.9.9"), {Time: now, Kind: "traffic", Action: "allow"}}); err != nil {
		t.Fatal(err)
	}
	alerts, err := client.Alerts(ctx, eng.Cursor(), 100)
	if err != nil || len(alerts) != 2 || alerts[0].SignatureID != "2024217" || alerts[0].Signature != "ET EXPLOIT SMB" || alerts[0].Severity != 1 {
		t.Fatalf("alerts: %v %+v", err, alerts)
	}
	eng.Ingest(ctx, alerts)
	if again, _ := client.Alerts(ctx, eng.Cursor(), 100); len(again) != 0 {
		t.Fatalf("alerts read twice: %+v", again)
	}
	pending := eng.Incidents(sentry.StatusPending, 0)
	if len(pending) != 2 {
		t.Fatalf("pending: %+v", eng.Incidents("", 0))
	}
	byRemote := map[string]sentry.Incident{}
	for _, inc := range pending {
		byRemote[inc.Enrichment.Remote] = inc
	}

	// approve: goblind blocks it, Sentry verifies
	inc, d, err := eng.Answer(ctx, byRemote["198.51.100.7"].ID, sentry.AnswerApprove, "alice", 0)
	if err != nil || inc.Status != sentry.StatusActed || !strings.Contains(d.Result, "verified") {
		t.Fatalf("approve: %v %+v %+v", err, inc, d)
	}
	if b, ok := srv.Blocks.Get(netip.MustParseAddr("198.51.100.7")); !ok || b.Source != "plugin:sentry" {
		t.Fatalf("block in goblind: %+v %v", b, ok)
	}
	// a protected target is refused by goblind, end to end
	inc, d, err = eng.Answer(ctx, byRemote["9.9.9.9"].ID, sentry.AnswerApprove, "alice", 0)
	if err != nil || inc.Status != sentry.StatusFailed || !strings.Contains(d.Result, "a DNS server the box uses") {
		t.Fatalf("protected: %v %+v %+v", err, inc, d)
	}
	if _, ok := srv.Blocks.Get(netip.MustParseAddr("9.9.9.9")); ok {
		t.Fatal("protected target blocked")
	}
}
