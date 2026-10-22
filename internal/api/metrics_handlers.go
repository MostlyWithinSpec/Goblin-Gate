package api

import (
	"math"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/auth"
	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/dhcpdns"
	"github.com/networkgoblinlabs/goblin-gate/internal/render"
	"github.com/networkgoblinlabs/goblin-gate/internal/render/kea"
	"github.com/networkgoblinlabs/goblin-gate/internal/version"
)

// sysClassNet is where interface counters are read; tests replace it.
var sysClassNet = "/sys/class/net"

// GET /metrics → Prometheus text format 0.0.4. Off unless system.metrics is
// enabled (404); then open to clients inside system.metrics.allow and to
// requests with a valid API token, 403 for everyone else. It sits outside
// the session check: scrapers have no session.
func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.Store.Running()
	if err != nil {
		s.internal(w, "read running config", err)
		return
	}
	if !cfg.System.Metrics.Enabled {
		writeError(w, http.StatusNotFound, "not_found", "metrics are turned off (System → Settings → Metrics)", nil)
		return
	}
	if !s.metricsAllowed(r, cfg) {
		writeError(w, http.StatusForbidden, "forbidden", "this address is not in the metrics allow list and no valid API token was sent", nil)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Write([]byte(s.renderMetrics(r, cfg)))
}

func (s *Server) metricsAllowed(r *http.Request, cfg *config.Config) bool {
	if s.Authn == nil || r.Context().Value(socketKey{}) == true {
		return true
	}
	if ip, err := netip.ParseAddr(clientAddr(r)); err == nil {
		ip = ip.Unmap()
		for _, c := range cfg.System.Metrics.Allow {
			if p, err := netip.ParsePrefix(c); err == nil && p.Contains(ip) {
				return true
			}
		}
	}
	if bearer, ok := cutBearer(r.Header.Get("Authorization")); ok {
		if ti, valid := s.Authn.Tokens.Verify(bearer); valid && s.currentRole(ti.User) != "" {
			ip, err := netip.ParseAddr(clientAddr(r))
			id := auth.Identity{Role: ti.Role, Scopes: ti.Scopes}
			return err == nil && ti.AllowsAddr(ip) && id.Has(auth.ScopeRead)
		}
	}
	return false
}

// promWriter writes the exposition format: one HELP and TYPE per family,
// then its samples.
type promWriter struct {
	b strings.Builder
}

