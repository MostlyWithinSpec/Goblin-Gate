package goblin

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
)

// Case is one labelled decision: the features of the incident, when it
// happened and what the admin wanted.
type Case struct {
	Time    time.Time `json:"time"`
	F       Features  `json:"f"`
	Label   string    `json:"label"`
	Minutes int       `json:"minutes,omitempty"` // for blocks, from BlockMinutes
}

// Query is what a model is asked about.
type Query struct {
	Time time.Time
	F    Features
}

// Prediction is a model's answer: one tool, a block length for blocks, a
// confidence and why.
type Prediction struct {
	Tool       string      `json:"tool"`
	Minutes    int         `json:"minutes,omitempty"`
	Confidence float64     `json:"confidence"`
	Why        string      `json:"why"`
	Neighbours []Neighbour `json:"neighbours,omitempty"`
}

// Neighbour is a past decision a nearest-neighbour prediction rests on.
type Neighbour struct {
	Time       time.Time `json:"time"`
	Summary    string    `json:"summary"`
	Label      string    `json:"label"`
	Minutes    int       `json:"minutes,omitempty"`
	Similarity float64   `json:"similarity"`
}

// Predictor is a trained model.
type Predictor interface {
	Predict(q Query) (Prediction, error)
}

// KNN is the plain classifier: the admin's most similar past decisions
// vote, weighted by similarity and (gently) by age.
type KNN struct {
	K            int     `json:"k"`
	HalfLifeDays float64 `json:"half_life_days"` // 0 = no decay
	Cases        []Case  `json:"cases"`
}

// TrainKNN builds the classifier from cases (oldest first). K and the
// half-life are picked by validating on the newest quarter of the cases
// (trained on the rest); ties go to the longer memory.
func TrainKNN(cases []Case) *KNN {
	m := &KNN{K: 7, HalfLifeDays: 30, Cases: slices.Clone(cases)}
	cut := len(cases) - len(cases)/4
	if len(cases) < 2*MinExamples {
		return m
	}
	fit, val := cases[:cut], cases[cut:]
	best := -1
	for _, hl := range []float64{90, 30, 14, 7} {
		for _, k := range []int{11, 7, 5} {
			try := &KNN{K: k, HalfLifeDays: hl, Cases: fit}
			right := 0
			for _, c := range val {
				if p, err := try.Predict(Query{Time: c.Time, F: c.F}); err == nil && p.Tool == c.Label {
					right++
				}
			}
			if right > best {
				best, m.K, m.HalfLifeDays = right, k, hl
			}
		}
	}
	return m
}

// weights of the similarity components.
var weights = struct {
	sid, words, cat, sev, dir, port, local, intf, remote, seen, rseen, watched, blocked, past float64
}{sid: 4, words: 2, cat: 1, sev: 1, dir: 2, port: 1, local: 1, intf: 0.5, remote: 1, seen: 1, rseen: 1, watched: 0.5, blocked: 0.5, past: 2}

func eq[T comparable](a, b T) float64 {
	if a == b {
		return 1
	}
	return 0
}

func jaccard(a, b []string) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	in := 0
	for _, w := range a {
		if slices.Contains(b, w) {
			in++
		}
	}
	return float64(in) / float64(len(a)+len(b)-in)
}

func countSim(a, b int) float64 {
	d := math.Abs(math.Log1p(float64(a)) - math.Log1p(float64(b)))
	return math.Max(0, 1-d/math.Log1p(50))
}

// Similarity of two incidents, 0..1.
func Similarity(a, b Features) float64 {
	w := weights
	var sum, total float64
	add := func(weight, sim float64) {
		sum += weight * sim
		total += weight
	}
	if a.SignatureID != "" && b.SignatureID != "" {
		add(w.sid, eq(a.SignatureID, b.SignatureID))
	} else {
		add(w.sid, eq(a.Signature, b.Signature))
	}
	add(w.words, jaccard(a.Words, b.Words))
	add(w.cat, eq(a.Category, b.Category))
	add(w.sev, math.Max(0, 1-math.Abs(float64(a.Severity-b.Severity))/3))
	add(w.dir, eq(a.Direction, b.Direction))
	add(w.port, eq(a.Port, b.Port))
	add(w.local, eq(a.Local, b.Local))
	add(w.intf, eq(a.LocalIntf, b.LocalIntf))
	add(w.remote, eq(a.RemoteNet, b.RemoteNet))
	add(w.seen, countSim(a.Seen, b.Seen))
	add(w.rseen, countSim(a.RemoteSeen, b.RemoteSeen))
	add(w.watched, eq(a.Watched, b.Watched))
	add(w.blocked, eq(a.Blocked, b.Blocked))
	add(w.past, 1-(math.Abs(a.PastBlock-b.PastBlock)+math.Abs(a.PastIgnore-b.PastIgnore))/2)
	return sum / total
}

