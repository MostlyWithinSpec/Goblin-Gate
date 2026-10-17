// Package goblin is G.O.B.L.I.N. (Guarded Observation & Behavioral Logic
// for Incident Neutralization), Goblin Sentry's decision model: it learns
// how this admin answers incidents from their own past answers, offline
// and versioned. A model only ever names one of Sentry's tools (plus a
// block length from a fixed menu); the engine fills the target from the
// incident, and goblind still checks every call.
//
// Two kinds of model are trained and compared on the same data: a plain
// nearest-neighbour classifier over structured features (kind "knn",
// built in) and FunctionGemma 270M run by llama.cpp with its output
// constrained by a grammar (kind "llm", see llm.go). A new version is used
// only after the admin accepts its evaluation report.
package goblin

import (
	"math"
	"net/netip"
	"strconv"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/sentry"
)

// Labels: what the admin wanted done, as one of Sentry's tools.
const (
	LabelBlock  = sentry.ToolBlock
	LabelIgnore = sentry.ToolFalsePositive
	LabelWatch  = sentry.ToolWatch
	LabelNotify = sentry.ToolNotify
)

// Labels lists the labels a model can learn.
var Labels = []string{LabelBlock, LabelIgnore, LabelWatch, LabelNotify}

// BlockMinutes is the menu of block lengths a model can name.
var BlockMinutes = []int{15, 60, 240, 1440, 10080}

// SnapMinutes returns the menu entry closest to m (on a log scale).
func SnapMinutes(m int) int {
	if m <= 0 {
		return 60
	}
	best, bestD := BlockMinutes[0], math.Inf(1)
	for _, b := range BlockMinutes {
		if d := math.Abs(math.Log(float64(m)) - math.Log(float64(b))); d < bestD {
			best, bestD = b, d
		}
	}
	return best
}

// Label is the tool an example's answer stands for: block and ignore
// directly; approve and always mean "what Sentry recommended".
func Label(x sentry.Example) (tool string, minutes int, ok bool) {
	act := x.Action
	if act.Tool == "" { // examples written before Action existed
		switch x.Answer {
		case sentry.AnswerBlock:
			act = sentry.Action{Tool: sentry.ToolBlock}
		case sentry.AnswerIgnore:
			act = sentry.Action{Tool: sentry.ToolFalsePositive}
		default:
			act = x.Recommendation
		}
	}
	switch act.Tool {
	case LabelBlock:
		return LabelBlock, SnapMinutes(act.Minutes), true
	case LabelIgnore, LabelWatch, LabelNotify:
		return act.Tool, 0, true
	}
	return "", 0, false
}

// Features are what a model sees of an incident: no free text beyond the
// signature, and addresses only as "same /24" comparisons.
type Features struct {
	SignatureID string   `json:"sid,omitempty"`
	Signature   string   `json:"sig"`
	Words       []string `json:"words,omitempty"` // signature words, lower case
	Category    string   `json:"cat,omitempty"`
	Severity    int      `json:"sev"`
	Direction   string   `json:"dir"`
	Proto       string   `json:"proto,omitempty"`
	Port        int      `json:"port,omitempty"` // the local side's port
	Local       string   `json:"local,omitempty"`
	LocalName   string   `json:"local_name,omitempty"`
	LocalIntf   string   `json:"intf,omitempty"`
	RemoteNet   string   `json:"remote_net,omitempty"` // /24 or /48
	Seen        int      `json:"seen"`
	RemoteSeen  int      `json:"remote_seen"`
	Watched     bool     `json:"watched,omitempty"`
	Blocked     bool     `json:"blocked,omitempty"`
	// Past answers to this signature, as shares.
	PastBlock  float64 `json:"past_block,omitempty"`
	PastIgnore float64 `json:"past_ignore,omitempty"`
	PastN      int     `json:"past_n,omitempty"`
}

// FeaturesOf extracts the features of an alert and its enrichment.
func FeaturesOf(a sentry.Alert, en sentry.Enrichment) Features {
	f := Features{SignatureID: a.SignatureID, Signature: a.Signature, Words: words(a.Signature),
		Category: strings.ToLower(a.Category), Severity: a.Severity, Direction: en.Direction,
		Proto: strings.ToLower(a.Proto), Local: en.Local, LocalName: en.LocalName, LocalIntf: en.LocalIntf,
		RemoteNet: netOf(en.Remote), Seen: en.Seen, RemoteSeen: en.RemoteSeen, Watched: en.Watched, Blocked: en.Blocked}
	switch {
	case en.Local != "" && en.Local == a.DstIP:
		f.Port = a.DstPort
	case en.Local != "" && en.Local == a.SrcIP:
		f.Port = a.SrcPort
	default:
		f.Port = a.DstPort
	}
	n := 0
	for _, c := range en.PastAnswers {
		n += c
	}
	if n > 0 {
		f.PastN = n
		f.PastBlock = float64(en.PastAnswers[sentry.AnswerBlock]) / float64(n)
		f.PastIgnore = float64(en.PastAnswers[sentry.AnswerIgnore]) / float64(n)
	}
	return f
}

func words(sig string) []string {
	var out []string
	seen := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(sig), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	}) {
		if len(w) < 2 || seen[w] {
			continue
		}
		if _, err := strconv.Atoi(w); err == nil {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

func netOf(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	a = a.Unmap()
	bits := 24
	if a.Is6() {
		bits = 48
	}
	p, _ := a.Prefix(bits)
	return p.String()
}
