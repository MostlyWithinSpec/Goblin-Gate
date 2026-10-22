// Command goblind is the Goblin Gate management daemon: REST API server and
// commit engine (log ingest arrives in later tasks).
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/api"
	"github.com/networkgoblinlabs/goblin-gate/internal/apply"
	"github.com/networkgoblinlabs/goblin-gate/internal/auth"
	"github.com/networkgoblinlabs/goblin-gate/internal/blocks"
	"github.com/networkgoblinlabs/goblin-gate/internal/capture"
	"github.com/networkgoblinlabs/goblin-gate/internal/checks"
	"github.com/networkgoblinlabs/goblin-gate/internal/commit"
	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/crowdsec"
	"github.com/networkgoblinlabs/goblin-gate/internal/ddns"
	"github.com/networkgoblinlabs/goblin-gate/internal/devices"
	"github.com/networkgoblinlabs/goblin-gate/internal/dhcpdns"
	"github.com/networkgoblinlabs/goblin-gate/internal/feeds"
	"github.com/networkgoblinlabs/goblin-gate/internal/flows"
	"github.com/networkgoblinlabs/goblin-gate/internal/gateways"
	"github.com/networkgoblinlabs/goblin-gate/internal/geoip"
	"github.com/networkgoblinlabs/goblin-gate/internal/gitbackup"
	"github.com/networkgoblinlabs/goblin-gate/internal/ips"
	"github.com/networkgoblinlabs/goblin-gate/internal/logexport"
	"github.com/networkgoblinlabs/goblin-gate/internal/logs"
	"github.com/networkgoblinlabs/goblin-gate/internal/notify"
	"github.com/networkgoblinlabs/goblin-gate/internal/plugins"
	"github.com/networkgoblinlabs/goblin-gate/internal/render"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/kea"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/netif"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/nft"
	nutrender "github.com/networkgoblinlabs/goblin-gate/internal/render/nut"
	sqmrender "github.com/networkgoblinlabs/goblin-gate/internal/render/sqm"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/suricata"
	sysrender "github.com/networkgoblinlabs/goblin-gate/internal/render/system"
	tsrender "github.com/networkgoblinlabs/goblin-gate/internal/render/tailscale"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/unbound"
	upnprender "github.com/networkgoblinlabs/goblin-gate/internal/render/upnp"
	wgrender "github.com/networkgoblinlabs/goblin-gate/internal/render/wireguard"
	"github.com/networkgoblinlabs/goblin-gate/internal/stats"
	"github.com/networkgoblinlabs/goblin-gate/internal/tailscale"
	"github.com/networkgoblinlabs/goblin-gate/internal/upnp"
	"github.com/networkgoblinlabs/goblin-gate/internal/ups"
	"github.com/networkgoblinlabs/goblin-gate/internal/version"
	"github.com/networkgoblinlabs/goblin-gate/internal/webfilter"
	"github.com/networkgoblinlabs/goblin-gate/internal/wireguard"
)

type options struct {
	listen    string
	configDir string
	dev       bool
	version   bool
	webDir    string
	logSrc    string
	demo      bool          // task 116: public, self-resetting showcase (implies dev)
	demoReset time.Duration // exit after this long in demo mode; 0 = never
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, nil))
}

