package sentry

import (
	"bufio"
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// fakeGoblind records tool calls.
type fakeGoblind struct {
	mu       sync.Mutex
	blocks   map[string]int
	notes    []string
	blockErr error
}

func newFake() *fakeGoblind { return &fakeGoblind{blocks: map[string]int{}} }

func (f *fakeGoblind) Block(_ context.Context, ip string, minutes int, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.blockErr != nil {
		return f.blockErr
	}
	f.blocks[ip] = minutes
	return nil
}

func (f *fakeGoblind) Blocked(context.Context) (map[string]bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := map[string]bool{}
	for ip := range f.blocks {
		m[ip] = true
	}
	return m, nil
}

func (f *fakeGoblind) Notify(_ context.Context, title, msg, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notes = append(f.notes, title+": "+msg)
	return nil
}

func (f *fakeGoblind) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.blocks), len(f.notes)
}

// testNet: LAN 192.168.1.0/24 with a named NAS, WAN 203.0.113.2.
func testNet() *Network {
	cfg := config.Default()
	lan := netip.MustParsePrefix("192.168.1.1/24")
	wan := netip.MustParsePrefix("203.0.113.2/24")
	cfg.Interfaces = []config.Interface{
		{Name: "wan1", Role: "wan", Address: &wan, Enabled: true},
		{Name: "lan", Role: "lan", Address: &lan, Enabled: true},
	}
	cfg.DHCP = []config.DHCPServer{{Interface: "lan", StaticLeases: []config.DHCPStaticLease{{IP: netip.MustParseAddr("192.168.1.10"), Hostname: "nas"}}}}
	return NetworkFrom(cfg)
}

var t0 = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

func newEngine(t *testing.T, mode string) (*Engine, *fakeGoblind, *time.Time) {
	t.Helper()
	g := newFake()
	e, err := New(t.TempDir(), g, Settings{Mode: mode})
	if err != nil {
		t.Fatal(err)
	}
	now := t0
	e.Now = func() time.Time { return now }
	e.SetNetwork(testNet())
	return e, g, &now
}

var nextID int64

// alert: an alert at t0+offset.
func alert(src, dst, sig string, sev int, offset time.Duration) Alert {
	nextID++
	return Alert{ID: nextID, Time: t0.Add(offset).Unix(), Action: "alert", SrcIP: src, DstIP: dst,
		Signature: sig, SignatureID: "200" + sig[:1], Severity: sev}
}

func TestDedupAndEnrichment(t *testing.T) {
	e, _, _ := newEngine(t, ModeShadow)
	ctx := context.Background()
	e.Ingest(ctx, []Alert{
		alert("198.51.100.7", "192.168.1.10", "ET SCAN", 2, 0),
		alert("198.51.100.7", "192.168.1.10", "ET SCAN", 2, 10*time.Minute),
		alert("198.51.100.7", "192.168.1.10", "ET SCAN", 2, 3*time.Hour), // outside the window
		alert("192.168.1.50", "203.0.113.200", "ET MALWARE", 1, time.Minute),
		alert("192.168.1.50", "192.168.1.10", "ET LATERAL", 2, time.Minute),
	})
	incs := e.Incidents("", 0)
	if len(incs) != 4 {
		t.Fatalf("incidents: %d", len(incs))
	}
	first := incs[3] // oldest last
	if first.Count != 2 || first.Enrichment.Direction != Inbound || first.Enrichment.Remote != "198.51.100.7" ||
		first.Enrichment.Local != "192.168.1.10" || first.Enrichment.LocalName != "nas" || first.Enrichment.LocalIntf != "lan" {
		t.Fatalf("first incident: %+v", first)
	}
	if !strings.Contains(first.Summary, "ET SCAN from 198.51.100.7 to nas (192.168.1.10), 2 times") {
		t.Fatalf("summary: %q", first.Summary)
	}
	again := incs[2] // newest first: lateral, malware, the repeat, the first
	if again.Enrichment.Seen != 1 || again.Enrichment.RemoteSeen != 1 {
		t.Fatalf("history not counted: %+v", again.Enrichment)
	}
	if out := incs[1]; out.Enrichment.Direction != Outbound || out.Enrichment.Remote != "203.0.113.200" || out.Enrichment.Local != "192.168.1.50" {
		t.Fatalf("outbound: %+v", out.Enrichment)
	}
	if lat := incs[0]; lat.Enrichment.Direction != Lateral || lat.Enrichment.Remote != "" {
		t.Fatalf("lateral: %+v", lat.Enrichment)
	}
	if e.Cursor() != nextID {
		t.Fatalf("cursor %d, want %d", e.Cursor(), nextID)
	}
}

