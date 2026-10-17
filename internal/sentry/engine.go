package sentry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Goblind is what Sentry asks goblind to do, each through its own plugin
// capability.
type Goblind interface {
	Block(ctx context.Context, ip string, minutes int, reason string) error // blocks:write
	Blocked(ctx context.Context) (map[string]bool, error)                   // blocks:read
	Notify(ctx context.Context, title, message, key string) error           // notify:send
}

// Settings are the plugin settings Sentry runs with.
type Settings struct {
	Mode         string
	DedupWindow  time.Duration // alerts with the same key within it join an incident
	BlockMinutes int           // block length when a rule or answer gives none
}

// Limits on what is kept.
const (
	maxIncidents = 5000
	maxDecisions = 20000
	watchFor     = 24 * time.Hour
)

// state is persisted in <Dir>/state.json.
type state struct {
	Cursor       int64                `json:"cursor"` // last log event id read
	NextIncident int64                `json:"next_incident"`
	NextDecision int64                `json:"next_decision"`
	Incidents    []*Incident          `json:"incidents"`
	Decisions    []Decision           `json:"decisions"`
	Rules        []Rule               `json:"rules"`
	Watches      map[string]time.Time `json:"watches"`
}

// Engine is Goblin Sentry. Create it with New.
type Engine struct {
	Dir      string // state directory
	Goblind  Goblind
	Settings Settings
	Now      func() time.Time
	Logger   *slog.Logger
	// Advisor is the decision model, asked when the rules would put an
	// incident in front of the admin; nil = rules and approval only.
	Advisor Advisor

	mu  sync.Mutex
	st  *state
	net *Network
}

// New loads the engine's state from dir (empty on first run, with the
// default rules).
func New(dir string, g Goblind, s Settings) (*Engine, error) {
	e := &Engine{Dir: dir, Goblind: g, Settings: s}
	if e.Settings.DedupWindow <= 0 {
		e.Settings.DedupWindow = time.Hour
	}
	if e.Settings.BlockMinutes <= 0 {
		e.Settings.BlockMinutes = 60
	}
	if !slices.Contains(Modes, e.Settings.Mode) {
		e.Settings.Mode = ModeApprove
	}
	e.st = &state{NextIncident: 1, NextDecision: 1, Watches: map[string]time.Time{}}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(e.statePath())
	switch {
	case err == nil:
		if err := json.Unmarshal(data, e.st); err != nil {
			return nil, fmt.Errorf("sentry: %s: %w", e.statePath(), err)
		}
		if e.st.Watches == nil {
			e.st.Watches = map[string]time.Time{}
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}
	if e.st.Rules == nil {
		e.st.Rules = DefaultRules()
	}
	return e, nil
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Engine) logger() *slog.Logger {
	if e.Logger != nil {
		return e.Logger
	}
	return slog.Default()
}

func (e *Engine) statePath() string { return filepath.Join(e.Dir, "state.json") }

func (e *Engine) saveLocked() {
	data, err := json.Marshal(e.st)
	if err == nil {
		tmp := e.statePath() + ".tmp"
		if err = os.WriteFile(tmp, data, 0o600); err == nil {
			err = os.Rename(tmp, e.statePath())
		}
	}
	if err != nil {
		e.logger().Error("sentry: saving state failed", "error", err)
	}
}

// SetNetwork updates what Sentry knows about the box's networks.
func (e *Engine) SetNetwork(n *Network) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.net = n
}

// Cursor is the last log event id Sentry has read.
func (e *Engine) Cursor() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.st.Cursor
}

// Mode is the mode Sentry runs in.
func (e *Engine) Mode() string { return e.Settings.Mode }

