package main

import "testing"

func TestParseLogSources(t *testing.T) {
	src, err := parseLogSources(defaultLogSources())
	if err != nil || len(src) != 2 || src[0].Name != "nftables" || src[1].Path != "/var/log/suricata/eve.json" {
		t.Fatalf("default: %v %+v", err, src)
	}
	if src, err := parseLogSources(""); err != nil || len(src) != 0 {
		t.Fatalf("empty: %v %+v", err, src)
	}
	for _, bad := range []string{"syslog=/var/log/syslog", "nftables", "suricata="} {
		if _, err := parseLogSources(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
