// Package sentry is Goblin Sentry's response engine: IPS alerts become
// incidents (deduplicated), are enriched with what the box knows, matched
// against an ordered rule list and answered with one of a few constrained
// tools - never a shell. The mode decides how much happens without the
// admin: off, shadow (record only), approve (ask first) or auto (rules
// act; blocks only where the admin said "always"). Every decision keeps a
// "why" record, and the admin's answers are kept as labelled examples for
// the G.O.B.L.I.N. decision model.
package sentry

import "time"

// Modes.
const (
	ModeOff     = "off"
	ModeShadow  = "shadow"
	ModeApprove = "approve"
	ModeAuto    = "auto"
)

// Modes lists the modes in order of autonomy.
var Modes = []string{ModeOff, ModeShadow, ModeApprove, ModeAuto}

// Tools: the only actions Sentry can take.
const (
	ToolBlock           = "temporary_block_ip" // goblind temporary block (blocks:write)
	ToolWatch           = "add_watch"          // remember an address: later incidents mention it
	ToolReputation      = "query_reputation"   // local history: incidents, blocks
	ToolNotify          = "notify_admin"       // notification (notify:send)
	ToolRequestApproval = "request_approval"   // put the incident in the queue
	ToolFalsePositive   = "mark_false_positive"
	ToolEscalate        = "escalate" // notify and queue, marked urgent
)

// Tools lists every tool.
var Tools = []string{ToolBlock, ToolWatch, ToolReputation, ToolNotify, ToolRequestApproval, ToolFalsePositive, ToolEscalate}

// Directions of an incident, seen from the box's networks.
const (
	Inbound  = "inbound"  // from outside to the box or an internal host
	Outbound = "outbound" // from an internal host to outside
	Lateral  = "lateral"  // between internal hosts
	External = "external" // neither end internal (transit)
)

// Alert is one IPS event as Sentry reads it from goblind's log.
type Alert struct {
	ID          int64  `json:"id"`
	Time        int64  `json:"time"`
	Action      string `json:"action"` // alert | drop
	SrcIP       string `json:"src_ip"`
	SrcPort     int    `json:"src_port,omitempty"`
	DstIP       string `json:"dst_ip"`
	DstPort     int    `json:"dst_port,omitempty"`
	Proto       string `json:"proto,omitempty"`
	InIntf      string `json:"in_intf,omitempty"`
	Signature   string `json:"signature"`
	SignatureID string `json:"signature_id,omitempty"`
	Category    string `json:"category,omitempty"`
	Severity    int    `json:"severity"` // 1 = high ... 4 = low (Suricata); 0 = unknown
}

// Enrichment is what Sentry added to an incident.
type Enrichment struct {
	Direction string `json:"direction"`
	// Remote is the address outside the box's networks (the block
	// target); "" for lateral traffic.
	Remote string `json:"remote,omitempty"`
	// Local is the internal host involved, with its name and network.
	Local      string `json:"local,omitempty"`
	LocalName  string `json:"local_name,omitempty"`
	LocalIntf  string `json:"local_interface,omitempty"`
	Seen       int    `json:"seen"`        // earlier incidents with this signature and remote
	RemoteSeen int    `json:"remote_seen"` // earlier incidents involving the remote address
	Watched    bool   `json:"watched"`     // the remote is on the watch list
	Blocked    bool   `json:"blocked"`     // the remote is blocked right now
	// PastAnswers counts the admin's earlier answers for this signature.
	PastAnswers map[string]int `json:"past_answers,omitempty"`
}

// Incident statuses.
const (
	StatusPending  = "pending"  // waiting for the admin
	StatusActed    = "acted"    // an action ran
	StatusRecorded = "recorded" // nothing to do (or shadow mode)
	StatusIgnored  = "ignored"  // the admin said ignore / false positive
	StatusFailed   = "failed"   // an action failed or was refused
)

// Incident groups alerts with the same signature and remote address.
type Incident struct {
	ID         int64      `json:"id"`
	Key        string     `json:"key"`
	Status     string     `json:"status"`
	Urgent     bool       `json:"urgent,omitempty"`
	First      time.Time  `json:"first"`
	Last       time.Time  `json:"last"`
	Count      int        `json:"count"`
	Alert      Alert      `json:"alert"` // the first alert
	Enrichment Enrichment `json:"enrichment"`
	// Recommendation is what Sentry would do (or did).
	Recommendation Action `json:"recommendation"`
	Summary        string `json:"summary"`
	Answer         string `json:"answer,omitempty"` // the admin's answer
	AnsweredBy     string `json:"answered_by,omitempty"`
}

// Action is a tool call.
type Action struct {
	Tool    string `json:"tool"`
	Target  string `json:"target,omitempty"`  // an IP for block/watch/reputation
	Minutes int    `json:"minutes,omitempty"` // block length
}

// Decision is the "why" record of one step taken for an incident.
type Decision struct {
	ID         int64      `json:"id"`
	IncidentID int64      `json:"incident_id"`
	Time       time.Time  `json:"time"`
	Mode       string     `json:"mode"`
	By         string     `json:"by"` // rule:<id> | default | admin:<user>
	RuleName   string     `json:"rule_name,omitempty"`
	Inputs     Enrichment `json:"inputs"`
	Action     Action     `json:"action"`
	Executed   bool       `json:"executed"`
	Result     string     `json:"result"`
	// Model is the decision model's advice, when it was asked.
	Model *ModelAdvice `json:"model,omitempty"`
}

// Example is an admin's answer to a recommendation: training data for the
// decision model.
type Example struct {
	Time           time.Time  `json:"time"`
	Alert          Alert      `json:"alert"`
	Enrichment     Enrichment `json:"enrichment"`
	Recommendation Action     `json:"recommendation"`
	Answer         string     `json:"answer"`
	// Action is what ran on the answer (with the admin's block length).
	Action Action `json:"action,omitempty"`
}

// Advice is the decision model's answer (G.O.B.L.I.N., package goblin) for
// an incident the rules would put in front of the admin.
type Advice struct {
	Model string `json:"model"` // e.g. "v3 (knn)"
	// Action is one tool call; the engine fills its target from the
	// incident (the model never chooses an address).
	Action     Action  `json:"action"`
	Confidence float64 `json:"confidence"` // 0..1
	// Act: the admin lets the model act and the confidence is above the
	// admin's threshold for this action. Disruptive actions (blocks) and
	// escalations still ask.
	Act bool   `json:"act"`
	Why string `json:"why"` // nearest past decisions or a short rationale
}

// Advisor is the decision model as the engine sees it. Advise returns
// false when there is no model or it has nothing to say.
type Advisor interface {
	Advise(inc Incident) (Advice, bool)
}

// ModelAdvice is the model's part of a why record.
type ModelAdvice struct {
	Advice
	Used bool `json:"used"` // the action ran (otherwise it was a suggestion)
}
