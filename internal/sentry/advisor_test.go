package sentry

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeAdvisor struct {
	adv   Advice
	asked int
}

func (f *fakeAdvisor) Advise(Incident) (Advice, bool) {
	f.asked++
	return f.adv, f.adv.Action.Tool != ""
}

func TestAdvisor(t *testing.T) {
	ctx := context.Background()
	inbound := func() Alert { return alert("198.51.100.7", "192.168.1.10", "ET EXPLOIT", 1, 0) }
	run := func(mode string, adv Advice, a Alert) (Incident, Decision, *fakeAdvisor, *fakeGoblind) {
		t.Helper()
		e, g, _ := newEngine(t, mode)
		f := &fakeAdvisor{adv: adv}
		e.Advisor = f
		e.Ingest(ctx, []Alert{a})
		inc := e.Incidents("", 0)[0]
		_, ds, _ := e.Incident(inc.ID)
		return inc, ds[len(ds)-1], f, g
	}

	// a confident "ignore" the admin lets the model give runs
	inc, d, _, _ := run(ModeApprove, Advice{Model: "v1 (knn)", Action: Action{Tool: ToolFalsePositive}, Confidence: 0.97, Act: true, Why: "like before"}, inbound())
	if inc.Status != StatusIgnored || d.By != "model:v1 (knn)" || !d.Executed || d.Model == nil || !d.Model.Used || d.Model.Why != "like before" {
		t.Fatalf("ignore: %+v %+v %+v", inc, d, d.Model)
	}

	// a block is never run by the model, even when it is sure: it becomes the suggestion
	inc, d, _, g := run(ModeAuto, Advice{Model: "v1 (knn)", Action: Action{Tool: ToolBlock, Minutes: 1440}, Confidence: 0.99, Act: true}, inbound())
	if inc.Status != StatusPending || d.Executed || d.Model.Used || inc.Recommendation != (Action{Tool: ToolBlock, Target: "198.51.100.7", Minutes: 1440}) {
		t.Fatalf("block: %+v %+v", inc, d)
	}
	if n, _ := g.counts(); n != 0 || !strings.Contains(d.Result, "G.O.B.L.I.N. suggests: block 198.51.100.7 for 1440 minutes (99% sure)") {
		t.Fatalf("block ran or result: %d %q", n, d.Result)
	}

	// below the threshold (Act false): only a suggestion
	inc, d, _, _ = run(ModeApprove, Advice{Model: "v1 (knn)", Action: Action{Tool: ToolWatch}, Confidence: 0.6}, inbound())
	if inc.Status != StatusPending || inc.Recommendation.Tool != ToolWatch || inc.Recommendation.Target != "198.51.100.7" || d.Model.Used {
		t.Fatalf("suggest: %+v %+v", inc, d)
	}

	// shadow mode records what it would do
	inc, d, _, _ = run(ModeShadow, Advice{Model: "v1 (knn)", Action: Action{Tool: ToolFalsePositive}, Confidence: 0.97, Act: true}, inbound())
	if inc.Status != StatusRecorded || d.Executed || d.Model.Used || !strings.Contains(d.Result, "would mark as false positive") {
		t.Fatalf("shadow: %+v %+v", inc, d)
	}

	// escalations about internal hosts are not the model's to answer
	_, _, f, _ := run(ModeApprove, Advice{Model: "v1 (knn)", Action: Action{Tool: ToolFalsePositive}, Confidence: 1, Act: true},
		alert("192.168.1.50", "203.0.113.200", "ET MALWARE", 1, 0))
	if f.asked != 0 {
		t.Fatal("advisor asked about an escalation")
	}

	// a lateral block suggestion becomes an escalation, which Approve cannot run: not suggested
	e, _, _ := newEngine(t, ModeApprove)
	e.SetRules([]Rule{{ID: 1, Name: "ask", Enabled: true, Action: Action{Tool: ToolRequestApproval}}})
	e.Advisor = &fakeAdvisor{adv: Advice{Model: "v1 (knn)", Action: Action{Tool: ToolBlock, Minutes: 60}, Confidence: 0.9}}
	e.Ingest(ctx, []Alert{alert("192.168.1.50", "192.168.1.10", "ET LATERAL", 2, 0)})
	if inc := e.Incidents("", 0)[0]; inc.Recommendation.Tool != ToolWatch {
		t.Fatalf("lateral: %+v", inc.Recommendation)
	}

	// answers keep what ran, with the admin's block length
	e, _, _ = newEngine(t, ModeApprove)
	e.Ingest(ctx, []Alert{inbound()})
	id := e.Incidents("", 0)[0].ID
	if _, _, err := e.Answer(ctx, id, AnswerBlock, "alice", 240); err != nil {
		t.Fatal(err)
	}
	xs := readExamples(t, e.Dir)
	if len(xs) != 1 || xs[0].Action != (Action{Tool: ToolBlock, Target: "198.51.100.7", Minutes: 240}) {
		t.Fatalf("example: %+v", xs)
	}

	// a wrong answer can be forgotten
	e.Ingest(ctx, []Alert{alert("198.51.100.8", "192.168.1.10", "ET EXPLOIT", 1, 0)})
	second := e.Incidents("", 0)[0]
	if _, _, err := e.Answer(ctx, second.ID, AnswerIgnore, "alice", 0); err != nil {
		t.Fatal(err)
	}
	if ok, err := e.RemoveExample(xs[0].Alert.ID); !ok || err != nil {
		t.Fatalf("remove: %v %v", ok, err)
	}
	if left := readExamples(t, e.Dir); len(left) != 1 || left[0].Alert.ID != second.Alert.ID {
		t.Fatalf("after remove: %+v", left)
	}
	if ok, _ := e.RemoveExample(xs[0].Alert.ID); ok {
		t.Fatal("removed twice")
	}
}

func readExamples(t *testing.T, dir string) []Example {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "examples.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var out []Example
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var x Example
		if err := json.Unmarshal([]byte(line), &x); err != nil {
			t.Fatal(err)
		}
		out = append(out, x)
	}
	return out
}
