package nft

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

// customConfig: one rule per hook and position, and a disabled one.
func customConfig() *config.Config {
	cfg := simConfig()
	cfg.CustomRules = []config.CustomRule{
		{Name: "node-exporter", Enabled: true, Hook: "input", Position: "start", Rule: `iifname "eth1" tcp dport 9100 accept`},
		{Name: "log-telnet", Enabled: true, Hook: "forward", Rule: `tcp dport 23 log prefix "telnet drop table " drop`},
		{Name: "no-smb-out", Enabled: true, Hook: "forward", Position: "start", Rule: `oifname "eth0" tcp dport 445 reject with tcp reset`},
		{Name: "redirect-ntp", Enabled: true, Hook: "prerouting", Position: "start", Rule: `iifname "eth1" udp dport 123 redirect to :123`},
		{Name: "count-out", Enabled: true, Hook: "postrouting", Position: "end", Rule: `oifname "eth0"`},
		{Name: "old", Enabled: false, Hook: "input", Rule: `tcp dport 1 accept`},
	}
	return cfg
}

func TestCustomRulesPlacement(t *testing.T) {
	cfg := customConfig()
	if errs := config.Validate(cfg); len(errs) > 0 {
		t.Fatalf("invalid: %v", errs)
	}
	out, err := build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	chain := func(name string) string {
		i := strings.Index(text, "\tchain "+name+" {")
		if i < 0 {
			t.Fatalf("no chain %s", name)
		}
		j := strings.Index(text[i:], "\n\t}")
		return text[i : i+j]
	}
	// order inside each chain: what must come before what
	for _, c := range []struct {
		chain  string
		before []string
	}{
		{"input", []string{"jump temp_blocks", "fib saddr", `tcp dport 9100 counter name "custom_node_exporter" accept comment "custom rule node-exporter"`, "jump threat_feeds_input", `# "custom rule old" is disabled`}},
		{"forward", []string{"ct state invalid drop", `reject with tcp reset comment "custom rule no-smb-out"`, "jump threat_feeds", `counter name "policy_6"`, `log prefix "telnet drop table " counter name "custom_log_telnet" drop comment "custom rule log-telnet"`}},
		{"prerouting", []string{"jump dns_redirect", `udp dport 123 counter name "custom_redirect_ntp" redirect to :123`, "port forward plex"}},
		{"postrouting", []string{"masquerade", `oifname "eth0" counter name "custom_count_out" comment "custom rule count-out"`}},
	} {
		body := chain(c.chain)
		last := -1
		for _, want := range c.before {
			i := strings.Index(body, want)
			if i < 0 || i < last {
				t.Fatalf("chain %s: %q missing or out of order:\n%s", c.chain, want, body)
			}
			last = i
		}
	}
	// NAT hook counters live in the NAT table, the others in inet goblin
	nat := text[strings.Index(text, "table ip goblin_nat {"):]
	if !strings.Contains(nat, "counter custom_redirect_ntp {") || !strings.Contains(nat, "counter custom_count_out {") || strings.Contains(nat, "counter custom_node_exporter {") {
		t.Fatalf("NAT table counters wrong:\n%s", nat)
	}
	if names := CustomCounterNames(cfg); len(names) != 5 || names["old"] != "" || names["node-exporter"] != "custom_node_exporter" {
		t.Fatalf("counter names: %v", names)
	}
}

func TestWithCounter(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{`tcp dport 22 accept`, `tcp dport 22 C accept`},
		{`ct status dnat accept`, `ct status dnat C accept`},
		{`ct status { snat, dnat } drop`, `ct status { snat, dnat } C drop`},
		{`ip saddr 10.0.0.1 log prefix "accept me" drop`, `ip saddr 10.0.0.1 log prefix "accept me" C drop`},
		{`tcp dport 80 dnat to 192.168.1.5:8080`, `tcp dport 80 C dnat to 192.168.1.5:8080`},
		{`oifname "eth0"`, `oifname "eth0" C`},
		{`tcp dport vmap { 22 : accept, 23 : drop }`, `tcp dport vmap { 22 : accept, 23 : drop }`},
	} {
		if got := withCounter(c.in, "x"); got != strings.ReplaceAll(c.want, "C", `counter name "x"`) {
			t.Errorf("%s:\n got %s", c.in, got)
		}
	}
}

func TestCheckCustomRules(t *testing.T) {
	cfg := customConfig()
	if errs := CheckCustomRules(context.Background(), cfg, func(context.Context, []byte) error { return nil }); errs != nil {
		t.Fatalf("clean check: %v", errs)
	}
	// nft blames the line of the log-telnet rule
	text, _ := build(cfg)
	line := slices.IndexFunc(strings.Split(string(text), "\n"), func(l string) bool { return strings.Contains(l, `"custom rule log-telnet"`) }) + 1
	errs := CheckCustomRules(context.Background(), cfg, func(context.Context, []byte) error {
		return fmt.Errorf("/dev/stdin:%d:9-12: Error: syntax error, unexpected junk\n\t\ttcp dport 23 ...\n\t\t^^^^", line)
	})
	if len(errs) != 1 || errs[0].Path != "custom_rules[1].rule" || !strings.Contains(errs[0].Message, `custom rule "log-telnet": nft: syntax error, unexpected junk`) {
		t.Fatalf("errors: %v", errs)
	}
	// an error elsewhere lands on the list
	errs = CheckCustomRules(context.Background(), cfg, func(context.Context, []byte) error { return errors.New("/dev/stdin:3:1-2: Error: odd") })
	if len(errs) != 1 || errs[0].Path != "custom_rules" || errs[0].Message != "nft: odd" {
		t.Fatalf("errors: %v", errs)
	}
	// nothing enabled: no check at all
	for i := range cfg.CustomRules {
		cfg.CustomRules[i].Enabled = false
	}
	called := false
	CheckCustomRules(context.Background(), cfg, func(context.Context, []byte) error { called = true; return nil })
	if called {
		t.Fatal("checked without enabled custom rules")
	}
}

func TestSimulateCustomNote(t *testing.T) {
	cfg := customConfig()
	res := runSim(t, cfg, SimRuntime{}, "192.168.1.10", "93.184.216.34", "tcp", 443, noon)
	if !slices.ContainsFunc(res.Notes, func(n string) bool {
		return strings.Contains(n, "custom nftables rules in prerouting, forward, postrouting")
	}) {
		t.Fatalf("notes: %v", res.Notes)
	}
	res = runSim(t, simConfig(), SimRuntime{}, "192.168.1.10", "93.184.216.34", "tcp", 443, noon)
	if slices.ContainsFunc(res.Notes, func(n string) bool { return strings.Contains(n, "custom") }) {
		t.Fatalf("note without custom rules: %v", res.Notes)
	}
}
