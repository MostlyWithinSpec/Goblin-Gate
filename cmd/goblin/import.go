package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/importer/pfsense"
)

// runImportPfSense is `goblin import-pfsense FILE [--devices wan=eth0,opt1=eth2]`:
// uploads a pfSense config.xml (POST /api/v1/import/pfsense), which becomes
// the candidate config, and prints the report. Nothing is committed.
func runImportPfSense(server, token string, args []string, out io.Writer) int {
	var file, devices string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--devices" && i+1 < len(args):
			devices = args[i+1]
			i++
		case strings.HasPrefix(args[i], "-"):
			fmt.Fprintf(os.Stderr, "goblin import-pfsense: unknown flag %q\n", args[i])
			return 2
		case file == "":
			file = args[i]
		default:
			fmt.Fprintln(os.Stderr, "goblin import-pfsense: one file only")
			return 2
		}
	}
	if file == "" {
		fmt.Fprintln(os.Stderr, "usage: goblin import-pfsense FILE [--devices wan=eth0,lan=eth1,opt1=eth2]")
		return 2
	}
	data, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "goblin: %v\n", err)
		return 1
	}
	path := "/api/v1/import/pfsense"
	if devices != "" {
		m := map[string]string{}
		for _, kv := range strings.Split(devices, ",") {
			k, v, ok := strings.Cut(strings.TrimSpace(kv), "=")
			if !ok || k == "" || v == "" {
				fmt.Fprintf(os.Stderr, "goblin import-pfsense: --devices: want pfsense=device pairs, got %q\n", kv)
				return 2
			}
			m[k] = v
		}
		b, _ := json.Marshal(m)
		path += "?devices=" + url.QueryEscape(string(b))
	}
	c := newGoblinClient(server, token)
	c.client.Timeout = 60 * time.Second
	req, err := http.NewRequest(http.MethodPost, c.server+path, bytes.NewReader(data))
	if err != nil {
		fmt.Fprintf(os.Stderr, "goblin: %v\n", err)
		return 1
	}
	req.Header.Set("Content-Type", "application/xml")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "goblin: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "goblin: import failed (%d): %s\n", resp.StatusCode, strings.TrimSpace(string(body)))
		return 1
	}
	var res pfsense.Result
	if err := json.Unmarshal(body, &res); err != nil {
		fmt.Fprintf(os.Stderr, "goblin: bad answer: %v\n", err)
		return 1
	}
	printImport(out, &res)
	return 0
}

func printImport(out io.Writer, res *pfsense.Result) {
	marks := map[string]string{pfsense.Converted: "ok  ", pfsense.Approximated: "~   ", pfsense.Skipped: "skip"}
	section := ""
	for _, e := range res.Report {
		if e.Section != section {
			section = e.Section
			fmt.Fprintf(out, "\n%s\n", strings.ToUpper(section))
		}
		risk := ""
		if e.Risk {
			risk = "  <-- check this"
		}
		fmt.Fprintf(out, "  %s %s: %s%s\n", marks[e.Status], e.Item, e.Detail, risk)
	}
	fmt.Fprintln(out, "\nINTERFACES")
	for _, m := range res.Interfaces {
		dev := m.Device
		if dev == "" {
			dev = "NOT ASSIGNED (use --devices " + m.PfSense + "=<device>)"
		}
		fmt.Fprintf(out, "  %-6s %-10s -> %-14s %s\n", m.PfSense, m.PfSenseDevice, m.Name, dev)
	}
	if len(res.Errors) > 0 {
		fmt.Fprintln(out, "\nTO FIX BEFORE COMMITTING")
		for _, e := range res.Errors {
			fmt.Fprintf(out, "  %s: %s\n", e.Path, e.Message)
		}
	}
	fmt.Fprintf(out, "\n%d converted, %d approximated, %d skipped. The result is the candidate config: review it (goblin -c diff, or the web UI) and commit.\n",
		res.Summary[pfsense.Converted], res.Summary[pfsense.Approximated], res.Summary[pfsense.Skipped])
}