// Ingest handles new alerts (any order): they join an open incident with
// the same key or start a new one, which is enriched and decided on.
func (e *Engine) Ingest(ctx context.Context, alerts []Alert) {
	sort.Slice(alerts, func(i, j int) bool { return alerts[i].ID < alerts[j].ID })
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(alerts) == 0 {
		return
	}
	defer e.saveLocked()
	if last := alerts[len(alerts)-1].ID; last > e.st.Cursor {
		e.st.Cursor = last
	}
	if e.Settings.Mode == ModeOff {
		return
	}
	var blocked map[string]bool
	for _, a := range alerts {
		if inc := e.joinLocked(a); inc != nil {
			continue
		}
		if blocked == nil {
			var err error
			if blocked, err = e.Goblind.Blocked(ctx); err != nil {
				blocked = map[string]bool{}
			}
		}
		inc := e.newIncidentLocked(a, blocked)
		e.decideLocked(ctx, inc)
	}
}

func incidentKey(a Alert, en Enrichment) string {
	sig := a.SignatureID
	if sig == "" {
		sig = a.Signature
	}
	who := en.Remote
	if who == "" {
		who = a.SrcIP
	}
	return sig + "|" + who
}

// joinLocked adds an alert to a recent incident with the same key.
func (e *Engine) joinLocked(a Alert) *Incident {
	var en Enrichment
	e.net.classify(a, &en)
	key := incidentKey(a, en)
	t := time.Unix(a.Time, 0)
	for i := len(e.st.Incidents) - 1; i >= 0; i-- {
		inc := e.st.Incidents[i]
		if inc.Key == key && t.Sub(inc.Last) <= e.Settings.DedupWindow && !t.Before(inc.First.Add(-e.Settings.DedupWindow)) {
			inc.Count++
			if t.After(inc.Last) {
				inc.Last = t
			}
			inc.Summary = summarize(inc)
			return inc
		}
	}
	return nil
}

func (e *Engine) newIncidentLocked(a Alert, blocked map[string]bool) *Incident {
	t := time.Unix(a.Time, 0)
	inc := &Incident{ID: e.st.NextIncident, First: t, Last: t, Count: 1, Alert: a}
	e.st.NextIncident++
	en := &inc.Enrichment
	e.net.classify(a, en)
	inc.Key = incidentKey(a, *en)
	now := e.now()
	for _, old := range e.st.Incidents {
		if old.Alert.Signature == a.Signature && old.Enrichment.Remote == en.Remote {
			en.Seen++
		}
		if en.Remote != "" && old.Enrichment.Remote == en.Remote {
			en.RemoteSeen++
		}
		if old.Answer != "" && old.Alert.Signature == a.Signature {
			if en.PastAnswers == nil {
				en.PastAnswers = map[string]int{}
			}
			en.PastAnswers[old.Answer]++
		}
	}
	if until, ok := e.st.Watches[en.Remote]; ok && now.Before(until) {
		en.Watched = true
	}
	en.Blocked = blocked[en.Remote]
	inc.Summary = summarize(inc)
	e.st.Incidents = append(e.st.Incidents, inc)
	e.trimLocked()
	return inc
}

func summarize(inc *Incident) string {
	a, en := inc.Alert, inc.Enrichment
	local := en.Local
	if en.LocalName != "" {
		local = en.LocalName + " (" + en.Local + ")"
	}
	var s string
	switch en.Direction {
	case Inbound:
		s = fmt.Sprintf("%s from %s to %s", a.Signature, en.Remote, local)
	case Outbound:
		s = fmt.Sprintf("%s from %s to %s", a.Signature, local, en.Remote)
	case Lateral:
		s = fmt.Sprintf("%s between %s and %s", a.Signature, local, a.DstIP)
	default:
		s = fmt.Sprintf("%s from %s to %s", a.Signature, a.SrcIP, a.DstIP)
	}
	if inc.Count > 1 {
		s += fmt.Sprintf(", %d times", inc.Count)
	}
	return s
}

