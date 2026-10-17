package sentry

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

// Rule is one entry of the ordered rule list: the first enabled rule that
// matches an incident decides its recommendation.
type Rule struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Match   Match  `json:"match"`
	Action  Action `json:"action"` // Target is filled from the incident
	// Auto lets the rule act in auto mode even with a disruptive tool
	// (a block); otherwise such actions still ask. Set by "Always do this".
	Auto    bool   `json:"auto,omitempty"`
	Builtin bool   `json:"builtin,omitempty"`
	Comment string `json:"comment,omitempty"`
}

// Match conditions; empty ones match anything. All given ones must hold.
type Match struct {
	Signature   string `json:"signature,omitempty"`    // case-insensitive substring
	SignatureID string `json:"signature_id,omitempty"` // exact
	Category    string `json:"category,omitempty"`     // case-insensitive substring
	// MaxSeverity matches severities 1..MaxSeverity (1 = high); 0 = any.
	MaxSeverity int    `json:"max_severity,omitempty"`
	Direction   string `json:"direction,omitempty"`
	// Remote and Local are an address or a CIDR prefix.
	Remote string `json:"remote,omitempty"`
	Local  string `json:"local,omitempty"`
}

// disruptive tools cut traffic.
func disruptive(tool string) bool { return tool == ToolBlock }

// DefaultRules are conservative: nothing is blocked without the admin,
// internal hosts are never blocked, low-severity noise is only recorded.
func DefaultRules() []Rule {
	return []Rule{
		{ID: 1, Name: "High severity from outside: ask to block", Enabled: true, Builtin: true,
			Match:  Match{Direction: Inbound, MaxSeverity: 1},
			Action: Action{Tool: ToolRequestApproval}},
		{ID: 2, Name: "Internal host talking out: tell the admin", Enabled: true, Builtin: true,
			Match:  Match{Direction: Outbound, MaxSeverity: 2},
			Action: Action{Tool: ToolEscalate}},
		{ID: 3, Name: "Between internal hosts: tell the admin", Enabled: true, Builtin: true,
			Match:  Match{Direction: Lateral, MaxSeverity: 2},
			Action: Action{Tool: ToolEscalate}},
		{ID: 4, Name: "Medium severity from outside: ask to block", Enabled: true, Builtin: true,
			Match:  Match{Direction: Inbound, MaxSeverity: 2},
			Action: Action{Tool: ToolRequestApproval}},
		{ID: 5, Name: "Everything else: keep an eye on it", Enabled: true, Builtin: true,
			Action: Action{Tool: ToolWatch}},
	}
}

func inPrefix(spec, ip string) bool {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	if p, err := netip.ParsePrefix(spec); err == nil {
		return p.Masked().Contains(a.Unmap())
	}
	b, err := netip.ParseAddr(spec)
	return err == nil && b.Unmap() == a.Unmap()
}

// Matches reports whether the rule applies to an incident.
func (r Rule) Matches(inc *Incident) bool {
	m, a, e := r.Match, inc.Alert, inc.Enrichment
	if m.Signature != "" && !strings.Contains(strings.ToLower(a.Signature), strings.ToLower(m.Signature)) {
		return false
	}
	if m.SignatureID != "" && m.SignatureID != a.SignatureID {
		return false
	}
	if m.Category != "" && !strings.Contains(strings.ToLower(a.Category), strings.ToLower(m.Category)) {
		return false
	}
	if m.MaxSeverity > 0 && (a.Severity < 1 || a.Severity > m.MaxSeverity) {
		return false
	}
	if m.Direction != "" && m.Direction != e.Direction {
		return false
	}
	if m.Remote != "" && !inPrefix(m.Remote, e.Remote) {
		return false
	}
	if m.Local != "" && !inPrefix(m.Local, e.Local) {
		return false
	}
	return true
}

// CheckRules validates a rule list and returns its problems.
func CheckRules(rules []Rule) []string {
	var out []string
	ids := map[int64]bool{}
	for i, r := range rules {
		at := fmt.Sprintf("rules[%d]", i)
		if r.ID <= 0 || ids[r.ID] {
			out = append(out, at+".id: must be a unique positive number")
		}
		ids[r.ID] = true
		if strings.TrimSpace(r.Name) == "" || len(r.Name) > 200 {
			out = append(out, at+".name: 1-200 characters")
		}
		if !slices.Contains(Tools, r.Action.Tool) {
			out = append(out, at+".action.tool: unknown tool "+r.Action.Tool)
		}
		if r.Action.Tool == ToolBlock && (r.Action.Minutes < 1 || r.Action.Minutes > 10080) {
			out = append(out, at+".action.minutes: 1-10080 for a block")
		}
		if r.Action.Target != "" {
			out = append(out, at+".action.target: set from the incident, leave it empty")
		}
		if r.Match.MaxSeverity < 0 || r.Match.MaxSeverity > 4 {
			out = append(out, at+".match.max_severity: 0-4")
		}
		if d := r.Match.Direction; d != "" && !slices.Contains([]string{Inbound, Outbound, Lateral, External}, d) {
			out = append(out, at+".match.direction: inbound, outbound, lateral or external")
		}
		for _, f := range []struct{ name, v string }{{"remote", r.Match.Remote}, {"local", r.Match.Local}} {
			if f.v == "" {
				continue
			}
			if _, err := netip.ParsePrefix(f.v); err != nil {
				if _, err := netip.ParseAddr(f.v); err != nil {
					out = append(out, at+".match."+f.name+": an address or CIDR prefix")
				}
			}
		}
		if r.Auto && r.Action.Tool != ToolBlock {
			out = append(out, at+".auto: only meaningful for blocks")
		}
	}
	return out
}
