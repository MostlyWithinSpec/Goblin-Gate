package goblin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
)

// LLM is the language-model candidate: FunctionGemma (or another model in
// its call syntax) served by llama.cpp's llama-server, asked with the
// incident and the admin's most similar past answers, its output held to
// Grammar(). Confidence is the model's probability for the chosen tool's
// first token among the allowed first tokens.
type LLM struct {
	Endpoint string // llama-server base URL, e.g. http://127.0.0.1:8090
	Name     string // model file, for the report
	// Past are the answers shown as examples (the training cases); Shots
	// how many of the most similar ones go into each prompt.
	Past  []Case
	Shots int
	HTTP  *http.Client
}

const systemPrompt = "You are G.O.B.L.I.N., the firewall's incident assistant. " +
	"Answer with the one action this admin would take for the new incident, judging by their past answers. " +
	"Blocks only ever hit the outside address."

// Prompt is the full prompt for a query, ending with CallPrefix. The past
// answers are the Shots most similar earlier cases.
func Prompt(q Query, past []Case, shots int) string {
	var b strings.Builder
	// llama-server and the tokenizer add <bos>
	b.WriteString("<start_of_turn>developer\n" + systemPrompt + declarations() + "<end_of_turn>\n<start_of_turn>user\n")
	if ex := similar(q, past, shots); len(ex) > 0 {
		b.WriteString("Past answers to similar incidents:\n")
		for _, c := range ex {
			b.WriteString("- " + Describe(c.F) + " -> " + callOf(c) + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("New incident: " + Describe(q.F) + "<end_of_turn>\n<start_of_turn>model\n" + CallPrefix)
	return b.String()
}

// Completion is the training target for a case (after CallPrefix).
func Completion(c Case) string { return callOf(c) + "<end_function_call>" }

func callOf(c Case) string {
	if c.Label == LabelBlock {
		return fmt.Sprintf("%s{minutes:%d}", c.Label, SnapMinutes(c.Minutes))
	}
	return c.Label + "{}"
}

// similar returns the n most similar cases before q, most similar last
// (closest to the question).
func similar(q Query, past []Case, n int) []Case {
	type scored struct {
		c   Case
		sim float64
	}
	var all []scored
	for _, c := range past {
		if !q.Time.IsZero() && !c.Time.Before(q.Time) {
			continue
		}
		all = append(all, scored{c, Similarity(q.F, c.F)})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].sim > all[j].sim })
	all = all[:min(n, len(all))]
	out := make([]Case, len(all))
	for i, s := range all {
		out[len(all)-1-i] = s.c
	}
	return out
}

// Describe is an incident as one line of text for the model.
func Describe(f Features) string {
	var b strings.Builder
	b.WriteString(f.Signature)
	if f.SignatureID != "" {
		b.WriteString(" [sid " + f.SignatureID + "]")
	}
	sev := map[int]string{1: "high", 2: "medium", 3: "low", 4: "info"}[f.Severity]
	if sev == "" {
		sev = "unknown"
	}
	fmt.Fprintf(&b, "; %s; severity %s; %s", f.Category, sev, f.Direction)
	local := f.Local
	if f.LocalName != "" {
		local = f.LocalName + " " + f.Local
	}
	switch f.Direction {
	case "inbound":
		fmt.Fprintf(&b, " from %s to %s", f.RemoteNet, local)
	case "outbound":
		fmt.Fprintf(&b, " from %s to %s", local, f.RemoteNet)
	default:
		fmt.Fprintf(&b, " at %s", local)
	}
	if f.Port > 0 {
		fmt.Fprintf(&b, " port %d", f.Port)
	}
	if f.Proto != "" {
		b.WriteString("/" + f.Proto)
	}
	fmt.Fprintf(&b, "; this alert from this address before: %d; this address before: %d", f.Seen, f.RemoteSeen)
	if f.Watched {
		b.WriteString("; address on the watch list")
	}
	if f.Blocked {
		b.WriteString("; address blocked")
	}
	if f.PastN > 0 {
		fmt.Fprintf(&b, "; earlier answers to this alert: %d, %.0f%% block, %.0f%% ignore", f.PastN, f.PastBlock*100, f.PastIgnore*100)
	}
	return b.String()
}

type completionReq struct {
	Prompt      string  `json:"prompt"`
	Grammar     string  `json:"grammar"`
	NPredict    int     `json:"n_predict"`
	Temperature float64 `json:"temperature"`
	NProbs      int     `json:"n_probs"`
	CachePrompt bool    `json:"cache_prompt"`
}

type tokenProb struct {
	Token   string   `json:"token"`
	Prob    *float64 `json:"prob"`    // with post_sampling_probs
	LogProb *float64 `json:"logprob"` // without
}

func (t tokenProb) p() float64 {
	switch {
	case t.Prob != nil:
		return *t.Prob
	case t.LogProb != nil:
		return math.Exp(*t.LogProb)
	}
	return 0
}

type completionResp struct {
	Content string `json:"content"`
	Probs   []struct {
		tokenProb
		Top    []tokenProb `json:"top_probs"`
		TopLog []tokenProb `json:"top_logprobs"`
	} `json:"completion_probabilities"`
}

// Complete sends a prompt to llama-server with the grammar and returns the
// raw answer and the first token's alternatives.
func (m *LLM) complete(ctx context.Context, prompt string) (completionResp, error) {
	var out completionResp
	body, _ := json.Marshal(completionReq{Prompt: prompt, Grammar: Grammar(), NPredict: 24, NProbs: 50, CachePrompt: true})
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimSuffix(m.Endpoint, "/")+"/completion", bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := m.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return out, fmt.Errorf("llama-server: %s", resp.Status)
	}
	return out, json.NewDecoder(resp.Body).Decode(&out)
}

// Predict asks the model.
func (m *LLM) Predict(q Query) (Prediction, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	shots := m.Shots
	if shots == 0 {
		shots = 5
	}
	resp, err := m.complete(ctx, Prompt(q, m.Past, shots))
	if err != nil {
		return Prediction{}, err
	}
	tool, minutes, err := ParseCall(resp.Content)
	if err != nil {
		return Prediction{Why: fmt.Sprintf("model said %q", resp.Content)}, err
	}
	p := Prediction{Tool: tool, Minutes: minutes, Confidence: firstTokenShare(resp, tool),
		Why: fmt.Sprintf("%s answered %s", m.Name, strings.TrimSpace(resp.Content))}
	return p, nil
}

// firstTokenShare: the probability of the chosen tool's first token, among
// the first tokens of the allowed tools.
func firstTokenShare(resp completionResp, tool string) float64 {
	if len(resp.Probs) == 0 {
		return 0
	}
	first := resp.Probs[0]
	top := append(first.Top, first.TopLog...)
	var chosen, allowed float64
	seen := map[string]bool{}
	for _, t := range top {
		if t.Token == "" || seen[t.Token] {
			continue
		}
		seen[t.Token] = true
		for _, c := range callTools {
			if strings.HasPrefix(c.name, t.Token) {
				allowed += t.p()
				if t.Token == first.Token && strings.HasPrefix(tool, t.Token) {
					chosen += t.p()
				}
				break
			}
		}
	}
	if allowed == 0 {
		return 0
	}
	return chosen / allowed
}