// trimLocked keeps the newest incidents and decisions; pending ones stay.
func (e *Engine) trimLocked() {
	if over := len(e.st.Incidents) - maxIncidents; over > 0 {
		kept := e.st.Incidents[:0]
		for _, inc := range e.st.Incidents {
			if over > 0 && inc.Status != StatusPending {
				over--
				continue
			}
			kept = append(kept, inc)
		}
		e.st.Incidents = kept
	}
	if over := len(e.st.Decisions) - maxDecisions; over > 0 {
		e.st.Decisions = append([]Decision(nil), e.st.Decisions[over:]...)
	}
}

// recommend applies the rules: the recommended action, who decided and
// the rule (nil for the default).
func (e *Engine) recommendLocked(inc *Incident) (Action, string, *Rule) {
	for i := range e.st.Rules {
		r := &e.st.Rules[i]
		if r.Enabled && r.Matches(inc) {
			return e.target(r.Action, inc), fmt.Sprintf("rule:%d", r.ID), r
		}
	}
	return e.target(Action{Tool: ToolRequestApproval}, inc), "default", nil
}

// target fills an action's target from the incident. A block needs a
// remote address: internal hosts are never blocked, so a block of a
// lateral incident becomes an escalation.
func (e *Engine) target(a Action, inc *Incident) Action {
	switch a.Tool {
	case ToolBlock:
		if inc.Enrichment.Remote == "" || inc.Enrichment.Direction == Lateral {
			return Action{Tool: ToolEscalate}
		}
		a.Target = inc.Enrichment.Remote
		if a.Minutes == 0 {
			a.Minutes = e.Settings.BlockMinutes
		}
	case ToolWatch, ToolReputation:
		a.Target = inc.Enrichment.Remote
		if a.Target == "" {
			a.Target = inc.Enrichment.Local
		}
	}
	return a
}

// suggestion is what Approve does for an incident that asks.
func (e *Engine) suggestion(inc *Incident) Action {
	if inc.Enrichment.Remote != "" && inc.Enrichment.Direction == Inbound {
		return Action{Tool: ToolBlock, Target: inc.Enrichment.Remote, Minutes: e.Settings.BlockMinutes}
	}
	return e.target(Action{Tool: ToolWatch}, inc)
}

// decideLocked runs the rules on a new incident and acts as the mode allows.
//
//   - shadow: records what would happen, runs nothing.
//   - approve: blocks, approval requests and escalations wait in the
//     queue; notifications and the watch list run.
//   - auto: everything runs, except blocks from rules without "auto",
//     which wait in the queue.
//
// When the rules would ask the admin, the decision model (Advisor) is
// asked first: a confident, non-disruptive answer the admin lets it give
// runs instead (in shadow mode it is only recorded); anything else only
// becomes the suggestion shown with the question.
func (e *Engine) decideLocked(ctx context.Context, inc *Incident) {
	act, by, rule := e.recommendLocked(inc)
	d := Decision{IncidentID: inc.ID, Mode: e.Settings.Mode, By: by, Inputs: inc.Enrichment, Action: act}
	if rule != nil {
		d.RuleName = rule.Name
	}
	var advised *Action
	if act.Tool == ToolRequestApproval && e.Advisor != nil {
		if adv, ok := e.Advisor.Advise(*inc); ok {
			adv.Action = e.target(adv.Action, inc)
			d.Model = &ModelAdvice{Advice: adv}
			switch a := adv.Action; {
			case adv.Act && modelMayRun(a.Tool):
				act, by = a, "model:"+adv.Model
				d.By, d.Action, d.RuleName = by, act, ""
			case suggestable(a.Tool):
				advised = &a
			}
		}
	}
	inc.Recommendation = act
	ask := func(urgent bool, why string) {
		if act.Tool == ToolRequestApproval || act.Tool == ToolEscalate {
			inc.Recommendation = e.suggestion(inc)
			if advised != nil {
				inc.Recommendation = *advised
				why += fmt.Sprintf("; G.O.B.L.I.N. suggests: %s (%.0f%% sure)", describe(*advised), d.Model.Confidence*100)
			}
		}
		inc.Status, inc.Urgent = StatusPending, urgent
		title := "Goblin Sentry needs a decision"
		if urgent {
			title = "Goblin Sentry: urgent"
		}
		msg := inc.Summary + ". Suggested: " + describe(inc.Recommendation) + "."
		d.Result = why
		if err := e.Goblind.Notify(ctx, title, msg, fmt.Sprintf("incident-%d", inc.ID)); err != nil {
			d.Result += " (notification failed: " + err.Error() + ")"
		}
	}
	switch {
	case e.Settings.Mode == ModeShadow:
		inc.Status = StatusRecorded
		d.Result = "shadow mode: would " + describe(act)
	case act.Tool == ToolRequestApproval:
		ask(false, "waiting for the admin")
	case act.Tool == ToolEscalate:
		ask(true, "escalated to the admin")
	case act.Tool == ToolBlock && (e.Settings.Mode == ModeApprove || rule == nil || !rule.Auto):
		ask(false, "waiting for the admin (blocks need approval)")
	default:
		d.Executed = true
		d.Result = e.executeLocked(ctx, inc, act, "Goblin Sentry: "+inc.Alert.Signature)
	}
	if d.Model != nil {
		d.Model.Used = d.Executed && strings.HasPrefix(d.By, "model:")
	}
	e.recordLocked(d)
}

