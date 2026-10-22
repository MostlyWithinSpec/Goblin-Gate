package config

import "testing"

func TestKeepRedactedSecrets(t *testing.T) {
	prev := Default()
	prev.DDNS = []DDNS{{Name: "home", Enabled: true, Provider: "duckdns", Hostname: "a.duckdns.org", Token: "duck"}}
	prev.Feeds = []Feed{{Name: "paid", Type: "ip", URL: "https://feeds.example/x", Enabled: true, AuthHeader: "Authorization: Bearer f"}}
	prev.CrowdSec = CrowdSec{Enabled: true, URL: "http://10.0.0.2:8080", APIKey: "cs"}
	prev.LogExport = []LogExport{{Name: "loki", Enabled: true, Type: "loki", URL: "https://loki.example/loki/api/v1/push", Token: "lk"}}

	read := func() *Config { // what a caller without secrets sees
		c := prev.Clone()
		RedactSecrets(c)
		return c
	}

	// written back unchanged, or only switched off: secrets stay
	next := read()
	next.DDNS[0].Enabled = false
	next.CrowdSec.Enabled = false
	KeepRedactedSecrets(prev, next)
	if next.DDNS[0].Token != "duck" || next.DDNS[0].Enabled || next.Feeds[0].AuthHeader == "" || next.CrowdSec.APIKey != "cs" || next.CrowdSec.Enabled || next.LogExport[0].Token != "lk" {
		t.Fatalf("unchanged entries lost secrets: %+v %+v %+v", next.DDNS, next.Feeds, next.CrowdSec)
	}

	// pointed somewhere else: the secret is not carried along
	next = read()
	next.DDNS[0].Hostname = "evil.duckdns.org"
	next.Feeds[0].URL = "https://attacker.example/x"
	next.CrowdSec.URL = "http://attacker.example:8080"
	next.LogExport[0].URL = "https://attacker.example/loki/api/v1/push"
	KeepRedactedSecrets(prev, next)
	if next.DDNS[0].Token != "" || next.Feeds[0].AuthHeader != "" || next.CrowdSec.APIKey != "" || next.LogExport[0].Token != "" {
		t.Fatalf("secret followed a changed destination: %+v %+v %+v %+v", next.DDNS, next.Feeds, next.CrowdSec, next.LogExport)
	}
}
