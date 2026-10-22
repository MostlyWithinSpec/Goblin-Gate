package config

import (
	"fmt"
	"strings"
)

// CustomRule is one nftables rule an admin wrote for what policies cannot
// express. It is placed as written at a fixed point of the generated ruleset
// and checked with `nft -c` before a commit applies it.
type CustomRule struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Comment string `json:"comment,omitempty"`
	// Hook: input | forward (table inet goblin) | prerouting | postrouting
	// (table ip goblin_nat).
	Hook string `json:"hook"`
	// Position: "start" = after the built-in safety rules (blocks,
	// established/invalid, anti-spoofing; in prerouting the DNS redirect)
	// and before the generated rules; "end" (or "") = after all of them,
	// before the chain policy.
	Position string `json:"position,omitempty"`
	// Rule is one nftables rule body, e.g. `iifname "eth3" tcp dport 9100 accept`.
	Rule string `json:"rule"`
}

// MaxCustomRuleLen bounds one rule body.
const MaxCustomRuleLen = 512

// customRuleWords may not appear in a rule body (outside quoted strings):
// they would leave the rule (new tables, chains, flushes, includes) or jump
// into chains the generated ruleset owns. Braces must balance too, so a rule
// cannot close its chain; with no ';' or line break it stays one rule.
var customRuleWords = []string{
	"table", "chain", "flush", "delete", "destroy", "insert", "replace", "create",
	"include", "define", "redefine", "undefine", "rename", "jump", "goto",
}

// CustomRuleWords lists the words a custom rule may not use.
func CustomRuleWords() []string { return append([]string(nil), customRuleWords...) }

func (v *validator) checkCustomRules() {
	for i, r := range v.cfg.CustomRules {
		p := fmt.Sprintf("custom_rules[%d]", i)
		if !oneOf(r.Hook, "input", "forward", "prerouting", "postrouting") {
			v.add(p+".hook", "custom rule %q: unknown hook %q: want input, forward, prerouting or postrouting", r.Name, r.Hook)
		}
		if !oneOf(r.Position, "", "start", "end") {
			v.add(p+".position", "custom rule %q: unknown position %q: want start or end", r.Name, r.Position)
		}
		if msg := CheckCustomRuleText(r.Rule); msg != "" {
			v.add(p+".rule", "custom rule %q: %s", r.Name, msg)
		}
		if len(r.Comment) > 200 || strings.ContainsAny(r.Comment, "\"\n\r") {
			v.add(p+".comment", "custom rule %q: comment must be at most 200 characters without quotes or line breaks", r.Name)
		}
	}
}

// CheckCustomRuleText returns why a rule body is refused, or "". It does not
// check nftables syntax: `nft -c` does that on the whole rendered ruleset.
func CheckCustomRuleText(rule string) string {
	s := strings.TrimSpace(rule)
	switch {
	case s == "":
		return "the rule is empty"
	case len(s) > MaxCustomRuleLen:
		return fmt.Sprintf("the rule is longer than %d characters", MaxCustomRuleLen)
	case strings.ContainsAny(s, "\n\r"):
		return "one rule per entry: no line breaks"
	}
	words, quoted, ok := customRuleTokens(s)
	if !ok {
		return "unbalanced quotes"
	}
	if strings.ContainsAny(quoted, ";#") {
		return "one rule per entry: no ';' or '#' outside quotes"
	}
	depth := 0
	for _, r := range quoted {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
		}
		if depth < 0 {
			break
		}
	}
	if depth != 0 {
		return "unbalanced braces"
	}
	for _, w := range words {
		for _, bad := range customRuleWords {
			if strings.EqualFold(w, bad) {
				return fmt.Sprintf("%q is not allowed: a custom rule is a single rule in a generated chain", bad)
			}
		}
	}
	return ""
}

// customRuleTokens splits s into words outside double quotes; unquoted is s
// with the quoted parts removed. ok is false for an unterminated quote.
func customRuleTokens(s string) (words []string, unquoted string, ok bool) {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '"':
			in = !in
			b.WriteRune(' ')
		case in:
		default:
			b.WriteRune(r)
		}
	}
	unquoted = b.String()
	f := func(r rune) bool { return r == ' ' || r == '\t' || r == '{' || r == '}' || r == ',' }
	return strings.FieldsFunc(unquoted, f), unquoted, !in
}
