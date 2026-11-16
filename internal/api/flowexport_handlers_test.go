package api

import (
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/flowexport"
)

func TestFlowExportAPI(t *testing.T) {
	if out := newTestAPI(t).expect("GET", "/api/v1/flow-export", nil, 503); errCode(out) != "flow_export_unavailable" {
		t.Fatalf("no exporter: %v", out)
	}

	exp := &flowexport.Exporter{}
	exp.Configure(config.FlowExport{
		Enabled:              true,
		Collector:            "10.0.0.5:2055",
		Protocol:             flowexport.ProtocolNetFlow9,
		ActiveTimeoutSeconds: 30,
	})
	a := newTestAPIWith(t, func(s *Server) { s.FlowExport = exp })

	out := a.expect("GET", "/api/v1/flow-export", nil, 200)
	if out["enabled"] != true || out["collector"] != "10.0.0.5:2055" || out["protocol"] != "netflow9" {
		t.Fatalf("status: %v", out)
	}
	if out["running"] != true || out["packets_sent"].(float64) != 0 || out["records_sent"].(float64) != 0 {
		t.Fatalf("counters: %v", out)
	}
	if _, ok := out["last_sent"]; ok {
		t.Errorf("nothing was sent yet, last_sent should be absent: %v", out)
	}
	if out["open_flows"].(float64) != 0 {
		t.Errorf("open_flows: %v", out)
	}

	// a commit that turns the section off shows up without a restart
	exp.Configure(config.FlowExport{Enabled: false, Collector: "10.0.0.5:2055"})
	out = a.expect("GET", "/api/v1/flow-export", nil, 200)
	if out["enabled"] != false || out["running"] != false {
		t.Fatalf("disabled: %v", out)
	}
}
