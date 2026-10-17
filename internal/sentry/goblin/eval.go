package goblin

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/sentry"
)

// MinExamples is how many usable answers training needs.
const MinExamples = 40

// TestShare is the newest share of the answers a model is evaluated on
// (it is trained on the older ones, as it would have been at the time).
const TestShare = 0.25

// Cases turns examples into labelled cases, oldest first; examples
// without a usable label are skipped.
func Cases(xs []sentry.Example) []Case {
	var out []Case
	for _, x := range xs {
		l, m, ok := Label(x)
		if !ok {
			continue
		}
		out = append(out, Case{Time: x.Time, F: FeaturesOf(x.Alert, x.Enrichment), Label: l, Minutes: m})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	return out
}

// Split divides cases into the older training part and the newer test part.
func Split(cases []Case) (train, test []Case, err error) {
	if len(cases) < MinExamples {
		return nil, nil, fmt.Errorf("%d usable answers, need at least %d", len(cases), MinExamples)
	}
	cut := len(cases) - int(float64(len(cases))*TestShare)
	return cases[:cut], cases[cut:], nil
}

// Thresholds a report shows coverage for.
var reportThresholds = []float64{0.5, 0.7, 0.8, 0.9, 0.95}

// Report is a model's evaluation on the newest answers.
type Report struct {
	Kind     string    `json:"kind"`
	Model    string    `json:"model,omitempty"` // e.g. the GGUF file
	Created  time.Time `json:"created"`
	Examples int       `json:"examples"` // usable answers
	Train    int       `json:"train"`
	Test     int       `json:"test"`
	// Accuracy: share of test answers predicted exactly (tool; for
	// blocks the length may differ).
	Accuracy float64               `json:"accuracy"`
	PerLabel map[string]LabelStats `json:"per_label"`
	// Confusion[admin][model] counts.
	Confusion map[string]map[string]int `json:"confusion"`
	// BlockedIgnored: the model said block where the admin ignored (it
	// would only have been suggested: blocks always ask).
	BlockedIgnored int `json:"blocked_ignored"`
	// IgnoredBlocked: the model said ignore where the admin blocked - an
	// attack it would have hidden, if allowed to act at that confidence.
	IgnoredBlocked int `json:"ignored_blocked"`
	// MinutesRight: blocks predicted with the admin's block length.
	MinutesRight int `json:"minutes_right"`
	// Coverage per confidence threshold of the actions the model may take
	// on its own (everything but blocks).
	Thresholds []ThresholdStats `json:"thresholds"`
	// Novel: test incidents whose signature never appears in training,
	// how many the model got right and how many it would have handled on
	// its own at 0.9 (it should ask instead).
	Novel        int `json:"novel"`
	NovelCorrect int `json:"novel_correct"`
	NovelActed   int `json:"novel_acted"`
	// Invalid answers (not a tool call): always 0 for knn; for llm the
	// grammar should keep it 0.
	Invalid   int     `json:"invalid"`
	LatencyMS float64 `json:"latency_ms"` // mean per prediction
}

// LabelStats are the counts for one label.
type LabelStats struct {
	N         int     `json:"n"`         // admin answers with this label
	Predicted int     `json:"predicted"` // model answers with this label
	Correct   int     `json:"correct"`
	Precision float64 `json:"precision"`
	Recall    float64 `json:"recall"`
}

// ThresholdStats: at this confidence, how many test incidents the model
// would have handled on its own and how many of those it got wrong.
type ThresholdStats struct {
	Threshold float64 `json:"threshold"`
	Acted     int     `json:"acted"`
	Wrong     int     `json:"wrong"`
	// HiddenAttacks: of the wrong ones, incidents the admin blocked.
	HiddenAttacks int `json:"hidden_attacks"`
}

// Evaluate asks p about every test case and compares with the admin.
func Evaluate(kind string, p Predictor, train, test []Case) (Report, error) {
	r := Report{Kind: kind, Created: time.Now().UTC(), Examples: len(train) + len(test), Train: len(train), Test: len(test),
		PerLabel: map[string]LabelStats{}, Confusion: map[string]map[string]int{}}
	if len(test) == 0 {
		return r, errors.New("nothing to evaluate on")
	}
	type result struct {
		c    Case
		p    Prediction
		fail bool
	}
	var results []result
	start := time.Now()
	for _, c := range test {
		pred, err := p.Predict(Query{Time: c.Time, F: c.F})
		results = append(results, result{c, pred, err != nil || !slices.Contains(Labels, pred.Tool) && pred.Tool != sentry.ToolRequestApproval})
	}
	r.LatencyMS = float64(time.Since(start).Microseconds()) / 1000 / float64(len(test))
	correct := 0
	for _, res := range results {
		got := res.p.Tool
		if res.fail {
			r.Invalid++
			got = "invalid"
		}
		want := res.c.Label
		if r.Confusion[want] == nil {
			r.Confusion[want] = map[string]int{}
		}
		r.Confusion[want][got]++
		ws := r.PerLabel[want]
		ws.N++
		r.PerLabel[want] = ws
		gs := r.PerLabel[got]
		gs.Predicted++
		if got == want {
			gs.Correct++
			correct++
			if got == LabelBlock && res.p.Minutes == res.c.Minutes {
				r.MinutesRight++
			}
		}
		r.PerLabel[got] = gs
		if got == LabelBlock && want == LabelIgnore {
			r.BlockedIgnored++
		}
		if got == LabelIgnore && want == LabelBlock {
			r.IgnoredBlocked++
		}
	}
	r.Accuracy = float64(correct) / float64(len(test))
	known := map[string]bool{}
	for _, c := range train {
		known[c.F.SignatureID+"|"+c.F.Signature] = true
	}
	for _, res := range results {
		if known[res.c.F.SignatureID+"|"+res.c.F.Signature] {
			continue
		}
		r.Novel++
		if !res.fail && res.p.Tool == res.c.Label {
			r.NovelCorrect++
		}
		if !res.fail && modelMayRun(res.p.Tool) && res.p.Confidence >= 0.9 {
			r.NovelActed++
		}
	}
	for l, s := range r.PerLabel {
		if s.Predicted > 0 {
			s.Precision = float64(s.Correct) / float64(s.Predicted)
		}
		if s.N > 0 {
			s.Recall = float64(s.Correct) / float64(s.N)
		}
		r.PerLabel[l] = s
	}
	for _, t := range reportThresholds {
		ts := ThresholdStats{Threshold: t}
		for _, res := range results {
			if res.fail || !modelMayRun(res.p.Tool) || res.p.Confidence < t {
				continue
			}
			ts.Acted++
			if res.p.Tool != res.c.Label {
				ts.Wrong++
				if res.c.Label == LabelBlock {
					ts.HiddenAttacks++
				}
			}
		}
		r.Thresholds = append(r.Thresholds, ts)
	}
	return r, nil
}

// modelMayRun mirrors the engine: the actions a model may take on its own.
func modelMayRun(tool string) bool {
	switch tool {
	case sentry.ToolWatch, sentry.ToolReputation, sentry.ToolNotify, sentry.ToolFalsePositive:
		return true
	}
	return false
}
