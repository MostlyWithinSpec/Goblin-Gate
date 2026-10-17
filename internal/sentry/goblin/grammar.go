package goblin

import (
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/sentry"
)

// The language model answers in FunctionGemma's call syntax after the
// prompt ends with CallPrefix: `temporary_block_ip{minutes:60}` or
// `<tool>{}`. The grammar below allows exactly the calls in Calls() - the
// four labels and request_approval ("not sure") - and nothing else: no
// address (the engine sets the target), no free text.
const CallPrefix = "<start_function_call>call:"

// callTools are the tools the language model may name, in prompt order.
var callTools = []struct{ name, desc string }{
	{sentry.ToolBlock, "Block the outside address for a while (the box picks the address)."},
	{sentry.ToolFalsePositive, "Ignore: the alert is harmless or expected here."},
	{sentry.ToolWatch, "Watch the address for 24 hours; later incidents mention it."},
	{sentry.ToolNotify, "Tell the admin, take no action."},
	{sentry.ToolRequestApproval, "Not sure: ask the admin."},
}

// Calls lists every string the grammar allows: the whole output language.
func Calls() []string {
	var out []string
	for _, t := range callTools {
		if t.name == sentry.ToolBlock {
			for _, m := range BlockMinutes {
				out = append(out, t.name+"{minutes:"+strconv.Itoa(m)+"}")
			}
			continue
		}
		out = append(out, t.name+"{}")
	}
	return out
}

// Grammar is the GBNF grammar (llama.cpp) for the model's answer.
func Grammar() string {
	var mins, plain []string
	for _, m := range BlockMinutes {
		mins = append(mins, strconv.Quote(strconv.Itoa(m)))
	}
	for _, t := range callTools {
		if t.name != sentry.ToolBlock {
			plain = append(plain, strconv.Quote(t.name))
		}
	}
	return "root ::= block | plain\n" +
		"block ::= \"" + sentry.ToolBlock + "{minutes:\" (" + strings.Join(mins, " | ") + ") \"}\"\n" +
		"plain ::= (" + strings.Join(plain, " | ") + ") \"{}\"\n"
}

// ErrInvalidCall is returned for model output that is not an allowed call.
var ErrInvalidCall = errors.New("not an allowed tool call")

// ParseCall reads the model's answer (what follows CallPrefix, optionally
// ending in <end_function_call>). Only strings in Calls() are accepted.
func ParseCall(s string) (tool string, minutes int, err error) {
	s = strings.TrimSuffix(strings.TrimSpace(s), "<end_function_call>")
	if !slices.Contains(Calls(), s) {
		return "", 0, ErrInvalidCall
	}
	name, args, _ := strings.Cut(s, "{")
	if v, ok := strings.CutPrefix(strings.TrimSuffix(args, "}"), "minutes:"); ok {
		minutes, _ = strconv.Atoi(v)
	}
	return name, minutes, nil
}

// declarations renders the tools in FunctionGemma's declaration syntax.
func declarations() string {
	var b strings.Builder
	for _, t := range callTools {
		params := "{type:<escape>OBJECT<escape>}"
		if t.name == sentry.ToolBlock {
			var mins []string
			for _, m := range BlockMinutes {
				mins = append(mins, strconv.Itoa(m))
			}
			params = "{properties:{minutes:{description:<escape>block length in minutes: " + strings.Join(mins, ", ") +
				"<escape>,type:<escape>INTEGER<escape>}},required:[<escape>minutes<escape>],type:<escape>OBJECT<escape>}"
		}
		b.WriteString("<start_function_declaration>declaration:" + t.name + "{description:<escape>" + t.desc +
			"<escape>,parameters:" + params + "}<end_function_declaration>")
	}
	return b.String()
}