func TestShadowExecutesNothing(t *testing.T) {
	e, g, _ := newEngine(t, ModeShadow)
	e.Ingest(context.Background(), []Alert{
		alert("198.51.100.7", "192.168.1.10", "ET EXPLOIT", 1, 0),
		alert("192.168.1.50", "203.0.113.200", "ET MALWARE", 1, 0),
	})
	if b, n := g.counts(); b != 0 || n != 0 {
		t.Fatalf("shadow mode acted: %d blocks, %d notifications", b, n)
	}
	for _, inc := range e.Incidents("", 0) {
		_, ds, _ := e.Incident(inc.ID)
		if inc.Status != StatusRecorded || len(ds) != 1 || ds[0].Executed || !strings.HasPrefix(ds[0].Result, "shadow mode: would ") {
			t.Fatalf("incident %+v decisions %+v", inc, ds)
		}
	}
}

func TestOffDoesNothing(t *testing.T) {
	e, g, _ := newEngine(t, ModeOff)
	e.Ingest(context.Background(), []Alert{alert("198.51.100.7", "192.168.1.10", "ET EXPLOIT", 1, 0)})
	if len(e.Incidents("", 0)) != 0 || e.Cursor() != nextID {
		t.Fatal("off mode recorded incidents or kept the cursor")
	}
	if b, n := g.counts(); b+n != 0 {
		t.Fatal("off mode acted")
	}
}

