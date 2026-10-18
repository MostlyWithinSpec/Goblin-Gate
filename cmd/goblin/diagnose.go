package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// simResult mirrors the answer of POST /api/v1/policy/simulate.
type simResult struct {
	Verdict string `json:"verdict"`
	Reason  string `json:"reason"`
	Chain   string `json:"chain"`
	SrcIntf string `json:"src_intf"`
	DstIntf string `json:"dst_intf"`
	Policy  *struct {
		ID   int
		Name string
	} `json:"policy"`
	NAT        bool              `json:"nat"`
	Gateway    string            `json:"gateway"`
	Profiles   map[string]string `json:"profiles"`
	Translated string            `json:"translated"`
	Steps      []struct {
		ID      int    `json:"id"`
		Name    string `json:"name"`
		Matched bool   `json:"matched"`
		Reason  string `json:"reason"`
	} `json:"steps"`
	Notes []string `json:"notes"`
}

// handleDiagnosePolicy: diagnose policy <src> <dst> [tcp|udp|icmp|any] [port]
// asks the policy simulator whether the connection would pass and why.
func (s *interactiveShell) handleDiagnosePolicy(line string) error {
	f := strings.Fields(line)[2:] // after "diagnose policy"
	if len(f) < 2 || len(f) > 4 {
		return fmt.Errorf("usage: diagnose policy <src-ip> <dst-ip> [tcp|udp|icmp|any] [port]")
	}
	body := map[string]any{"src_ip": f[0], "dst_ip": f[1], "protocol": "any"}
	if len(f) >= 3 {
		body["protocol"] = strings.ToLower(f[2])
	}
	if len(f) == 4 {
		port, err := strconv.Atoi(f[3])
		if err != nil {
			return fmt.Errorf("port %q is not a number", f[3])
		}
		body["dst_port"] = port
	}
	resp, err := s.client.doRequest(http.MethodPost, "/api/v1/policy/simulate", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error struct{ Message string } `json:"error"`
		}
		json.Unmarshal(data, &e)
		return fmt.Errorf("simulate: %s", firstNonEmpty(e.Error.Message, resp.Status))
	}
	var res simResult
	if err := json.Unmarshal(data, &res); err != nil {
		return fmt.Errorf("simulate: %w", err)
	}
	printSimulation(os.Stdout, &res)
	return nil
}

func printSimulation(w io.Writer, r *simResult) {
	fmt.Fprintf(w, "%s: %s\n", strings.ToUpper(r.Verdict), r.Reason)
	path := r.SrcIntf
	if r.DstIntf != "" {
		path += " -> " + r.DstIntf
	}
	fmt.Fprintf(w, "  %s chain, %s\n", r.Chain, path)
	if r.Translated != "" {
		fmt.Fprintf(w, "  %s\n", r.Translated)
	}
	if r.NAT {
		fmt.Fprintln(w, "  masqueraded behind the WAN address")
	}
	if r.Gateway != "" {
		fmt.Fprintf(w, "  via gateway %s\n", r.Gateway)
	}
	for k, v := range r.Profiles {
		fmt.Fprintf(w, "  %s profile %s\n", k, v)
	}
	for _, st := range r.Steps {
		mark := "x"
		if st.Matched {
			mark = "+"
		}
		fmt.Fprintf(w, "  [%s] policy %d %s: %s\n", mark, st.ID, st.Name, st.Reason)
	}
	for _, n := range r.Notes {
		fmt.Fprintf(w, "  note: %s\n", n)
	}
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}