// Predict lets the K most similar earlier cases vote. Confidence is the
// winning share of the vote times the similarity of the closest case, so
// an incident unlike anything the admin answered gets a low confidence.
func (m *KNN) Predict(q Query) (Prediction, error) {
	type scored struct {
		c   *Case
		sim float64
	}
	var all []scored
	for i := range m.Cases {
		c := &m.Cases[i]
		if !q.Time.IsZero() && c.Time.After(q.Time) {
			continue
		}
		all = append(all, scored{c, Similarity(q.F, c.F)})
	}
	if len(all) == 0 {
		return Prediction{}, fmt.Errorf("no past decisions to compare with")
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].sim > all[j].sim })
	k := min(max(m.K, 1), len(all))
	top := all[:k]
	votes := map[string]float64{}
	var total float64
	var blockMins []scored
	for _, s := range top {
		w := math.Pow(s.sim, 4)
		if m.HalfLifeDays > 0 && !q.Time.IsZero() {
			w *= math.Pow(0.5, q.Time.Sub(s.c.Time).Hours()/24/m.HalfLifeDays)
		}
		votes[s.c.Label] += w
		total += w
		if s.c.Label == LabelBlock {
			blockMins = append(blockMins, scored{s.c, w})
		}
	}
	p := Prediction{}
	best := -1.0
	for _, l := range Labels { // fixed order: ties are stable
		if votes[l] > best {
			p.Tool, best = l, votes[l]
		}
	}
	if total > 0 {
		p.Confidence = best / total * top[0].sim
	}
	if p.Tool == LabelBlock {
		// weighted median of the neighbours' block lengths
		sort.Slice(blockMins, func(i, j int) bool { return blockMins[i].c.Minutes < blockMins[j].c.Minutes })
		var acc float64
		for _, s := range blockMins {
			acc += s.sim
			if acc >= votes[LabelBlock]/2 {
				p.Minutes = s.c.Minutes
				break
			}
		}
		p.Minutes = SnapMinutes(p.Minutes)
	}
	var lines []string
	for _, s := range top[:min(3, len(top))] {
		n := Neighbour{Time: s.c.Time, Summary: summary(s.c.F), Label: s.c.Label, Minutes: s.c.Minutes, Similarity: s.sim}
		p.Neighbours = append(p.Neighbours, n)
		lines = append(lines, fmt.Sprintf("%s: %s -> %s (%.0f%% similar)", n.Time.Format("2006-01-02"), n.Summary, describeLabel(n.Label, n.Minutes), s.sim*100))
	}
	p.Why = fmt.Sprintf("%.0f%% of the vote of your %d most similar past answers; closest: %s", best/total*100, k, strings.Join(lines, "; "))
	return p, nil
}

func summary(f Features) string {
	sig := f.Signature
	if f.SignatureID != "" {
		sig += " [" + f.SignatureID + "]"
	}
	local := f.Local
	if f.LocalName != "" {
		local = f.LocalName
	}
	switch f.Direction {
	case "inbound":
		return fmt.Sprintf("%s from %s to %s", sig, f.RemoteNet, local)
	case "outbound":
		return fmt.Sprintf("%s from %s to %s", sig, local, f.RemoteNet)
	}
	return fmt.Sprintf("%s (%s, %s)", sig, f.Direction, local)
}

func describeLabel(l string, minutes int) string {
	switch l {
	case LabelBlock:
		return fmt.Sprintf("blocked %d min", minutes)
	case LabelIgnore:
		return "ignored"
	case LabelWatch:
		return "watched"
	case LabelNotify:
		return "notified"
	}
	return l
}