func TestApproveWaitsThenBlocks(t *testing.T) {
	e, g, _ := newEngine(t, ModeApprove)
	ctx := context.Background()
	e.Ingest(ctx, []Alert{alert("198.51.100.7", "192.168.1.10", "ET EXPLOIT", 1, 0)})
	pending := e.Incidents(StatusPending, 0)
	if len(pending) != 1 {
		t.Fatalf("pending: %+v", e.Incidents("", 0))
	}
	inc := pending[0]
	if inc.Recommendation.Tool != ToolBlock || inc.Recommendation.Target != "198.51.100.7" || inc.Recommendation.Minutes != 60 {
		t.Fatalf("recommendation: %+v", inc.Recommendation)
	}
	if b, n := g.counts(); b != 0 || n != 1 {
		t.Fatalf("before the answer: %d blocks, %d notifications", b, n)
	}
	_, ds, _ := e.Incident(inc.ID)
	if len(ds) != 1 || ds[0].By != "rule:1" || ds[0].Executed {
		t.Fatalf("why: %+v", ds)
	}

	if _, _, err := e.Answer(ctx, inc.ID, "maybe", "alice", 0); err == nil {
		t.Fatal("bad answer accepted")
	}
	got, d, err := e.Answer(ctx, inc.ID, AnswerApprove, "alice", 30)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusActed || got.AnsweredBy != "alice" || !d.Executed || d.By != "admin:alice" || !strings.Contains(d.Result, "verified") {
		t.Fatalf("after approve: %+v %+v", got, d)
	}
	if g.blocks["198.51.100.7"] != 30 {
		t.Fatalf("block: %v", g.blocks)
	}
	if _, _, err := e.Answer(ctx, inc.ID, AnswerApprove, "alice", 0); err == nil {
		t.Fatal("answered twice")
	}
	// the answer is a training example
	f, err := os.Open(filepath.Join(e.Dir, "examples.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() || !strings.Contains(sc.Text(), `"answer":"approve"`) || sc.Scan() {
		t.Fatal("example not written once")
	}
}

func TestAnswerIgnoreAndRefusedBlock(t *testing.T) {
	e, g, _ := newEngine(t, ModeApprove)
	ctx := context.Background()
	e.Ingest(ctx, []Alert{
		alert("198.51.100.7", "192.168.1.10", "ET EXPLOIT", 1, 0),
		alert("198.51.100.8", "192.168.1.10", "ET EXPLOIT", 1, 0),
		alert("192.168.1.50", "192.168.1.10", "ET LATERAL", 1, 0),
	})
	p := e.Incidents(StatusPending, 0)
	if len(p) != 3 {
		t.Fatalf("pending: %d", len(p))
	}
	lateral, second, first := p[0], p[1], p[2]
	if inc, _, err := e.Answer(ctx, first.ID, AnswerIgnore, "alice", 0); err != nil || inc.Status != StatusIgnored {
		t.Fatalf("ignore: %v %+v", err, inc)
	}
	// internal hosts are never blocked
	if _, _, err := e.Answer(ctx, lateral.ID, AnswerBlock, "alice", 0); err == nil {
		t.Fatal("blocked an internal host")
	}
	// goblind refuses: the incident fails with the reason
	g.blockErr = errors.New("403: 198.51.100.8 cannot be blocked: a DNS server the box uses")
	inc, d, err := e.Answer(ctx, second.ID, AnswerBlock, "alice", 0)
	if err != nil || inc.Status != StatusFailed || !strings.Contains(d.Result, "refused by goblind: 403") {
		t.Fatalf("refused: %v %+v %+v", err, inc, d)
	}
}

func TestAlwaysThenAuto(t *testing.T) {
	e, g, _ := newEngine(t, ModeAuto)
	ctx := context.Background()
	// auto mode: built-in rules still ask before blocking
	e.Ingest(ctx, []Alert{alert("198.51.100.7", "192.168.1.10", "ET EXPLOIT", 1, 0)})
	p := e.Incidents(StatusPending, 0)
	if len(p) != 1 || len(g.blocks) != 0 {
		t.Fatalf("auto blocked without an always rule: %+v %v", p, g.blocks)
	}
	if _, _, err := e.Answer(ctx, p[0].ID, AnswerAlways, "alice", 0); err != nil {
		t.Fatal(err)
	}
	rules := e.Rules()
	if r := rules[0]; !r.Auto || r.Action.Tool != ToolBlock || r.Match.SignatureID != "200E" || r.Match.Direction != Inbound || r.Builtin {
		t.Fatalf("always rule: %+v", r)
	}
	// the same signature from another address is now blocked on its own
	e.Ingest(ctx, []Alert{alert("198.51.100.99", "192.168.1.11", "ET EXPLOIT", 1, time.Minute)})
	if g.blocks["198.51.100.99"] != 60 {
		t.Fatalf("auto block: %v", g.blocks)
	}
	if inc := e.Incidents("", 1)[0]; inc.Status != StatusActed {
		t.Fatalf("auto incident: %+v", inc)
	}
	// low severity noise is only watched
	e.Ingest(ctx, []Alert{alert("198.51.100.200", "192.168.1.10", "GPL ICMP", 3, 2*time.Minute)})
	if inc := e.Incidents("", 1)[0]; inc.Status != StatusRecorded || len(e.Status().Watches) != 1 {
		t.Fatalf("watch: %+v %+v", inc, e.Status())
	}
	// the watch shows up in the next incident from that address
	e.Ingest(ctx, []Alert{alert("198.51.100.200", "192.168.1.10", "ET OTHER", 3, 3*time.Minute)})
	if inc := e.Incidents("", 1)[0]; !inc.Enrichment.Watched {
		t.Fatalf("watched not set: %+v", inc.Enrichment)
	}
}

func TestOutboundEscalates(t *testing.T) {
	e, g, _ := newEngine(t, ModeAuto)
	e.Ingest(context.Background(), []Alert{alert("192.168.1.50", "203.0.113.200", "ET MALWARE", 1, 0)})
	inc := e.Incidents("", 1)[0]
	if inc.Status != StatusPending || !inc.Urgent || len(g.blocks) != 0 {
		t.Fatalf("outbound: %+v", inc)
	}
	if _, n := g.counts(); n != 1 || !strings.Contains(g.notes[0], "urgent") {
		t.Fatalf("notes: %v", g.notes)
	}
}

func TestRulesAndPersistence(t *testing.T) {
	e, g, _ := newEngine(t, ModeApprove)
	bad := []Rule{{ID: 1, Name: "", Action: Action{Tool: "shell"}}, {ID: 1, Name: "x", Action: Action{Tool: ToolBlock}, Match: Match{Remote: "nope", Direction: "up"}}}
	if p := e.SetRules(bad); len(p) < 5 {
		t.Fatalf("problems: %v", p)
	}
	good := []Rule{{ID: 7, Name: "scans: watch", Enabled: true, Match: Match{Signature: "scan", Remote: "198.51.100.0/24"}, Action: Action{Tool: ToolWatch}}}
	if p := e.SetRules(good); p != nil {
		t.Fatal(p)
	}
	e.Ingest(context.Background(), []Alert{alert("198.51.100.7", "192.168.1.10", "ET SCAN", 1, 0)})
	inc := e.Incidents("", 1)[0]
	if inc.Status != StatusRecorded || inc.Recommendation.Tool != ToolWatch {
		t.Fatalf("custom rule: %+v", inc)
	}
	// a new engine on the same directory has the same state
	e2, err := New(e.Dir, g, Settings{Mode: ModeApprove})
	if err != nil {
		t.Fatal(err)
	}
	if len(e2.Rules()) != 1 || len(e2.Incidents("", 0)) != 1 || e2.Cursor() != e.Cursor() || len(e2.Decisions(0)) != 1 {
		t.Fatalf("reloaded: %+v", e2.Status())
	}
}