func (p *promWriter) family(name, typ, help string) {
	p.b.WriteString("# HELP " + name + " " + strings.NewReplacer(`\`, `\\`, "\n", `\n`).Replace(help) + "\n")
	p.b.WriteString("# TYPE " + name + " " + typ + "\n")
}

// sample writes name{k="v",...} value; labels are key, value pairs.
func (p *promWriter) sample(name string, value float64, labels ...string) {
	p.b.WriteString(name)
	if len(labels) > 0 {
		p.b.WriteByte('{')
		for i := 0; i+1 < len(labels); i += 2 {
			if i > 0 {
				p.b.WriteByte(',')
			}
			p.b.WriteString(labels[i] + `="` + escapeLabel(labels[i+1]) + `"`)
		}
		p.b.WriteByte('}')
	}
	p.b.WriteString(" " + formatValue(value) + "\n")
}

func escapeLabel(v string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(v)
}

func formatValue(v float64) string {
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

func (s *Server) renderMetrics(r *http.Request, cfg *config.Config) string {
	var p promWriter
	host, _ := os.Hostname()
	p.family("goblin_info", "gauge", "Goblin Gate version and hostname (always 1).")
	p.sample("goblin_info", 1, "version", version.Version, "hostname", host)

	if s.System != nil {
		sys := s.System.Sample()
		p.family("goblin_uptime_seconds", "gauge", "Seconds since the box booted.")
		p.sample("goblin_uptime_seconds", float64(sys.UptimeSeconds))
		p.family("goblin_cpu_usage_ratio", "gauge", "CPU busy share (0-1) since the previous sample.")
		p.sample("goblin_cpu_usage_ratio", sys.CPUPercent/100)
		p.family("goblin_memory_used_bytes", "gauge", "Memory in use (total minus available).")
		p.sample("goblin_memory_used_bytes", float64(sys.MemUsedBytes))
		p.family("goblin_memory_total_bytes", "gauge", "Total memory.")
		p.sample("goblin_memory_total_bytes", float64(sys.MemTotalBytes))
		p.family("goblin_sessions", "gauge", "Tracked connections (conntrack entries).")
		p.sample("goblin_sessions", float64(sys.Sessions))
	}

	s.interfaceMetrics(&p, cfg)

	if s.Policies != nil {
		if ps, err := s.Policies.Read(r.Context()); err == nil {
			names := map[uint32]string{}
			for _, pol := range cfg.Policies {
				names[pol.ID] = pol.Name
			}
			p.family("goblin_policy_hits_total", "counter", "Packets matched per firewall policy (restarts at 0 when a commit reloads the ruleset).")
			for _, st := range ps {
				p.sample("goblin_policy_hits_total", float64(st.Packets), "id", strconv.FormatUint(uint64(st.ID), 10), "name", names[st.ID])
			}
			p.family("goblin_policy_bytes_total", "counter", "Bytes matched per firewall policy (restarts at 0 when a commit reloads the ruleset).")
			for _, st := range ps {
				p.sample("goblin_policy_bytes_total", float64(st.Bytes), "id", strconv.FormatUint(uint64(st.ID), 10), "name", names[st.ID])
			}
		}
	}

	if s.Gateways != nil {
		rep := s.Gateways.Report()
		p.family("goblin_gateway_up", "gauge", "1 when the gateway's monitor says it is up.")
		for _, g := range rep.Gateways {
			p.sample("goblin_gateway_up", boolValue(g.State == "up"), "gateway", g.Name)
		}
		p.family("goblin_gateway_rtt_seconds", "gauge", "Gateway round-trip time to the monitor address.")
		for _, g := range rep.Gateways {
			if g.State != "unknown" {
				p.sample("goblin_gateway_rtt_seconds", float64(g.LatencyMs)/1000, "gateway", g.Name)
			}
		}
		p.family("goblin_gateway_loss_ratio", "gauge", "Gateway packet loss (0-1).")
		for _, g := range rep.Gateways {
			if g.State != "unknown" {
				p.sample("goblin_gateway_loss_ratio", float64(g.LossPercent)/100, "gateway", g.Name)
			}
		}
	}

	if s.Feeds != nil {
		st := s.Feeds.Status()
		p.family("goblin_feed_entries", "gauge", "Entries in the last good copy of each threat feed.")
		for _, f := range st {
			p.sample("goblin_feed_entries", float64(f.Count), "feed", f.Name, "type", f.Type)
		}
		p.family("goblin_feed_last_success_timestamp_seconds", "gauge", "When each threat feed was last downloaded successfully (Unix time).")
		for _, f := range st {
			if !f.FetchedAt.IsZero() {
				p.sample("goblin_feed_last_success_timestamp_seconds", float64(f.FetchedAt.Unix()), "feed", f.Name)
			}
		}
	}

	if s.Logs != nil {
		if n, err := s.Logs.CountByAction("ips", s.started.Unix()); err == nil {
			p.family("goblin_ips_alerts_total", "counter", "IPS alerts since goblind started.")
			p.sample("goblin_ips_alerts_total", float64(n["alert"]))
			p.family("goblin_ips_drops_total", "counter", "Packets the IPS dropped since goblind started.")
			p.sample("goblin_ips_drops_total", float64(n["drop"]))
		}
	}

	if s.Blocks != nil {
		p.family("goblin_temp_blocks", "gauge", "Active temporary IP blocks.")
		p.sample("goblin_temp_blocks", float64(len(s.Blocks.List())))
	}

	s.leaseMetrics(&p, cfg)

	if revs, err := s.Store.ListRevisions(); err == nil {
		latest := 0
		for _, rv := range revs {
			latest = max(latest, rv.Revision)
		}
		p.family("goblin_commits_total", "counter", "Commits ever made, rollbacks included (each one is a new revision).")
		p.sample("goblin_commits_total", float64(latest))
		p.family("goblin_config_revision", "gauge", "Revision number of the running config.")
		p.sample("goblin_config_revision", float64(latest))
	}
	return p.b.String()
}

func boolValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// interfaceMetrics reads the kernel counters of every configured interface
// and VLAN; devices that do not exist are left out.
func (s *Server) interfaceMetrics(p *promWriter, cfg *config.Config) {
	devs := render.Devices(cfg)
	names := make([]string, 0, len(devs))
	for n := range devs {
		names = append(names, n)
	}
	sort.Strings(names)
	type row struct {
		name, dev string
		rx, tx    float64
		up        bool
	}
	var rows []row
	read := func(dev, file string) (string, bool) {
		b, err := os.ReadFile(filepath.Join(sysClassNet, dev, file))
		return strings.TrimSpace(string(b)), err == nil
	}
	for _, n := range names {
		dev := devs[n]
		rx, ok1 := read(dev, "statistics/rx_bytes")
		tx, ok2 := read(dev, "statistics/tx_bytes")
		if !ok1 || !ok2 {
			continue
		}
		rxv, _ := strconv.ParseFloat(rx, 64)
		txv, _ := strconv.ParseFloat(tx, 64)
		state, _ := read(dev, "operstate")
		carrier, _ := read(dev, "carrier")
		rows = append(rows, row{n, dev, rxv, txv, state == "up" || (state == "unknown" && carrier == "1")})
	}
	p.family("goblin_interface_receive_bytes_total", "counter", "Bytes received per interface.")
	for _, r := range rows {
		p.sample("goblin_interface_receive_bytes_total", r.rx, "interface", r.name, "device", r.dev)
	}
	p.family("goblin_interface_transmit_bytes_total", "counter", "Bytes sent per interface.")
	for _, r := range rows {
		p.sample("goblin_interface_transmit_bytes_total", r.tx, "interface", r.name, "device", r.dev)
	}
	p.family("goblin_interface_up", "gauge", "1 when the interface's link is up.")
	for _, r := range rows {
		p.sample("goblin_interface_up", boolValue(r.up), "interface", r.name, "device", r.dev)
	}
}

// leaseMetrics counts active DHCP leases per interface (the interface whose
// subnet holds the address).
func (s *Server) leaseMetrics(p *promWriter, cfg *config.Config) {
	path := s.DHCPLeaseFile
	if path == "" {
		path = kea.LeaseFile
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	recs, err := dhcpdns.ParseLeaseRecords(f, time.Now())
	f.Close()
	if err != nil {
		return
	}
	type subnet struct {
		name string
		p    netip.Prefix
	}
	var subnets []subnet
	for _, ifc := range cfg.Interfaces {
		if ifc.Address != nil {
			subnets = append(subnets, subnet{ifc.Name, ifc.Address.Masked()})
		}
	}
	for _, v := range cfg.VLANs {
		if v.Address != nil {
			subnets = append(subnets, subnet{v.Name, v.Address.Masked()})
		}
	}
	counts := map[string]int{}
	for _, rec := range recs {
		for _, sn := range subnets {
			if sn.p.Contains(rec.IP) {
				counts[sn.name]++
				break
			}
		}
	}
	names := make([]string, 0, len(counts))
	for n := range counts {
		names = append(names, n)
	}
	sort.Strings(names)
	p.family("goblin_dhcp_leases", "gauge", "Active DHCP leases per interface.")
	for _, n := range names {
		p.sample("goblin_dhcp_leases", float64(counts[n]), "interface", n)
	}
}
