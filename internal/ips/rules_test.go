package ips

import (
	"os"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

func classes(t *testing.T) map[string]int {
	f, err := os.Open("testdata/classification.config")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	c, err := ParseClassifications(f)
	if err != nil || c["trojan-activity"] != 1 || c["not-suspicious"] != 3 {
		t.Fatalf("classes %v %v", c, err)
	}
	return c
}

// actions maps sid -> action in the rendered rules.
func render(t *testing.T, p config.IPSProfile) (map[string]string, Stats) {
	t.Helper()
	src, err := os.Open("testdata/sample.rules")
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	var b strings.Builder
	st, err := ProfileRules(&b, src, p, classes(t))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, l := range strings.Split(b.String(), "\n") {
		if l == "" || l[0] == '#' {
			continue
		}
		i := strings.Index(l, "sid:")
		sid := strings.TrimSuffix(strings.Fields(l[i+4:])[0], ";")
		out[sid] = strings.Fields(l)[0]
	}
	return out, st
}

func TestPreventDefaults(t *testing.T) {
	got, st := render(t, config.IPSProfile{Name: "home"})
	want := map[string]string{
		"2000001": "drop",  // trojan-activity, priority 1
		"2001219": "drop",  // attempted-recon, priority 2 (default block severity 2)
		"2060000": "alert", // priority 3
		"2060001": "drop",  // priority keyword overrides the classtype
		"2070000": "alert", // noalert flowbit setter must never drop
		"2400000": "alert", // unknown classtype -> severity 3
		"9000001": "pass",
	}
	for sid, a := range want {
		if got[sid] != a {
			t.Errorf("sid %s: %q, want %q", sid, got[sid], a)
		}
	}
	for _, sid := range []string{"2013028", "2050000", "2210054", "2080000"} { // INFO, POLICY, SURICATA, commented
		if _, ok := got[sid]; ok {
			t.Errorf("sid %s should be excluded by default", sid)
		}
	}
	if st.Drop != 3 || st.Alert != 3 {
		t.Errorf("stats %+v", st)
	}
}

func TestDetectAndCategories(t *testing.T) {
	got, _ := render(t, config.IPSProfile{Name: "watch", Mode: "detect"})
	for sid, a := range got {
		if a == "drop" {
			t.Errorf("detect mode dropped sid %s", sid)
		}
	}
	got, _ = render(t, config.IPSProfile{Name: "strict", BlockSeverity: 3, Categories: []string{"scan", "SURICATA", "POLICY"}})
	if len(got) != 3 || got["2001219"] != "drop" || got["2050000"] != "drop" || got["2210054"] != "alert" {
		t.Errorf("categories: %v", got)
	}
	got, _ = render(t, config.IPSProfile{Name: "highonly", BlockSeverity: 1})
	if got["2001219"] != "alert" || got["2000001"] != "drop" {
		t.Errorf("block severity 1: %v", got)
	}
}

func TestCategory(t *testing.T) {
	for msg, want := range map[string]string{"ET MALWARE x": "MALWARE", "ETPRO TROJAN y": "TROJAN", "SURICATA STREAM z": "SURICATA", "local rule": "", "": ""} {
		if got := Category(msg); got != want {
			t.Errorf("%q: %q", msg, got)
		}
	}
}