// modelMayRun: the decision model may only run actions that cut no
// traffic and do not need the admin. Blocks always ask (unless a rule the
// admin made says "always").
func modelMayRun(tool string) bool {
	switch tool {
	case ToolWatch, ToolReputation, ToolNotify, ToolFalsePositive:
		return true
	}
	return false
}

// suggestable: actions Approve can run for a queued incident.
func suggestable(tool string) bool { return tool == ToolBlock || modelMayRun(tool) }

func describe(a Action) string {
	switch a.Tool {
	case ToolBlock:
		return fmt.Sprintf("block %s for %d minutes", a.Target, a.Minutes)
	case ToolWatch:
		return "watch " + a.Target
	case ToolReputation:
		return "look up " + a.Target
	case ToolNotify:
		return "notify the admin"
	case ToolRequestApproval:
		return "ask the admin"
	case ToolEscalate:
		return "escalate to the admin"
	case ToolFalsePositive:
		return "mark as false positive"
	}
	return a.Tool
}

func (e *Engine) recordLocked(d Decision) {
	d.ID = e.st.NextDecision
	e.st.NextDecision++
	d.Time = e.now()
	e.st.Decisions = append(e.st.Decisions, d)
	e.trimLocked()
}

// executeLocked runs one tool and sets the incident's status; it returns
// the result for the why record.
func (e *Engine) executeLocked(ctx context.Context, inc *Incident, a Action, reason string) string {
	inc.Status = StatusActed
	switch a.Tool {
	case ToolBlock:
		if err := e.Goblind.Block(ctx, a.Target, a.Minutes, reason); err != nil {
			inc.Status = StatusFailed
			return "refused by goblind: " + err.Error()
		}
		// verify: the block is in place
		if b, err := e.Goblind.Blocked(ctx); err == nil && !b[a.Target] {
			inc.Status = StatusFailed
			return "blocked, but the block is not listed"
		}
		return fmt.Sprintf("blocked %s for %d minutes (verified)", a.Target, a.Minutes)
	case ToolWatch:
		if a.Target != "" {
			e.st.Watches[a.Target] = e.now().Add(watchFor)
		}
		inc.Status = StatusRecorded
		return "watching " + a.Target + " for 24 hours"
	case ToolReputation:
		inc.Status = StatusRecorded
		return e.reputationLocked(a.Target)
	case ToolNotify:
		if err := e.Goblind.Notify(ctx, "Goblin Sentry", inc.Summary, fmt.Sprintf("incident-%d", inc.ID)); err != nil {
			inc.Status = StatusFailed
			return "notification failed: " + err.Error()
		}
		return "notified the admin"
	case ToolFalsePositive:
		inc.Status = StatusIgnored
		return "marked as false positive"
	}
	inc.Status = StatusFailed
	return "unknown tool " + a.Tool
}

