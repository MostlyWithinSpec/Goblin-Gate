package pfsense

import (
	"os"
	"testing"
)

// TestLocalFile imports a real pfSense backup given in GOBLIN_PFSENSE_FILE
// and prints the report (never commit such a file or its output).
func TestLocalFile(t *testing.T) {
	path := os.Getenv("GOBLIN_PFSENSE_FILE")
	if path == "" {
		t.Skip("set GOBLIN_PFSENSE_FILE to a config.xml")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Import(data, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range res.Report {
		risk := ""
		if e.Risk {
			risk = " [RISK]"
		}
		t.Logf("%-12s %-13s %s: %s%s", e.Section, e.Status, e.Item, e.Detail, risk)
	}
	for _, m := range res.Interfaces {
		t.Logf("interface %+v", m)
	}
	for _, e := range res.Errors {
		t.Logf("validation: %s", e)
	}
	t.Logf("summary %v, %d policies, %d forwards, %d addresses", res.Summary, len(res.Config.Policies), len(res.Config.NAT.PortForwards), len(res.Config.Addresses))
}
