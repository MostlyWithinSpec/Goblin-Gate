package appctrl

import "testing"

func TestIdentify(t *testing.T) {
	for name, want := range map[string]string{
		"www.netflix.com":           "netflix",
		"ipv4-c001.nflxvideo.net.":  "netflix",
		"r3---sn-x.googlevideo.com": "youtube",
		"teams.microsoft.com":       "teams",
		"www.microsoft.com":         "",
		"example.com":               "",
		"com":                       "",
	} {
		id, _, ok := Identify(name)
		if id != want || ok != (want != "") {
			t.Errorf("%s: %q %v", name, id, ok)
		}
	}
}

func TestCatalog(t *testing.T) {
	seen := map[string]string{}
	for _, a := range Catalog() {
		if a.ID == "" || a.Name == "" || a.Group == "" || len(a.Domains) == 0 {
			t.Errorf("incomplete app %+v", a)
		}
		for _, d := range a.Domains {
			if other, dup := seen[d]; dup {
				t.Errorf("domain %s in %s and %s", d, other, a.ID)
			}
			seen[d] = a.ID
		}
	}
	if !Known("steam") || Known("nope") {
		t.Error("Known")
	}
}