func (e *Engine) reputationLocked(ip string) string {
	n, answers := 0, map[string]int{}
	for _, inc := range e.st.Incidents {
		if inc.Enrichment.Remote == ip {
			n++
			if inc.Answer != "" {
				answers[inc.Answer]++
			}
		}
	}
	return fmt.Sprintf("%s: %d incidents here, admin answers %v", ip, n, answers)
}

// Answers the admin can give.
const (
	AnswerBlock   = "block"
	AnswerIgnore  = "ignore"
	AnswerApprove = "approve"
	AnswerAlways  = "always"
)

// ErrNotFound is returned for an unknown incident.
var ErrNotFound = errors.New("no such incident")

// Answer records the admin's answer to a pending incident and acts on it:
// block (the remote address, minutes 0 = default), ignore, approve (run
// the recommendation) or always (add a rule that does the recommendation
// from now on - on its own in auto mode - and run it). Answers are kept as
// examples for the decision model.
func (e *Engine) Answer(ctx context.Context, id int64, answer, user string, minutes int) (*Incident, Decision, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var inc *Incident
	for _, i := range e.st.Incidents {
		if i.ID == id {
			inc = i
		}
	}
	if inc == nil {
		return nil, Decision{}, ErrNotFound
	}
	if inc.Status != StatusPending {
		return nil, Decision{}, fmt.Errorf("incident %d is not waiting for an answer (status %s)", id, inc.Status)
	}
	if minutes < 0 || minutes > 10080 {
		return nil, Decision{}, errors.New("minutes: 1-10080")
	}
	d := Decision{IncidentID: id, Mode: e.Settings.Mode, By: "admin:" + user, Inputs: inc.Enrichment}
	rec := inc.Recommendation
	switch answer {
	case AnswerBlock:
		if inc.Enrichment.Remote == "" || inc.Enrichment.Direction == Lateral {
			return nil, Decision{}, errors.New("this incident has no outside address to block")
		}
		if minutes == 0 {
			minutes = e.Settings.BlockMinutes
		}
		d.Action = Action{Tool: ToolBlock, Target: inc.Enrichment.Remote, Minutes: minutes}
	case AnswerIgnore:
		d.Action = Action{Tool: ToolFalsePositive}
	case AnswerApprove:
		d.Action = rec
		if rec.Tool == ToolBlock && minutes > 0 {
			d.Action.Minutes = minutes
		}
	case AnswerAlways:
		d.Action = rec
		r := Rule{ID: e.nextRuleIDLocked(), Enabled: true, Auto: rec.Tool == ToolBlock,
			Name:    "Always: " + inc.Alert.Signature,
			Match:   Match{SignatureID: inc.Alert.SignatureID, Direction: inc.Enrichment.Direction},
			Action:  Action{Tool: rec.Tool, Minutes: rec.Minutes},
			Comment: fmt.Sprintf("from incident %d, by %s", id, user)}
		if r.Match.SignatureID == "" {
			r.Match.Signature = inc.Alert.Signature
		}
		e.st.Rules = append([]Rule{r}, e.st.Rules...)
		d.RuleName = r.Name
	default:
		return nil, Decision{}, fmt.Errorf("answer must be %s, %s, %s or %s", AnswerBlock, AnswerIgnore, AnswerApprove, AnswerAlways)
	}
	inc.Answer, inc.AnsweredBy = answer, user
	d.Executed = true
	d.Result = e.executeLocked(ctx, inc, d.Action, fmt.Sprintf("Goblin Sentry, approved by %s: %s", user, inc.Alert.Signature))
	if answer == AnswerIgnore {
		inc.Status = StatusIgnored
	}
	e.recordLocked(d)
	e.saveLocked()
	e.appendExample(Example{Time: e.now(), Alert: inc.Alert, Enrichment: inc.Enrichment, Recommendation: rec, Answer: answer, Action: d.Action})
	cp := *inc
	return &cp, e.st.Decisions[len(e.st.Decisions)-1], nil
}

