package config

import (
	"strings"
	"testing"
)

func TestCheckCustomRuleText(t *testing.T) {
	for _, c := range []struct{ rule, want string }{
		{`iifname "eth3" tcp dport 9100 accept`, ""},
		{`tcp dport { 80, 443 } log prefix "drop table chain " drop`, ""}, // refused words inside quotes are fine
		{`oifname "eth0" tcp dport 445 reject with tcp reset`, ""},
		{`ct status dnat accept`, ""},
		{"", "empty"},
		{"accept\ntcp dport 1 drop", "line breaks"},
		{`accept; flush ruleset`, "no ';'"},
		{`accept # comment`, "no ';' or '#'"},
		{`accept } } table inet x { chain y { type filter hook input priority 0; } }`, "no ';'"},
		{`accept } }`, "unbalanced braces"},
		{`tcp dport { 80 accept`, "unbalanced braces"},
		{`} tcp dport 80 {`, "unbalanced braces"},
		{`log prefix "x drop`, "unbalanced quotes"},
		{`jump policies`, `"jump" is not allowed`},
		{`ip saddr 1.2.3.4 GOTO x`, `"goto" is not allowed`},
		{`tcp dport 80 accept comment "x" include "/etc/passwd"`, `"include" is not allowed`},
		{strings.Repeat("a", MaxCustomRuleLen+1), "longer than"},
		{`tcp dport 80 accept comment 'x' include '/etc/passwd'`, "double quotes"},
		{`log prefix "it's fine in double quotes" accept`, ""},
	} {
		got := CheckCustomRuleText(c.rule)
		if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
			t.Errorf("%q: got %q, want %q", c.rule, got, c.want)
		}
	}
}

func TestValidateCustomRules(t *testing.T) {
	cfg := Default()
	cfg.CustomRules = []CustomRule{
		{Name: "ok", Enabled: true, Hook: "input", Rule: "tcp dport 9100 accept"},
		{Name: "ok", Enabled: true, Hook: "output", Position: "middle", Rule: "accept", Comment: `say "hi"`},
	}
	var msgs []string
	for _, e := range Validate(cfg) {
		msgs = append(msgs, e.Path+": "+e.Message)
	}
	all := strings.Join(msgs, "\n")
	for _, want := range []string{
		"custom_rules[1].name: duplicate custom rule name",
		`custom_rules[1].hook: custom rule "ok": unknown hook "output"`,
		`custom_rules[1].position: custom rule "ok": unknown position "middle"`,
		"custom_rules[1].comment",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	if strings.Contains("\n"+all, "\ncustom_rules[0]") {
		t.Errorf("valid rule refused:\n%s", all)
	}
}
