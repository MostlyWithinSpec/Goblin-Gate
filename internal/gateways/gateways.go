// Package gateways is goblind's multi-WAN gateway monitor (task 049): it
// pings each gateway's monitor address through the gateway's own routing
// table, decides up/down with hysteresis, and writes the routes of the
// routing targets and the default route (tables in internal/render/
// routing.go): the lowest tier with a gateway up carries the traffic,
// shared by weight; a kill-switch tunnel that is down gets a blackhole.
package gateways

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render"
)

// Window is how many recent pings decide a gateway's state.
const Window = 10

// Pinger sends one echo request and returns the round-trip time.
type Pinger interface {
	Ping(ctx context.Context, ip string, timeout time.Duration) (time.Duration, error)
}

// Router runs ip(8) commands.
type Router interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
}

// Status of one gateway, for the API.
type Status struct {
	Name        string    `json:"name"`
	Interface   string    `json:"interface"`
	Device      string    `json:"device"`
	MonitorIP   string    `json:"monitor_ip,omitempty"`
	State       string    `json:"state"` // up | down | unknown (not measured yet)
	LossPercent int       `json:"loss_percent"`
	LatencyMs   int       `json:"latency_ms"`
	Since       time.Time `json:"since,omitzero"`
	Reason      string    `json:"reason,omitempty"`
}

// TargetStatus says which gateways a routing target uses now.
type TargetStatus struct {
	Name   string   `json:"name"`
	Group  bool     `json:"group,omitempty"`
	Active []string `json:"active"` // empty: blackhole (kill switch) or the default route
	Mode   string   `json:"mode"`   // gateways | default | blackhole
}

// Report is GET /api/v1/gateways/status.
type Report struct {
	Enabled  bool           `json:"enabled"`
	Gateways []Status       `json:"gateways"`
	Targets  []TargetStatus `json:"targets"`
	Default  TargetStatus   `json:"default"`
}

type sample struct {
	ok  bool
	rtt time.Duration
}

type gwState struct {
	plan    render.GatewayPlan
	samples []sample
	state   string
	since   time.Time
	reason  string
}

// Monitor watches the gateways of the running config.
type Monitor struct {
	Pinger Pinger
	Router Router
	Logger *slog.Logger
	Now    func() time.Time
	// Changed is called when a gateway goes up or down (set it before
	// Update, or with SetChanged).
	Changed func(st Status)

	mu      sync.Mutex
	plan    render.RoutingPlan
	gws     map[string]*gwState
	order   []string
	targets []TargetStatus
	def     TargetStatus
	cancel  context.CancelFunc
	cfgs    map[string]config.Gateway
}

func (m *Monitor) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Monitor) logger() *slog.Logger {
	if m.Logger != nil {
		return m.Logger
	}
	return slog.Default()
}

// SetChanged sets Changed while the monitor may be running.
func (m *Monitor) SetChanged(f func(Status)) {
	m.mu.Lock()
	m.Changed = f
	m.mu.Unlock()
}

// Update starts or restarts monitoring for a config (after every apply).
func (m *Monitor) Update(ctx context.Context, cfg *config.Config) error {
	plan, err := render.Routing(cfg)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	old := m.gws
	m.plan, m.gws, m.order, m.cfgs = plan, map[string]*gwState{}, nil, map[string]config.Gateway{}
	for _, g := range cfg.Gateways {
		m.cfgs[g.Name] = g
	}
	for _, g := range plan.Gateways {
		st := &gwState{plan: g, state: "unknown", since: m.now()}
		if o, ok := old[g.Name]; ok && o.plan.MonitorIP == g.MonitorIP && o.plan.Device == g.Device {
			st.samples, st.state, st.since, st.reason = o.samples, o.state, o.since, o.reason
		}
		if g.MonitorIP == "" {
			st.state, st.reason = "up", "not monitored"
		}
		m.gws[g.Name] = st
		m.order = append(m.order, g.Name)
	}
	if !plan.Enabled {
		m.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.mu.Unlock()
	m.apply(ctx)
	for _, g := range plan.Gateways {
		if g.MonitorIP != "" {
			go m.watch(ctx, g)
		}
	}
	go m.refresh(ctx)
	return nil
}

// Stop ends monitoring.
func (m *Monitor) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
}

// refresh re-applies the routes every 30 s: a DHCP gateway may have
// changed its router, networkd may have re-added a table.
func (m *Monitor) refresh(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.apply(ctx)
		}
	}
}