func (e *Engine) nextRuleIDLocked() int64 {
	var max int64
	for _, r := range e.st.Rules {
		if r.ID > max {
			max = r.ID
		}
	}
	return max + 1
}

// appendExample adds one line to <Dir>/examples.jsonl.
func (e *Engine) appendExample(x Example) {
	line, err := json.Marshal(x)
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(e.Dir, "examples.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		e.logger().Error("sentry: saving an example failed", "error", err)
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

// RemoveExample drops the admin's answer to the incident that started
// with alert id from the examples (a wrong answer the model should not
// learn). It reports whether there was one.
func (e *Engine) RemoveExample(alertID int64) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	path := filepath.Join(e.Dir, "examples.jsonl")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var kept []byte
	found := false
	for _, line := range bytes.SplitAfter(data, []byte("\n")) {
		var x Example
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if json.Unmarshal(line, &x) == nil && x.Alert.ID == alertID {
			found = true
			continue
		}
		kept = append(kept, line...)
	}
	if !found {
		return false, nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, kept, 0o600); err != nil {
		return false, err
	}
	return true, os.Rename(tmp, path)
}

// Incidents returns incidents newest first, optionally only one status.
func (e *Engine) Incidents(status string, limit int) []Incident {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := []Incident{}
	for i := len(e.st.Incidents) - 1; i >= 0 && (limit <= 0 || len(out) < limit); i-- {
		if status == "" || e.st.Incidents[i].Status == status {
			out = append(out, *e.st.Incidents[i])
		}
	}
	return out
}

// Incident returns one incident with its decisions (oldest first).
func (e *Engine) Incident(id int64) (Incident, []Decision, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, inc := range e.st.Incidents {
		if inc.ID == id {
			ds := []Decision{}
			for _, d := range e.st.Decisions {
				if d.IncidentID == id {
					ds = append(ds, d)
				}
			}
			return *inc, ds, true
		}
	}
	return Incident{}, nil, false
}

// Decisions returns the newest decisions first.
func (e *Engine) Decisions(limit int) []Decision {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := []Decision{}
	for i := len(e.st.Decisions) - 1; i >= 0 && (limit <= 0 || len(out) < limit); i-- {
		out = append(out, e.st.Decisions[i])
	}
	return out
}

// Rules returns the rule list.
func (e *Engine) Rules() []Rule {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.st.Rules)
}

// SetRules replaces the rule list after checking it.
func (e *Engine) SetRules(rules []Rule) []string {
	if rules == nil {
		rules = []Rule{}
	}
	if problems := CheckRules(rules); len(problems) > 0 {
		return problems
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.st.Rules = slices.Clone(rules)
	e.saveLocked()
	return nil
}

// Status is an overview for the admin UI.
type Status struct {
	Mode      string         `json:"mode"`
	Counts    map[string]int `json:"counts"` // incidents per status
	Rules     int            `json:"rules"`
	Watches   []string       `json:"watches"`
	Cursor    int64          `json:"cursor"`
	Incidents int            `json:"incidents"`
}

// Status returns the overview.
func (e *Engine) Status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := Status{Mode: e.Settings.Mode, Counts: map[string]int{}, Rules: len(e.st.Rules), Watches: []string{}, Cursor: e.st.Cursor, Incidents: len(e.st.Incidents)}
	for _, inc := range e.st.Incidents {
		s.Counts[inc.Status]++
	}
	now := e.now()
	for ip, until := range e.st.Watches {
		if now.Before(until) {
			s.Watches = append(s.Watches, ip)
		}
	}
	sort.Strings(s.Watches)
	return s
}

// StartAt sets the read position on the first run, so Sentry does not
// judge alerts from before it was installed.
func (e *Engine) StartAt(id int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.st.Cursor == 0 && id > 0 {
		e.st.Cursor = id
		e.saveLocked()
	}
}
