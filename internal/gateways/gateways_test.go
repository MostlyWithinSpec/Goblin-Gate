package gateways

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/render/rendertest"
)

// idlePinger never answers on its own: tests feed results with Record.
type idlePinger struct{}

func (idlePinger) Ping(ctx context.Context, _ string, _ time.Duration) (time.Duration, error) {
	<-ctx.Done()
	return 0, ctx.Err()
}

// fakeIP answers "ip -j route show table N default" from routes and
// records every other command.
type fakeIP struct {
	mu     sync.Mutex
	routes map[string]string // table -> JSON
	cmds   []string
}

func (f *fakeIP) Run(_ context.Context, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	line := strings.Join(args, " ")
	if strings.HasPrefix(line, "-j -4 route show table ") {
		table := strings.Fields(line)[5]
		if r, ok := f.routes[table]; ok {
			return []byte(r), nil
		}
		return []byte("[]"), nil
	}
	f.cmds = append(f.cmds, line)
	return nil, nil
}

func (f *fakeIP) take() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.cmds
	f.cmds = nil
	return out
}

func newMonitor(t *testing.T) (*Monitor, *fakeIP, context.Context, *[]Status) {
	t.Helper()
	ip := &fakeIP{routes: map[string]string{
		"100": `[{"dst":"default","gateway":"192.0.2.1","dev":"eth0"}]`,
		"101": `[{"dst":"default","gateway":"198.51.100.1","dev":"eth2"}]`,
		"102": `[{"dst":"default","dev":"vpn","scope":"link"}]`,
	}}
	var changes []Status
	m := &Monitor{Pinger: idlePinger{}, Router: ip, Changed: func(s Status) { changes = append(changes, s) }}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := m.Update(ctx, rendertest.MultiWAN()); err != nil {
		t.Fatal(err)
	}
	return m, ip, ctx, &changes
}

func contains(cmds []string, want string) bool {
	for _, c := range cmds {
		if c == want {
			return true
		}
	}
	return false
}

func TestRoutesAllUp(t *testing.T) {
	_, ip, _, _ := newMonitor(t)
	cmds := ip.take()
	for _, want := range []string{
		"-4 route replace default table 250 via 192.0.2.1 dev eth0",                                                             // failover: tier 1
		"-4 route replace default table 200 via 192.0.2.1 dev eth0",                                                             // wan1
		"-4 route replace default table 201 via 198.51.100.1 dev eth2",                                                          // wan2
		"-4 route replace default table 202 dev vpn",                                                                            // vpn
		"-4 route replace default table 203 via 192.0.2.1 dev eth0",                                                             // failover
		"-4 route replace default table 204 nexthop via 192.0.2.1 dev eth0 weight 2 nexthop via 198.51.100.1 dev eth2 weight 1", // balance
	} {
		if !contains(cmds, want) {
			t.Errorf("missing %q in\n%s", want, strings.Join(cmds, "\n"))
		}
	}
}

func TestFailoverAndKillSwitch(t *testing.T) {
	m, ip, ctx, changes := newMonitor(t)
	ip.take()
	// wan1 stops answering: down once 40% of the window is lost
	for i := 0; i < 3; i++ {
		m.Record("wan1", true, 10*time.Millisecond)
	}
	changed := false
	for i := 0; i < 2; i++ {
		changed = m.Record("wan1", false, 0) || changed
	}
	if !changed || m.Report().Gateways[0].State != "down" {
		t.Fatalf("wan1 should be down: %+v", m.Report().Gateways[0])
	}
	// the tunnel loses its route (link down)
	ip.mu.Lock()
	delete(ip.routes, "102")
	ip.mu.Unlock()
	m.apply(ctx)
	cmds := ip.take()
	for _, want := range []string{
		"-4 route replace default table 250 via 198.51.100.1 dev eth2", // default fails over to wan2
		"-4 route replace default table 203 via 198.51.100.1 dev eth2", // failover group too
		"-4 route replace default table 204 via 198.51.100.1 dev eth2", // balance: only wan2 left
		"-4 route replace default table 200 via 198.51.100.1 dev eth2", // wan1 target: falls back to the default
		"-4 route replace blackhole default table 202",                 // vpn: kill switch, no leak
	} {
		if !contains(cmds, want) {
			t.Errorf("missing %q in\n%s", want, strings.Join(cmds, "\n"))
		}
	}
	r := m.Report()
	if r.Default.Active[0] != "wan2" || r.Targets[2].Mode != "blackhole" {
		t.Errorf("report: %+v", r)
	}
	if len(*changes) != 1 || (*changes)[0].Name != "wan1" || (*changes)[0].State != "down" {
		t.Errorf("changes: %+v", *changes)
	}
	// wan1 recovers once the loss is at most half the threshold
	for i := 0; i < 5; i++ {
		m.Record("wan1", true, 10*time.Millisecond)
	}
	if m.Report().Gateways[0].State != "up" {
		t.Fatalf("wan1 should be up again: %+v", m.Report().Gateways[0])
	}
	if last := (*changes)[len(*changes)-1]; last.State != "up" {
		t.Errorf("no up event: %+v", *changes)
	}
}

func TestLatency(t *testing.T) {
	m, _, _, _ := newMonitor(t)
	for i := 0; i < 5; i++ {
		m.Record("wan2", true, 1500*time.Millisecond) // threshold 1000 ms
	}
	if s := m.Report().Gateways[1]; s.State != "down" || !strings.Contains(s.Reason, "latency") {
		t.Fatalf("slow gateway: %+v", s)
	}
}

func TestNoGateways(t *testing.T) {
	ip := &fakeIP{}
	m := &Monitor{Pinger: idlePinger{}, Router: ip}
	cfg := rendertest.MultiWAN()
	cfg.Gateways, cfg.GatewayGroups, cfg.DefaultGateway, cfg.Routes = nil, nil, "", nil
	for i := range cfg.Policies {
		cfg.Policies[i].Gateway = ""
	}
	if err := m.Update(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if cmds := ip.take(); len(cmds) != 0 || m.Report().Enabled {
		t.Fatalf("single WAN must not touch routes: %v", cmds)
	}
}

func TestStats(t *testing.T) {
	loss, lat := stats([]sample{{true, 10 * time.Millisecond}, {false, 0}, {true, 30 * time.Millisecond}, {false, 0}})
	if loss != 50 || lat != 20 {
		t.Fatalf("loss %d latency %d", loss, lat)
	}
}

// Before any gateway is configured the report's lists are [] in JSON, not
// null: the web UI reads their length.
func TestReportEmptyListsNotNull(t *testing.T) {
	b, err := json.Marshal((&Monitor{}).Report())
	if err != nil {
		t.Fatal(err)
	}
	if s := string(b); strings.Contains(s, "null") {
		t.Fatalf("report has null: %s", s)
	}
}
