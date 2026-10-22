package nft

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// Custom rules (task 100): admin-written rules placed as written at fixed
// points of the generated chains. Each enabled rule gets a named counter in
// its own table (inet goblin for input/forward, ip goblin_nat for the NAT
// hooks) and a comment naming it, which CheckCustomRules uses to blame nft
// errors on the right entry.

// customComment marks a custom rule's line in the rendered ruleset.
func customComment(name string) string { return "custom rule " + name }

// CustomCounterNames maps each enabled custom rule to its counter name.
func CustomCounterNames(cfg *config.Config) map[string]string {
	n := newNamer()
	out := map[string]string{}
	for _, c := range cfg.CustomRules {
		if c.Enabled {
			out[c.Name] = n.get("custom_", c.Name)
		}
	}
	return out
}

// customRules renders the custom rules into r.custom by hook and position.
func (r *ruleset) customRules() {
	r.custom = map[string][]string{}
	counters := CustomCounterNames(r.cfg)
	for _, c := range r.cfg.CustomRules {
		pos := c.Position
		if pos == "" {
			pos = "end"
		}
		key := c.Hook + "/" + pos
		if !c.Enabled {
			r.custom[key] = append(r.custom[key], fmt.Sprintf("# %s is disabled", quote(customComment(c.Name))))
			continue
		}
		counter := counters[c.Name]
		decl := counterDecl{counter, customComment(c.Name)}
		if c.Hook == "prerouting" || c.Hook == "postrouting" {
			r.natCounters = append(r.natCounters, decl)
		} else {
			r.counters = append(r.counters, decl)
		}
		r.custom[key] = append(r.custom[key], fmt.Sprintf("%s comment %s",
			withCounter(strings.TrimSpace(c.Rule), counter), quote(customComment(c.Name))))
	}
}

// customHooks lists the hooks with enabled custom rules that a connection
// in chain ("input" or "forward") passes.
func (r *ruleset) customHooks(chain string) []string {
	path := []string{"prerouting", chain}
	if chain == "forward" {
		path = append(path, "postrouting")
	}
	var out []string
	for _, h := range path {
		for _, l := range append(r.customAt(h, "start"), r.customAt(h, "end")...) {
			if !strings.HasPrefix(l, "#") {
				out = append(out, h)
				break
			}
		}
	}
	return out
}

// customAt returns the rendered custom rules of one hook and position.
func (r *ruleset) customAt(hook, pos string) []string { return r.custom[hook+"/"+pos] }

// terminal statements: the counter goes right before the first one, so it
// counts the packets the rule's matches select.
var terminalWords = map[string]bool{
	"accept": true, "drop": true, "reject": true, "queue": true, "return": true, "continue": true,
	"dnat": true, "snat": true, "masquerade": true, "redirect": true,
}

// withCounter inserts `counter name <counter>` before the rule's first
// terminal statement, or appends it when there is none. Words inside quotes
// or braces (sets, verdict maps) and ct status values do not count; a rule
// with a verdict map gets no counter (the map's key expression precedes it).
func withCounter(rule, counter string) string {
	stmt := "counter name " + quote(counter)
	depth, inQuote := 0, false
	prev := ""
	start := -1
	for i := 0; i <= len(rule); i++ {
		end := i == len(rule)
		var ch byte
		if !end {
			ch = rule[i]
		}
		if !end && inQuote {
			if ch == '"' {
				inQuote = false
			}
			continue
		}
		if !end && ch != ' ' && ch != '\t' && ch != '{' && ch != '}' && ch != ',' && ch != '"' {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			w := rule[start:i]
			if depth == 0 {
				switch {
				case strings.EqualFold(w, "vmap"):
					return rule
				case terminalWords[strings.ToLower(w)] && !strings.EqualFold(prev, "status"):
					return rule[:start] + stmt + " " + rule[start:]
				}
			}
			prev, start = w, -1
		}
		switch ch {
		case '"':
			inQuote = true
		case '{':
			depth++
		case '}':
			depth--
		}
	}
	return rule + " " + stmt
}

// RunNFTCheck runs `nft -c -f -` on a ruleset and returns nft's error output.
func RunNFTCheck(ctx context.Context, ruleset []byte) error {
	cmd := exec.CommandContext(ctx, "nft", "-c", "-f", "-")
	cmd.Stdin = bytes.NewReader(ruleset)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return nil
}

var nftErrLine = regexp.MustCompile(`(?m)^[^:\n]*:(\d+):\d+(?:-\d+)?: Error: (.*)$`)

// CheckCustomRules renders cfg and checks the ruleset with check (nil =
// RunNFTCheck) when it has custom rules. An nft error on a custom rule's
// line is reported on that rule (custom_rules[i].rule); others on
// custom_rules. Without custom rules nothing runs (the applier checks the
// generated ruleset anyway).
func CheckCustomRules(ctx context.Context, cfg *config.Config, check func(context.Context, []byte) error) []config.ValidationError {
	enabled := false
	for _, c := range cfg.CustomRules {
		enabled = enabled || c.Enabled
	}
	if !enabled {
		return nil
	}
	if check == nil {
		check = RunNFTCheck
	}
	text, err := build(cfg)
	if err != nil {
		return []config.ValidationError{{Path: "custom_rules", Message: err.Error()}}
	}
	err = check(ctx, text)
	if err == nil {
		return nil
	}
	msg := err.Error()
	path := "custom_rules"
	if m := nftErrLine.FindStringSubmatch(msg); m != nil {
		line, _ := strconv.Atoi(m[1])
		if i := customRuleOnLine(cfg, text, line); i >= 0 {
			path = fmt.Sprintf("custom_rules[%d].rule", i)
			msg = fmt.Sprintf("custom rule %q: nft: %s", cfg.CustomRules[i].Name, m[2])
		} else {
			msg = "nft: " + m[2]
		}
	} else {
		msg = "nft: " + msg
	}
	return []config.ValidationError{{Path: path, Message: msg}}
}

// customRuleOnLine returns the index of the custom rule rendered on line
// (1-based) of text, or -1.
func customRuleOnLine(cfg *config.Config, text []byte, line int) int {
	sc := bufio.NewScanner(bytes.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for n := 1; sc.Scan(); n++ {
		if n != line {
			continue
		}
		l := sc.Text()
		for i, c := range cfg.CustomRules {
			if strings.HasSuffix(l, "comment "+quote(customComment(c.Name))) {
				return i
			}
		}
		return -1
	}
	return -1
}
