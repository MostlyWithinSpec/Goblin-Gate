package nft

import (
	"strings"
	"testing"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/render/rendertest"
)

func TestPortalRuleset(t *testing.T) {
	files, err := Renderer{}.Render(rendertest.Portal())
	if err != nil {
		t.Fatal(err)
	}
	var rs string
	for _, b := range files {
		rs += string(b)
	}
	for _, want := range []string{
		"set portal_ok {\n\t\ttype ether_addr . ipv4_addr\n\t\tflags timeout",
		"set portal_mac {",
		"set portal_seen {",
		`iifname "eth1.30" udp dport 67 accept comment "portal guest: dhcp"`,
		`iifname "eth1.30" ip daddr 192.168.30.1 meta l4proto { tcp, udp } th dport 53 accept`,
		`iifname "eth1.30" ip daddr 192.168.30.1 tcp dport 8880 accept`,
		`iifname "eth1.30" ether saddr . ip saddr != @portal_ok drop`,
		`iifname "eth1.30" ether saddr . ip saddr != @portal_ok tcp dport 443 reject with tcp reset`,
		`iifname "eth1.30" ether saddr . ip saddr @portal_ok update @portal_seen { ether saddr . ip saddr timeout 30m }`,
		`iifname "eth1.30" update @portal_up_0 { ip saddr limit rate over 1250 kbytes/second } drop`,
		`oifname "eth1.30" update @portal_down_0 { ip daddr limit rate over 1250 kbytes/second } drop`,
		"chain portal_redirect {\n\t\ttype nat hook prerouting priority dstnat; policy accept;",
		`iifname "eth1.30" tcp dport 80 ether saddr . ip saddr != @portal_ok dnat ip to 192.168.30.1:8880`,
	} {
		if !strings.Contains(rs, want) {
			t.Errorf("missing %q", want)
		}
	}
	// the disabled LAN portal adds nothing
	if strings.Contains(rs, `portal lan`) {
		t.Error("rules for a disabled portal")
	}
	// the guard comes before the established accept, so a removed guest is cut off at once
	fwd := rs[strings.Index(rs, "chain forward {"):]
	if strings.Index(fwd, "@portal_ok reject") > strings.Index(fwd, "ct state established,related accept") {
		t.Error("portal guard after the established accept")
	}
}

func TestPortalWithoutPortals(t *testing.T) {
	cfg := rendertest.Portal()
	cfg.CaptivePortals = nil
	files, err := Renderer{}.Render(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range files {
		if strings.Contains(string(b), "portal") {
			t.Fatal("portal rules without a portal")
		}
	}
}

func TestPortalScripts(t *testing.T) {
	got := string(PortalScript([]PortalClient{{MAC: "aa:bb:cc:dd:ee:ff", IP: "192.168.30.100", Timeout: 90 * time.Second}, {MAC: "x", IP: "y"}}))
	want := "add element inet goblin portal_ok { aa:bb:cc:dd:ee:ff . 192.168.30.100 timeout 90s }\n" +
		"add element inet goblin portal_mac { aa:bb:cc:dd:ee:ff timeout 90s }\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	rm := PortalRemoveScript("aa:bb:cc:dd:ee:ff", "192.168.30.100")
	if len(rm) != 3 || !strings.HasPrefix(rm[0], "delete element inet goblin portal_ok") {
		t.Errorf("remove: %v", rm)
	}
}
