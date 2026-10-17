package goblin

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/sentry"
)

// TestFixtureReproducible: the comparison dataset and the classifier's
// evaluation on it are the same on every run.
func TestFixtureReproducible(t *testing.T) {
	a, _ := json.Marshal(Fixture(1, 1500))
	b, _ := json.Marshal(Fixture(1, 1500))
	if sha256.Sum256(a) != sha256.Sum256(b) {
		t.Fatal("fixture differs between runs")
	}
	c, _ := json.Marshal(Fixture(2, 1500))
	if string(a) == string(c) {
		t.Fatal("seeds give the same fixture")
	}
	train, test, err := Split(Cases(Fixture(1, 1500)))
	if err != nil || len(train) != 1125 || len(test) != 375 {
		t.Fatalf("split: %v %d %d", err, len(train), len(test))
	}
	r1, _ := Evaluate(KindKNN, TrainKNN(train), train, test)
	r2, _ := Evaluate(KindKNN, TrainKNN(train), train, test)
	r1.Created, r1.LatencyMS, r2.LatencyMS = r2.Created, 0, 0
	if fmt.Sprint(r1) != fmt.Sprint(r2) {
		t.Fatal("evaluation not reproducible")
	}
	// the floor: 4% of the fixture's answers are random
	if r1.Accuracy < 0.92 || r1.Invalid != 0 || r1.Novel == 0 {
		t.Fatalf("knn on the fixture: %+v", r1)
	}
	// at 0.9 it handles a good part alone and hides at most a few attacks
	if at := r1.Thresholds[3]; at.Threshold != 0.9 || at.Acted < 50 || at.HiddenAttacks > 3 {
		t.Fatalf("at 0.9: %+v", at)
	}
}

func TestLabel(t *testing.T) {
	for _, c := range []struct {
		x       sentry.Example
		tool    string
		minutes int
	}{
		{sentry.Example{Answer: "block", Action: sentry.Action{Tool: sentry.ToolBlock, Minutes: 90}}, LabelBlock, 60},
		{sentry.Example{Answer: "block"}, LabelBlock, 60}, // written before Action existed
		{sentry.Example{Answer: "ignore"}, LabelIgnore, 0},
		{sentry.Example{Answer: "approve", Recommendation: sentry.Action{Tool: sentry.ToolWatch}}, LabelWatch, 0},
		{sentry.Example{Answer: "always", Action: sentry.Action{Tool: sentry.ToolBlock, Minutes: 10080}}, LabelBlock, 10080},
	} {
		tool, m, ok := Label(c.x)
		if !ok || tool != c.tool || m != c.minutes {
			t.Fatalf("%+v: %s %d %v", c.x, tool, m, ok)
		}
	}
	if _, _, ok := Label(sentry.Example{Answer: "approve", Recommendation: sentry.Action{Tool: sentry.ToolEscalate}}); ok {
		t.Fatal("escalate is not a label")
	}
	if SnapMinutes(100000) != 10080 || SnapMinutes(0) != 60 || SnapMinutes(1000) != 1440 {
		t.Fatal("snap")
	}
}

func TestKNNExplains(t *testing.T) {
	cases := Cases(Fixture(1, 400))
	m := TrainKNN(cases[:300])
	p, err := m.Predict(Query{Time: cases[350].Time, F: cases[350].F})
	if err != nil || len(p.Neighbours) != 3 || p.Why == "" || p.Confidence <= 0 || p.Confidence > 1 {
		t.Fatalf("%+v %v", p, err)
	}
	// nothing to compare with
	if _, err := (&KNN{K: 5}).Predict(Query{F: cases[0].F}); err == nil {
		t.Fatal("prediction without cases")
	}
	// an incident unlike anything answered gets a low confidence
	odd := Features{Signature: "GPL CHAT IRC", Words: words("GPL CHAT IRC"), Direction: "external", Severity: 4, Seen: 900}
	if p, _ := m.Predict(Query{F: odd}); p.Confidence > 0.6 {
		t.Fatalf("odd incident: %+v", p)
	}
}
