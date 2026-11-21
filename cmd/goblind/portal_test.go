package main

import "testing"

func TestParsePortalSeen(t *testing.T) {
	out := `{"nftables": [{"metainfo": {"version": "1.0.9"}}, {"set": {"family": "inet", "name": "portal_seen", "table": "goblin",
	"type": ["ether_addr", "ipv4_addr"], "flags": ["timeout", "dynamic"],
	"elem": [{"elem": {"val": {"concat": ["aa:bb:cc:00:00:01", "192.168.30.100"]}, "timeout": 1800, "expires": 1700}},
	         {"concat": ["aa:bb:cc:00:00:02", "192.168.30.101"]}]}}]}`
	seen, err := parsePortalSeen([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if !seen["aa:bb:cc:00:00:01 192.168.30.100"] || !seen["aa:bb:cc:00:00:02 192.168.30.101"] || len(seen) != 2 {
		t.Errorf("seen %v", seen)
	}
}