func run(args []string, stdout, stderr io.Writer, ready chan<- string) int {
	// goblind ips-rules <instance>: build one Suricata instance's rules
	// (ExecStartPre/ExecReload of goblin-suricata@.service).
	if len(args) > 0 && args[0] == "ips-rules" {
		if len(args) != 2 {
			fmt.Fprintln(stderr, "usage: goblind ips-rules <instance>")
			return 2
		}
		st, err := ips.BuildRules(ips.DefaultPaths, args[1])
		if err != nil {
			fmt.Fprintf(stderr, "goblind: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "goblind: %s: %d drop and %d alert rules (%d skipped)\n", args[1], st.Drop, st.Alert, st.Skipped)
		return 0
	}
	// goblind tailscale-apply: push the rendered Tailscale settings to
	// tailscaled (the applier's activation command for task 041).
	if len(args) > 0 && args[0] == "tailscale-apply" {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := tailscale.ApplyFile(ctx, &tailscale.Client{}, tailscale.PrefsPath); err != nil {
			fmt.Fprintf(stderr, "goblind: %v\n", err)
			return 1
		}
		return 0
	}
	// goblind update-run --to <version> [--os]: install an update with
	// health check and rollback (started in a transient unit, task 051).
	if len(args) > 0 && args[0] == "update-run" {
		return runUpdate(args[1:], stdout, stderr)
	}
	opts, err := parseFlags(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if opts.version {
		fmt.Fprintf(stdout, "goblind %s\n", version.Version)
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := serve(ctx, opts, ready); err != nil {
		fmt.Fprintf(stderr, "goblind: %v\n", err)
		return 1
	}
	return 0
}

// tls12Suites are the TLS 1.2 cipher suites offered: forward secret and
// authenticated encryption only (Go's defaults add CBC-SHA suites). TLS 1.3
// suites are not configurable and all fine.
var tls12Suites = []uint16{
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256, tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
	tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256, tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
}

func parseFlags(args []string, stderr io.Writer) (options, error) {
	var o options
	fs := flag.NewFlagSet("goblind", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.listen, "listen", ":8443", "HTTP listen address")
	fs.StringVar(&o.configDir, "config-dir", config.DefaultDir, "configuration directory")
	fs.BoolVar(&o.dev, "dev", false, "development mode: fake applier, config under ~/.goblingate-dev, text logs")
	fs.BoolVar(&o.version, "version", false, "print version and exit")
	fs.StringVar(&o.webDir, "web-dir", "", "directory to serve web UI from (for development)")
	fs.StringVar(&o.logSrc, "log-sources", defaultLogSources(), "log files to ingest: comma-separated nftables=PATH and suricata=PATH; empty = none")
	fs.BoolVar(&o.demo, "demo", false, "public demo: --dev with a seeded network, synthetic events, no outside connections, no account changes")
	fs.DurationVar(&o.demoReset, "demo-reset", time.Hour, "in demo mode, exit after this long so the service restarts from the seed (0 = never)")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "goblind: unexpected argument %q\n", fs.Arg(0))
		return o, fmt.Errorf("unexpected argument")
	}
	return o, nil
}

// serve runs the API until ctx is cancelled. If ready is not nil it receives
// the listening address once the server accepts connections (used by tests).
func serve(ctx context.Context, o options, ready chan<- string) error {
	// Load settings from JSON file if present
	settingsFile := "/etc/goblingate/goblind.json"
	if o.demo {
		settingsFile = "" // the demo host is not a firewall: its settings don't apply
	}
	settings, err := LoadSettingsFile(settingsFile)
	if err != nil {
		return fmt.Errorf("load settings: %w", err)
	}

	// Override with command-line flags
	if o.listen != ":8443" {
		settings.Listen = o.listen
	}
	if o.configDir != config.DefaultDir {
		settings.ConfigDir = o.configDir
	}
	if o.demo {
		o.dev = true
		tmp, err := os.MkdirTemp("", "goblin-demo-*")
		if err != nil {
			return fmt.Errorf("demo: %w", err)
		}
		defer os.RemoveAll(tmp)
		settings.ConfigDir = tmp
		settings.DataDir = filepath.Join(tmp, "data")
		blockOutbound()
		if o.demoReset > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, o.demoReset)
			defer cancel()
		}
	}

	var logger *slog.Logger
	if o.dev {
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	} else {
		logger = slog.New(slog.NewJSONHandler(os.Stderr, nil))
	}
	slog.SetDefault(logger)

	dir := settings.ConfigDir
	var applier apply.Applier
	if o.dev {
		applier = apply.NewFake()
		if dir == config.DefaultDir {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("dev mode: %w", err)
			}
			dir = filepath.Join(home, ".goblingate-dev")
		}
	} else {
		a, err := apply.NewSystem(logger)
		if err != nil {
			return err
		}
		applier = a
	}

	store, err := config.New(dir)
	if err != nil {
		return err
	}
	// workerConfig is the running config as the background workers see it;
	// in demo mode without anything that would contact another host.
	workerConfig := store.Running
	if o.demo {
		workerConfig = func() (*config.Config, error) {
			c, err := store.Running()
			if err != nil {
				return nil, err
			}
			return demoScrub(c), nil
		}
	}
	// Threat feeds: downloaded on their schedules into data_dir/feeds. The
	// nft sets (task 028) and the DNS filter zones (task 029) are filled
	// from them at runtime; a ruleset load empties the sets, so both
	// syncers also run after every apply.
	dataDir := settings.DataDir
	if o.dev && dataDir == DefaultSettings().DataDir {
		dataDir = filepath.Join(dir, "data")
	}
	if o.demo {
		if err := seedDemo(store, dataDir, time.Now()); err != nil {
			return fmt.Errorf("demo: %w", err)
		}
	}
	feedMgr := &feeds.Manager{Dir: filepath.Join(dataDir, "feeds"), Logger: logger}
	feedSync := &feeds.NFTSync{Logger: logger}
	dnsSync := &feeds.DNSSync{Feeds: feedMgr, Logger: logger}
	// Dynamic DNS (task 087): updates cloudflare/duckdns/custom entries when
	// the public IP changes. Not in --dev (the API answers 503).
	var ddnsMgr *ddns.Manager
	if !o.dev {
		ddnsMgr = &ddns.Manager{Updater: &ddns.Updater{}, Logger: logger}
	}
	// Log export (task 096): forwards events to remote syslog / Loki. Not in
	// --dev (the API answers 503).
	var logExporter *logexport.Exporter
	if !o.dev {
		logExporter = &logexport.Exporter{Logger: logger}
	}
	if o.dev {
		feedSync.Run = func(context.Context, []byte) error { return nil }
		dnsSync.Write = func(string, []byte) error { return nil }
		dnsSync.Reload = func(context.Context, string) error { return nil }
	}
	feedMgr.OnUpdate = func(name string, entries []string) {
		feedSync.Update(name, entries)
		dnsSync.Update(name)
	}

	renderers := []commit.Renderer{netif.Renderer{}, nft.Renderer{}, kea.Renderer{}, unbound.Renderer{Feeds: feedMgr}, suricata.Renderer{}, tsrender.Renderer{}, wgrender.Renderer{}, sysrender.Renderer{}, upnprender.Renderer{}, sqmrender.Renderer{},
		nutrender.Renderer{Secret: nutSecret(filepath.Join(dataDir, "nut", "monitor.secret"))}}
	engine := commit.New(store, renderers, applier)
	engine.Logger = logger
	// Web filter (task 033): goblind answers the DNS of web-filtered
	// subnets itself; its events go to the log store once it is open.
	var logStore *logs.Store
	wf := &webfilter.Filter{Logger: logger, Event: func(e logs.Event) {
		if logStore != nil {
			if err := logStore.InsertBatch([]*logs.Event{&e}); err != nil {
				logger.Warn("webfilter: storing event failed", "error", err)
			}
		}
	}}
	// WireGuard server keys are created before networkd needs them.
	wgKeyDir := config.WireGuardKeyDir
	if o.dev {
		wgKeyDir = filepath.Join(dir, "wireguard")
	}
	engine.BeforeApply = func(cfg *config.Config) error { return wireguard.EnsureKeys(wgKeyDir, cfg) }

	// GeoIP (task 094): the country database behind "country" objects. A
	// commit that uses countries needs it; a ruleset load empties the
	// country sets, so they are refilled after every apply.
	geoMgr := &geoip.Manager{Dir: filepath.Join(dataDir, "geoip"), Logger: logger}
	if o.dev {
		geoMgr.RunNFT = func(context.Context, []byte) error { return nil }
	}
	if err := geoMgr.Load(); err != nil {
		logger.Warn("geoip: stored database unreadable, it will be downloaded again", "error", err)
	}
	// custom nftables rules (task 100) are checked with nft -c, blamed on
	// the rule nft complains about (not in --dev: no nft there)
	checkCustom := func(ctx context.Context, cfg *config.Config) []config.ValidationError {
		if o.dev {
			return nil
		}
		return nft.CheckCustomRules(ctx, cfg, nil)
	}
	engine.CheckCandidate = func(ctx context.Context, cfg *config.Config) []config.ValidationError {
		if errs := checkCustom(ctx, cfg); len(errs) > 0 {
			return errs
		}
		sets, err := nft.CountrySets(cfg)
		if err != nil || len(sets) == 0 {
			return nil
		}
		dctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		if err := geoMgr.Ensure(dctx, cfg.GeoIP.SourceURL); err != nil {
			return []config.ValidationError{{Path: "geoip", Message: "the policies use countries (" + strings.Join(nft.UsedCountries(sets), ", ") +
				"), but the GeoIP database could not be downloaded, so they would match nothing: " + err.Error()}}
		}
		return nil
	}
	// Git history (task 098): each saved revision becomes a commit, pushed
	// in the background. Not in --dev (the API answers 503).
	var gitMgr *gitbackup.Manager
	if !o.dev {
		gitMgr = &gitbackup.Manager{Dir: filepath.Join(dataDir, "git-backup"), Store: store, Logger: logger}
		engine.AfterSave = func(live *config.Config, _ int) { gitMgr.Sync(ctx, live) }
		go gitMgr.Run(ctx)
		go func() { // catch up on revisions recorded before a restart
			if cfg, err := workerConfig(); err == nil {
				gitMgr.Sync(ctx, cfg)
			}
		}()
	}
	go geoMgr.Run(ctx, func() *config.Config {
		cfg, err := workerConfig()
		if err != nil {
			return nil
		}
		return cfg
	})
	// DHCP client names in DNS (task 055): kept in Unbound at runtime.
	dhcpNames := &dhcpdns.Syncer{LeaseFile: kea.LeaseFile, Logger: logger, Settings: func() (dhcpdns.Settings, error) {
		cfg, err := workerConfig()
		if err != nil {
			return dhcpdns.Settings{}, err
		}
		return dhcpDNSSettings(cfg), nil
	}}
	if o.dev {
		dhcpNames.Control = func(context.Context, ...string) error { return nil }
	}
	// Multi-WAN (task 049): the gateway monitor owns the default route and
	// the policy-routing tables once gateways are configured.
	gwMon := &gateways.Monitor{Pinger: gateways.ExecPinger{}, Router: gateways.ExecRouter{}, Logger: logger}
	// Plugins (docs/dev/plugins.md): goblind starts the enabled ones as
	// sandboxed systemd units after each commit. Not in --dev.
	var pluginMgr *plugins.Manager
	if !o.dev {
		pluginMgr = &plugins.Manager{ManifestDir: plugins.ManifestDir, ProgramDir: plugins.ProgramDir,
			Version: version.Version, Systemd: plugins.Systemctl{}, Logger: logger}
		config.SetPluginCatalog(pluginMgr.Lookup)
	}
	// Temporary blocks: kept in the nft sets block_tmp/block_tmp6, which a
	// ruleset load empties, so they are put back after every apply.
	blockMgr := &blocks.Manager{Path: filepath.Join(dataDir, "blocks.json"), Logger: logger}
	if o.dev {
		blockMgr.Run = func(context.Context, []byte) error { return nil }
	}
	// CrowdSec bouncer (task 104): same sets-after-apply pattern
	csMgr := &crowdsec.Manager{Logger: logger,
		Config: func() (config.CrowdSec, error) {
			cfg, err := workerConfig()
			if err != nil {
				return config.CrowdSec{}, err
			}
			return cfg.CrowdSec, nil
		},
		Drops: func(ctx context.Context) (uint64, error) {
			out, err := exec.CommandContext(ctx, "nft", "-j", "list", "counter", "inet", nft.FilterTable, nft.CrowdSecCounter).Output()
			if err != nil {
				return 0, err
			}
			c, err := stats.ParseNFTNamedCounter(out, nft.CrowdSecCounter)
			return c.Packets, err
		},
	}
	if o.dev {
		csMgr.RunNFT = func(context.Context, []byte) error { return nil }
		csMgr.Drops = nil
	}
	go csMgr.Run(ctx)
	// live flows and bandwidth (task 078): local networks follow the running
	// config (set after every apply)
	var flowSampler *flows.Sampler
	if !o.dev && runtime.GOOS == "linux" {
		flowSampler = &flows.Sampler{Logger: logger}
	}
	// the rule updater (started below) fetches ET Pro instead of ET Open
	// while the running config has a code; a commit that changes it updates now
	ipsUpd := &ips.Updater{Paths: ips.DefaultPaths, Logger: logger, ETProCode: func() string {
		if cfg, err := workerConfig(); err == nil {
			return cfg.IPS.ETProCode
		}
		return ""
	}}
	lastETPro := ipsUpd.ETProCode()
	engine.AfterApply = func(live *config.Config) {
		if live.IPS.ETProCode != lastETPro {
			lastETPro = live.IPS.ETProCode
			ipsUpd.Kick()
		}
		blockMgr.Resync(ctx)
		csMgr.Resync(ctx)
		if pluginMgr != nil {
			pluginMgr.Resync(ctx, live)
		}
		if !o.dev {
			if err := gwMon.Update(ctx, live); err != nil {
				logger.Warn("gateways: monitor", "error", err)
			}
		}
		wf.Resync(live)
		dnsSync.Resync(live)
		feedSync.Resync(live)
		geoMgr.Resync(live)
		dhcpNames.Resync(ctx) // a commit restarts Unbound, which drops runtime names
		if flowSampler != nil {
			flowSampler.SetLocalPrefixes(render.InternalPrefixes(live))
		}
	}
	go dhcpNames.Run(ctx)

	// Device inventory (task 070): DHCP leases + neighbour table, refreshed
	// every 60 s; a new MAC publishes a "new_device" notification after the
	// first poll. In --dev the store is not wired to the API (503).
	devStore := &devices.Store{
		Path:      filepath.Join(dataDir, "devices.json"),
		LeaseFile: kea.LeaseFile,
		Logger:    logger,
		Interfaces: func() map[string]string {
			cfg, err := workerConfig()
			if err != nil {
				return nil
			}
			rev := make(map[string]string, len(cfg.Interfaces)+len(cfg.VLANs))
			for name, dev := range render.Devices(cfg) {
				rev[dev] = name
			}
			return rev
		},
	}
	if !o.dev {
		go devStore.Run(ctx)
	}

	// Re-apply the running config so the live data plane matches what is on disk.
	if applied, err := engine.ApplyRunning(ctx); err != nil {
		logger.Warn("apply running config failed", "error", err)
	} else if applied {
		logger.Info("running config applied on start")
	}
	if !o.dev {
		go watchScheduleOffset(ctx, store, engine, logger)
	}

	apiServer := api.NewServer(store, engine)
	apiServer.GeoIP = geoMgr
	apiServer.CheckCustom = checkCustom
	apiServer.CrowdSec = csMgr
	apiServer.Dismissed = &checks.Dismissals{Path: filepath.Join(dataDir, "checks", "dismissed.json")}
	apiServer.GitBackup = gitMgr
	apiServer.Logger = logger

	// Notifications (task 053): alerts about commits, lockouts, feed errors,
	// IPS blocks and WANs go to the channels the running config names; each
	// delivery is recorded as a system event in the log store.
	notifier := &notify.Dispatcher{
		Logger: logger,
		Config: func() (config.Notifications, error) {
			cfg, err := workerConfig()
			if err != nil {
				return config.Notifications{}, err
			}
			return cfg.Notifications, nil
		},
		Hostname: func() string {
			if cfg, err := workerConfig(); err == nil {
				return cfg.System.Hostname
			}
			return ""
		},
		Delivered: func(ev notify.Event, channels []string) {
			if logStore == nil {
				return
			}
			e := &logs.Event{Time: ev.Time.Unix(), Kind: "system", Action: ev.Kind,
				Message: ev.Title + " (sent to " + strings.Join(channels, ", ") + ")"}
			if err := logStore.InsertBatch([]*logs.Event{e}); err != nil {
				logger.Warn("notify: storing event failed", "error", err)
			}
		},
	}
	go notifier.Run(ctx)
	apiServer.Notify = notifier
	devStore.Notify = func(kind, key, title, message string) {
		notifier.Publish(notify.Event{Kind: kind, Key: key, Title: title, Message: message})
	}
	// UPS (task 112): status, power notifications, and the shutdown at the
	// owner's thresholds for a remote NUT server
	upsMgr := &ups.Manager{Logger: logger,
		Config: func() (config.UPS, error) {
			cfg, err := workerConfig()
			if err != nil {
				return config.UPS{}, err
			}
			return cfg.UPS, nil
		},
		Publish: func(kind, title, message string) {
			notifier.Publish(notify.Event{Kind: kind, Key: "ups", Title: title, Message: message})
		},
	}
	if o.dev {
		upsMgr.Shutdown = func(context.Context) error {
			logger.Warn("ups: would shut down now (dev mode: not really)")
			return nil
		}
	}
	go upsMgr.Run(ctx)
	apiServer.UPS = upsMgr
	apiServer.Gateways = gwMon
	apiServer.Plugins = pluginMgr
	apiServer.Blocks = blockMgr
	if !o.dev {
		apiServer.Devices = devStore // dev mode: no store, the API answers 503
		apiServer.LiveAddrs = blocks.LiveAddrs
	}
	if !o.dev {
		apiServer.Reboot = func() error { return exec.Command("systemctl", "reboot").Run() }
	}
	if o.demo {
		apiServer.Devices = devStore // the seeded inventory, never refreshed
		apiServer.Demo = true
	}
	if !o.dev && runtime.GOOS == "linux" {
		// the last capture's pcap stays here until the next one (task 076)
		apiServer.Capture = &capture.Manager{Dir: filepath.Join(dataDir, "capture")}
		apiServer.Flows = flowSampler
		flowSampler.Names = deviceNames(devStore)
		go flowSampler.Run(ctx)
	}
	gwMon.SetChanged(func(st gateways.Status) {
		kind, title := "wan_up", "Gateway "+st.Name+" is up again"
		if st.State == "down" {
			kind, title = "wan_down", "Gateway "+st.Name+" is down"
		}
		msg := fmt.Sprintf("%s (%s, monitor %s): %s", title, st.Interface, st.MonitorIP, st.Reason)
		notifier.Publish(notify.Event{Kind: kind, Key: st.Name, Title: title, Message: msg})
		if logStore != nil {
			e := &logs.Event{Time: time.Now().Unix(), Kind: "system", Action: kind, InIntf: st.Device, Message: msg}
			if err := logStore.InsertBatch([]*logs.Event{e}); err != nil {
				logger.Warn("gateways: storing event failed", "error", err)
			}
		}
	})
	feedMgr.OnError = func(name string, err error) {
		notifier.Publish(notify.Event{Kind: "feed_error", Key: name, Title: "Threat feed download failed",
			Message: fmt.Sprintf("Feed %q: %v. Its last good copy stays in use.", name, err)})
	}
	ipsBatch := &notify.IPSBatcher{D: notifier}
	go ipsBatch.Run(ctx)

	// Updates (task 051): check the channel every 6 hours and say so once
	// per new version; installing goes through POST /api/v1/system/updates/apply.
	if !o.dev {
		upd := updateManager(logger)
		apiServer.Updates = upd
		go func() {
			told := ""
			for {
				if st := upd.Check(ctx, 0); st.Available != "" && st.Available != told {
					told = st.Available
					notifier.Publish(notify.Event{Kind: "update_available", Key: st.Available,
						Title:   "Update available: " + st.Available,
						Message: "Goblin Gate " + st.Available + " is available (installed: " + st.Installed + "). Install it on the System page. " + st.ReleaseURL})
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(6 * time.Hour):
				}
			}
		}()
	}

	// IPS: refresh the rules (ET Open, or ET Pro with a code, task 114)
	// daily and reload the Suricata instances.
	if !o.dev {
		go ipsUpd.Run(ctx)
		apiServer.IPSRules = ipsUpd
	}

	go feedMgr.Run(ctx, func() ([]config.Feed, error) {
		cfg, err := workerConfig()
		if err != nil {
			return nil, err
		}
		return cfg.Feeds, nil
	})
	apiServer.Feeds = feedMgr

	if ddnsMgr != nil {
		go ddnsMgr.Run(ctx, func() []config.DDNS {
			cfg, err := workerConfig()
			if err != nil {
				return nil
			}
			return cfg.DDNS
		})
		apiServer.DDNS = ddnsMgr
	}

	if logExporter != nil {
		go logExporter.Run(ctx, func() []config.LogExport {
			cfg, err := workerConfig()
			if err != nil {
				return nil
			}
			return cfg.LogExport
		})
		apiServer.LogExport = logExporter
	}

	// Stats: the event store, policy counters and system load.
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return fmt.Errorf("data dir: %w", err)
	}
	logStore, err = logs.NewStore(filepath.Join(dataDir, "logs.db"))
	if err != nil {
		return err
	}
	if o.demo {
		go runDemoEvents(ctx, logStore)
	}
	defer logStore.Close()
	go runLogMaintenance(ctx, logStore, logger)
	// UPnP / NAT-PMP mappings (task 059) are logged as system events.
	if !o.dev {
		go upnp.Watch(ctx, upnprender.LeaseFile, 30*time.Second, func(m upnp.Mapping, added bool) {
			action, verb := "upnp_mapping_added", "opened"
			if !added {
				action, verb = "upnp_mapping_removed", "closed"
			}
			e := &logs.Event{Time: time.Now().Unix(), Kind: "system", Action: action, Proto: m.Protocol,
				DstIP: m.InternalIP, DstPort: m.InternalPort,
				Message: fmt.Sprintf("UPnP %s %s port %d -> %s:%d (%s)", verb, m.Protocol, m.ExternalPort, m.InternalIP, m.InternalPort, m.Description)}
			if err := logStore.InsertBatch([]*logs.Event{e}); err != nil {
				logger.Warn("upnp: storing event failed", "error", err)
			}
		})
	}
	sources, err := parseLogSources(o.logSrc)
	if err != nil {
		return err
	}
	// Every event goes to the IPS batcher and, when configured, to the log
	// export destinations (task 096).
	onEvent := func(ev *logs.Event) { ipsBatch.Observe(ev) }
	if logExporter != nil {
		onEvent = func(ev *logs.Event) {
			ipsBatch.Observe(ev)
			logExporter.Observe(ev)
		}
	}
	ingester := &logs.Ingester{Store: logStore, PosDir: dataDir, Logger: logger, PolicyAction: policyActions(store), InterfaceFor: interfaceForAddr(store), OnEvent: onEvent}
	go ingester.Run(ctx, sources)
	var counters interface {
		stats.CounterSource
		stats.FeedCounterSource
		stats.CustomCounterSource
	} = stats.NFTCounters{}
	if o.dev {
		counters = &stats.FakeCounters{}
	}
	apiServer.Logs = logStore
	apiServer.Policies = &stats.PolicyTracker{Source: counters}
	apiServer.FeedHits = counters
	apiServer.CustomHits = counters
	apiServer.UI = &api.UIStore{Dir: filepath.Join(dataDir, "ui")}
	apiServer.WebFilter = wf
	apiServer.WireGuardKeyDir = wgKeyDir
	if !o.dev {
		apiServer.Tailscale = &tailscale.Client{}
		apiServer.WireGuardStats = api.WireGuardStats
	}
	if err := startWebFilter(ctx, wf, o.dev, filepath.Join(dataDir, "webfilter"), store, logger); err != nil {
		return err
	}
	apiServer.System = &stats.SystemSampler{}

	if o.webDir != "" {
		apiServer.Static = api.StaticDir(o.webDir)
	}

	// Authentication and TLS. --dev serves plain HTTP with no login.
	listen := settings.Listen
	var tlsCfg *tls.Config
	if !o.dev {
		apiServer.Authn = api.NewAuthenticator(filepath.Join(dataDir, "tokens.json"), filepath.Join(dataDir, "audit.log"))
		running, err := workerConfig()
		if err != nil {
			return err
		}
		if listen == DefaultSettings().Listen { // not set by flag or settings file
			port := running.System.AdminPort
			if port == 0 {
				port = 443
			}
			listen = ":" + strconv.Itoa(port)
		}
		cert, err := auth.EnsureCert(filepath.Join(dataDir, "tls"), certNames(running))
		if err != nil {
			return err
		}
		tlsCfg = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12, CipherSuites: tls12Suites}
	}
	handler := apiServer.Routes()
	if o.demo {
		handler = demoGuard(handler)
	}

	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", listen, err)
	}
	if tlsCfg != nil {
		ln = tls.NewListener(ln, tlsCfg)
	}
	newServer := func() *http.Server {
		return &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      60 * time.Second,
			IdleTimeout:       120 * time.Second,
			ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
		}
	}
	srv := newServer()
	servers := []*http.Server{srv}
	errc := make(chan error, 3)
	go func() { errc <- srv.Serve(ln) }()

	// The local CLI talks over a unix socket only root can open; requests
	// on it act as root/admin without a login.
	if !o.dev && runtime.GOOS == "linux" {
		sockLn, err := listenSocket(SocketPath)
		if err != nil {
			logger.Warn("local API socket unavailable", "path", SocketPath, "error", err)
		} else {
			sockSrv := newServer()
			sockSrv.ConnContext = api.WithSocketConn
			servers = append(servers, sockSrv)
			go func() { errc <- sockSrv.Serve(sockLn) }()
		}
		// Plugins call goblind on their own socket, which serves only the
		// plugin routes; the caller is identified by its systemd unit.
		if pluginMgr != nil {
			plugLn, err := listenPluginSocket(plugins.SocketPath)
			if err != nil {
				logger.Warn("plugin API socket unavailable", "path", plugins.SocketPath, "error", err)
			} else {
				plugSrv := newServer()
				plugSrv.Handler = apiServer.PluginRoutes()
				plugSrv.ConnContext = api.PluginConnContext(plugins.PeerUnit)
				servers = append(servers, plugSrv)
				go func() { errc <- plugSrv.Serve(plugLn) }()
			}
		}
	}

	logger.Info("goblind started", "version", version.Version, "listen", ln.Addr().String(), "tls", tlsCfg != nil, "config_dir", dir, "dev", o.dev)
	if ready != nil {
		ready <- ln.Addr().String()
	}

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	logger.Info("goblind shutting down")
	var firstErr error
	for _, s := range servers {
		if err := s.Shutdown(shutdownCtx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// SocketPath is the local API socket used by the goblin CLI on the box.
const SocketPath = "/run/goblind/api.sock"

// listenSocket creates the unix socket with mode 0600 (root only).
func listenSocket(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	_ = os.Remove(path) // stale socket from a previous run
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}

// listenPluginSocket creates the plugin socket. Plugins run as dynamic
// users, so anyone may connect; goblind answers only processes of running
// plugin units (plugins.PeerUnit).
func listenPluginSocket(path string) (net.Listener, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		return nil, err
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o666); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}

// certNames are the self-signed certificate's SANs: the hostname and the
// static interface addresses.
func certNames(cfg *config.Config) []string {
	names := []string{cfg.System.Hostname}
	if cfg.System.Domain != "" {
		names = append(names, cfg.System.Hostname+"."+cfg.System.Domain)
	}
	for _, i := range cfg.Interfaces {
		if i.Address != nil {
			names = append(names, i.Address.Addr().String())
		}
	}
	for _, v := range cfg.VLANs {
		if v.Address != nil {
			names = append(names, v.Address.Addr().String())
		}
	}
	return names
}

// LogRetentionDays is how long raw events are kept.
const LogRetentionDays = 14

// runLogMaintenance rolls up each finished minute and applies retention
// hourly until ctx ends.
func runLogMaintenance(ctx context.Context, store *logs.Store, logger *slog.Logger) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for n := 0; ; n++ {
		if err := store.Rollup(); err != nil {
			logger.Warn("logs: rollup failed", "error", err)
		}
		if n%60 == 0 {
			if err := store.Retention(LogRetentionDays); err != nil {
				logger.Warn("logs: retention failed", "error", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func defaultLogSources() string {
	return "nftables=" + logs.NftablesLog + ",suricata=" + logs.SuricataLog
}

// parseLogSources turns "nftables=PATH,suricata=PATH" into sources.
func parseLogSources(spec string) ([]logs.Source, error) {
	parsers := map[string]func(string) (*logs.Event, error){"nftables": logs.ParseNftables, "suricata": logs.ParseSuricata}
	var out []logs.Source
	for _, item := range strings.Split(spec, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		name, path, ok := strings.Cut(item, "=")
		parse := parsers[name]
		if !ok || parse == nil || path == "" {
			return nil, fmt.Errorf("--log-sources: %q is not nftables=PATH or suricata=PATH", item)
		}
		out = append(out, logs.Source{Name: name, Path: path, Parse: parse})
	}
	return out, nil
}

// policyActions maps a policy id to its action ("accept"/"deny") from the
// running config, re-read at most every 10 seconds.
func policyActions(store *config.Store) func(string) string {
	var (
		mu      sync.Mutex
		actions map[string]string
		loaded  time.Time
	)
	return func(id string) string {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(loaded) > 10*time.Second {
			if cfg, err := store.Running(); err == nil {
				actions = make(map[string]string, len(cfg.Policies))
				for _, p := range cfg.Policies {
					actions[strconv.FormatUint(uint64(p.ID), 10)] = p.Action
				}
			}
			loaded = time.Now()
		}
		return actions[id]
	}
}

// startWebFilter loads the category database and, outside --dev, serves the
// web filter DNS proxy (udp+tcp WebFilterProxyPort) and the block page
// (tcp 80) on all addresses (nft only lets filtered subnets reach them) and
// keeps the category lists current while a profile uses categories.
func startWebFilter(ctx context.Context, wf *webfilter.Filter, dev bool, dir string, store *config.Store, logger *slog.Logger) error {
	if cfg, err := store.Running(); err == nil {
		wf.Resync(cfg)
	}
	up := &webfilter.Updater{Dir: dir, Filter: wf, Logger: logger, Wanted: func() bool {
		cfg, err := store.Running()
		if err != nil {
			return false
		}
		for _, p := range cfg.Profiles.WebFilter {
			if len(p.Categories) > 0 {
				return true
			}
		}
		return false
	}}
	if dev {
		up.Wanted = func() bool { return false } // no downloads in dev; a saved database still loads
		go up.Run(ctx)
		return nil
	}
	go up.Run(ctx)
	addr := fmt.Sprintf(":%d", webfilter.ProxyPort)
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("web filter dns (udp %s): %w", addr, err)
	}
	tl, err := net.Listen("tcp", addr)
	if err != nil {
		pc.Close()
		return fmt.Errorf("web filter dns (tcp %s): %w", addr, err)
	}
	go func() {
		if err := wf.ServeUDP(ctx, pc); err != nil {
			logger.Error("webfilter: udp proxy stopped", "error", err)
		}
	}()
	go func() {
		if err := wf.ServeTCP(ctx, tl); err != nil {
			logger.Error("webfilter: tcp proxy stopped", "error", err)
		}
	}()
	page := &http.Server{Addr: ":80", Handler: webfilter.NewBlockPage(wf), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := page.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Warn("webfilter: block page server stopped", "error", err)
		}
	}()
	go func() { <-ctx.Done(); page.Close() }()
	return nil
}

// dhcpDNSSettings is what the DHCP name syncer needs from the running config:
// the local domain, whether it is on, and the names already defined there.
func dhcpDNSSettings(cfg *config.Config) dhcpdns.Settings {
	domain := strings.ToLower(cfg.EffectiveLocalDomain())
	// the box's own name: a client must not take it over
	taken := map[string]bool{strings.ToLower(cfg.System.Hostname) + "." + domain: true}
	for name := range cfg.DNS.LocalRecords {
		taken[strings.ToLower(strings.TrimSuffix(name, "."))] = true
	}
	for _, d := range cfg.DHCP {
		for _, l := range d.StaticLeases {
			if l.Hostname != "" {
				taken[strings.ToLower(l.Hostname)+"."+domain] = true
			}
		}
	}
	return dhcpdns.Settings{Enabled: !cfg.DNS.NoDHCPNames, Domain: domain, Taken: taken}
}

// deviceNames names flow addresses from the device inventory, re-reading it
// at most every 10 s (the sampler asks once per flow).
func deviceNames(store *devices.Store) func(netip.Addr) (flows.Device, bool) {
	var (
		mu    sync.Mutex
		index map[netip.Addr]devices.Device
		at    time.Time
	)
	return func(a netip.Addr) (flows.Device, bool) {
		mu.Lock()
		defer mu.Unlock()
		if index == nil || time.Since(at) > 10*time.Second {
			index, at = store.ByIP(), time.Now()
		}
		d, ok := index[a]
		if !ok {
			return flows.Device{}, false
		}
		name := d.Name
		if name == "" {
			name = d.Hostname
		}
		return flows.Device{MAC: d.MAC, Name: name}, true
	}
}