func (m *Monitor) watch(ctx context.Context, g render.GatewayPlan) {
	c := m.cfgs[g.Name].Monitor
	interval := time.Duration(c.IntervalSeconds) * time.Second
	if interval <= 0 {
		interval = config.DefaultMonitorInterval * time.Second
	}
	timeout := min(interval, 2*time.Second)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		rtt, err := m.Pinger.Ping(ctx, g.MonitorIP, timeout)
		if ctx.Err() != nil {
			return
		}
		if m.Record(g.Name, err == nil, rtt) {
			m.apply(ctx)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Record adds one ping result and reports whether the gateway's state
// changed. Down: loss at or above the threshold, or average latency above
// it, over the last Window pings (at least 3). Up again: loss at most half
// the threshold and latency within it (hysteresis).
func (m *Monitor) Record(name string, ok bool, rtt time.Duration) bool {
	m.mu.Lock()
	st, exists := m.gws[name]
	if !exists {
		m.mu.Unlock()
		return false
	}
	st.samples = append(st.samples, sample{ok, rtt})
	if len(st.samples) > Window {
		st.samples = st.samples[len(st.samples)-Window:]
	}
	c := m.cfgs[name].Monitor
	lossMax, latMax := c.LossPercent, c.LatencyMs
	if lossMax == 0 {
		lossMax = config.DefaultMonitorLoss
	}
	if latMax == 0 {
		latMax = config.DefaultMonitorLatency
	}
	loss, lat := stats(st.samples)
	prev := st.state
	switch {
	case len(st.samples) < 3 && st.state == "unknown":
		if ok {
			st.state = "up"
		}
	case loss >= lossMax:
		st.state, st.reason = "down", fmt.Sprintf("%d%% packet loss", loss)
	case lat > latMax:
		st.state, st.reason = "down", fmt.Sprintf("latency %d ms", lat)
	case st.state != "up" && loss <= lossMax/2:
		st.state, st.reason = "up", ""
	}
	changed := st.state != prev
	if changed {
		st.since = m.now()
	}
	status := m.statusLocked(st)
	notify := m.Changed
	m.mu.Unlock()
	if changed && prev != "unknown" || changed && status.State == "down" {
		m.logger().Info("gateway "+status.State, "gateway", name, "reason", status.Reason)
		if notify != nil {
			notify(status)
		}
	}
	return changed
}

func stats(s []sample) (lossPercent, latencyMs int) {
	if len(s) == 0 {
		return 0, 0
	}
	lost, n := 0, 0
	var sum time.Duration
	for _, x := range s {
		if !x.ok {
			lost++
			continue
		}
		sum += x.rtt
		n++
	}
	if n > 0 {
		latencyMs = int(sum / time.Duration(n) / time.Millisecond)
	}
	return lost * 100 / len(s), latencyMs
}

func (m *Monitor) statusLocked(st *gwState) Status {
	loss, lat := stats(st.samples)
	return Status{Name: st.plan.Name, Interface: st.plan.Interface, Device: st.plan.Device, MonitorIP: st.plan.MonitorIP,
		State: st.state, LossPercent: loss, LatencyMs: lat, Since: st.since, Reason: st.reason}
}

// Report returns the current state.
func (m *Monitor) Report() Report {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := Report{Enabled: m.plan.Enabled, Gateways: []Status{}, Targets: append([]TargetStatus{}, m.targets...), Default: m.def}
	// lists are [] not null in JSON, also before the first plan
	for i := range r.Targets {
		r.Targets[i].Active = append([]string{}, r.Targets[i].Active...)
	}
	r.Default.Active = append([]string{}, r.Default.Active...)
	for _, n := range m.order {
		r.Gateways = append(r.Gateways, m.statusLocked(m.gws[n]))
	}
	return r
}

// nexthop is one gateway's route as networkd put it in the gateway table.
type nexthop struct {
	Gateway string `json:"gateway"`
	Dev     string `json:"dev"`
}

// lookup reads a gateway's default route from its table; ok is false
// while it has none (link down, no DHCP lease).
func (m *Monitor) lookup(ctx context.Context, g render.GatewayPlan) (nexthop, bool) {
	out, err := m.Router.Run(ctx, "-j", "-4", "route", "show", "table", strconv.Itoa(g.Table), "default")
	if err != nil {
		return nexthop{}, false
	}
	var routes []nexthop
	if json.Unmarshal(out, &routes) != nil || len(routes) == 0 || routes[0].Dev == "" {
		return nexthop{}, false
	}
	return routes[0], true
}

// choose returns the live members of the lowest tier that has any.
func choose(members []config.GroupMember, live map[string]bool) []config.GroupMember {
	tiers := map[int][]config.GroupMember{}
	var order []int
	for _, mb := range members {
		if !live[mb.Gateway] {
			continue
		}
		if _, ok := tiers[mb.Tier]; !ok {
			order = append(order, mb.Tier)
		}
		tiers[mb.Tier] = append(tiers[mb.Tier], mb)
	}
	if len(order) == 0 {
		return nil
	}
	sort.Ints(order)
	return tiers[order[0]]
}

// apply writes the routes of every target and the default route.
func (m *Monitor) apply(ctx context.Context) {
	m.mu.Lock()
	plan := m.plan
	states := map[string]string{}
	for n, st := range m.gws {
		states[n] = st.state
	}
	m.mu.Unlock()
	if !plan.Enabled {
		return
	}
	hops := map[string]nexthop{}
	live := map[string]bool{}
	for _, g := range plan.Gateways {
		h, ok := m.lookup(ctx, g)
		if ok {
			hops[g.Name] = h
		}
		live[g.Name] = ok && states[g.Name] != "down"
	}
	write := func(table int, ms []config.GroupMember) error {
		args := []string{"-4", "route", "replace", "default", "table", strconv.Itoa(table)}
		if len(ms) == 1 {
			args = append(args, hopArgs(hops[ms[0].Gateway])...)
		} else {
			for _, mb := range ms {
				w := mb.Weight
				if w <= 0 {
					w = 1
				}
				args = append(append(append(args, "nexthop"), hopArgs(hops[mb.Gateway])...), "weight", strconv.Itoa(w))
			}
		}
		_, err := m.Router.Run(ctx, args...)
		return err
	}
	blackhole := func(table int) error {
		_, err := m.Router.Run(ctx, "-4", "route", "replace", "blackhole", "default", "table", strconv.Itoa(table))
		return err
	}
	names := func(ms []config.GroupMember) []string {
		out := []string{}
		for _, mb := range ms {
			out = append(out, mb.Gateway)
		}
		return out
	}

	// the default route first: the fallback of every target
	defMembers := choose(plan.Default.Members, live)
	def := TargetStatus{Name: "default", Mode: "gateways"}
	if len(defMembers) == 0 {
		// nothing measured up: use any gateway that has a route rather than none
		reach := map[string]bool{}
		for n := range hops {
			reach[n] = true
		}
		defMembers = choose(plan.Default.Members, reach)
	}
	if len(defMembers) == 0 {
		def.Mode, def.Active = "none", []string{}
		m.Router.Run(ctx, "-4", "route", "flush", "table", strconv.Itoa(render.DefaultTable))
	} else {
		def.Active = names(defMembers)
		if err := write(render.DefaultTable, defMembers); err != nil {
			m.logger().Warn("gateways: default route", "error", err)
		}
	}
	var targets []TargetStatus
	for _, t := range plan.Targets {
		ts := TargetStatus{Name: t.Name, Group: t.Group}
		ms := choose(t.Members, live)
		var err error
		switch {
		case len(ms) > 0:
			ts.Mode, ts.Active = "gateways", names(ms)
			err = write(t.Table, ms)
		case t.KillSwitch:
			ts.Mode, ts.Active = "blackhole", []string{}
			err = blackhole(t.Table)
		case len(defMembers) > 0:
			ts.Mode, ts.Active = "default", def.Active
			err = write(t.Table, defMembers)
		default:
			ts.Mode, ts.Active = "none", []string{}
			m.Router.Run(ctx, "-4", "route", "flush", "table", strconv.Itoa(t.Table))
		}
		if err != nil {
			m.logger().Warn("gateways: route", "target", t.Name, "error", err)
		}
		targets = append(targets, ts)
	}
	m.mu.Lock()
	m.targets, m.def = targets, def
	m.mu.Unlock()
}

func hopArgs(h nexthop) []string {
	if h.Gateway != "" {
		return []string{"via", h.Gateway, "dev", h.Dev}
	}
	return []string{"dev", h.Dev}
}

// ExecRouter runs /sbin/ip.
type ExecRouter struct{}

// Run implements Router.
func (ExecRouter) Run(ctx context.Context, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, "ip", args...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("ip %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// ExecPinger runs ping(8) (iputils): the ip rule for the monitor address
// sends it through the gateway.
type ExecPinger struct{}

var rttRe = regexp.MustCompile(`time[=<]([0-9.]+) ms`)

// Ping implements Pinger.
func (ExecPinger) Ping(ctx context.Context, ip string, timeout time.Duration) (time.Duration, error) {
	secs := max(1, int(timeout/time.Second))
	out, err := exec.CommandContext(ctx, "ping", "-n", "-q", "-c", "1", "-W", strconv.Itoa(secs), ip).CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("no reply from %s", ip)
	}
	// -q prints "rtt min/avg/max/mdev = 1.234/..." instead of per-packet time
	if m := regexp.MustCompile(`= ([0-9.]+)/`).FindSubmatch(out); m != nil {
		f, _ := strconv.ParseFloat(string(m[1]), 64)
		return time.Duration(f * float64(time.Millisecond)), nil
	}
	if m := rttRe.FindSubmatch(out); m != nil {
		f, _ := strconv.ParseFloat(string(m[1]), 64)
		return time.Duration(f * float64(time.Millisecond)), nil
	}
	return 0, nil
}
