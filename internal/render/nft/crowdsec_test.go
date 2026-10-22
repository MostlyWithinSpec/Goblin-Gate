package nft

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

func crowdSecConfig(scope string) *config.Config {
	cfg := simConfig()
	cfg.CrowdSec = config.CrowdSec{Enabled: true, URL: "http://192.168.1.20:8080", APIKey: "k", Scope: scope}
	return cfg
}

func TestCrowdSecRender(t *testing.T) {
	out, err := build(simConfig())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "crowdsec") {
		t.Fatal("crowdsec rendered while off")
	}
	cfg := crowdSecConfig("")
	if errs := config.Validate(cfg); len(errs) > 0 {
		t.Fatal(errs)
	}
	out, _ = build(cfg)
	text := string(out)
	for _, want := range []string{
		"set crowdsec_range4 {\n\t\ttype ipv4_addr\n\t\tflags interval, timeout",
		"set crowdsec6 {\n\t\ttype ipv6_addr\n\t\tflags timeout",
		`iifname "eth0" ip saddr @crowdsec4 counter name "crowdsec_drops" drop`,
		`iifname "eth0" ip6 saddr @crowdsec_range6 counter name "crowdsec_drops" drop`,
		"jump crowdsec\n\t\tjump threat_feeds_input",
		"jump crowdsec\n\t\tjump threat_feeds\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(text, "daddr @crowdsec") {
		t.Error("wan scope drops destinations")
	}
	out, _ = build(crowdSecConfig("all"))
	if text = string(out); !strings.Contains(text, "\t\tip daddr @crowdsec4 counter") || strings.Contains(text, `iifname "eth0" ip saddr @crowdsec4`) {
		t.Errorf("scope all:\n%s", text)
	}
}

func TestCrowdSecScript(t *testing.T) {
	s := string(CrowdSecScript([]CrowdSecBan{
		{Prefix: netip.MustParsePrefix("198.51.100.7/24"), Left: time.Hour},
		{Prefix: netip.MustParsePrefix("203.0.113.9/32"), Left: 1500 * time.Millisecond},
	}, []netip.Prefix{netip.MustParsePrefix("2001:db8::1/128")}))
	for _, want := range []string{
		"add element inet goblin crowdsec_range4 { 198.51.100.0/24 }\ndelete element inet goblin crowdsec_range4 { 198.51.100.0/24 }\nadd element inet goblin crowdsec_range4 { 198.51.100.0/24 timeout 3600s }\n",
		"add element inet goblin crowdsec4 { 203.0.113.9 timeout 2s }\n",
		"add element inet goblin crowdsec6 { 2001:db8::1 }\ndelete element inet goblin crowdsec6 { 2001:db8::1 }\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
}
