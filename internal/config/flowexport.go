package config

// FlowExport sends the box's connections to a flow collector (ntopng,
// Elastic, GoFlow2, nfdump) as NetFlow v9 or IPFIX over UDP (task 144).
// The records come from the conntrack sampler, so they are sampled, not
// every packet: a connection shorter than one sample interval may be missed.
type FlowExport struct {
	Enabled bool `json:"enabled"`
	// Collector is the UDP collector's "host:port", e.g. "10.0.0.5:2055".
	// A DNS name is resolved when the exporter starts and every 5 minutes.
	Collector string `json:"collector,omitempty"`
	// Protocol: "ipfix" (or "") or "netflow9".
	Protocol string `json:"protocol,omitempty"`
	// ActiveTimeoutSeconds: a connection that stays alive longer than this
	// gets a record every timeout with the counters since the last one.
	// 0 = 60; 10-600.
	ActiveTimeoutSeconds int `json:"active_timeout_seconds,omitempty"`
}

// ActiveTimeout returns the active timeout in seconds (0 = 60).
func (f FlowExport) ActiveTimeout() int {
	if f.ActiveTimeoutSeconds == 0 {
		return 60
	}
	return f.ActiveTimeoutSeconds
}

func (v *validator) checkFlowExport() {
	f := v.cfg.FlowExport
	if !oneOf(f.Protocol, "", "ipfix", "netflow9") {
		v.add("flow_export.protocol", "unknown protocol %q: want ipfix or netflow9", f.Protocol)
	}
	if f.Enabled && f.Collector == "" {
		v.add("flow_export.collector", "required when flow export is enabled")
	}
	if f.Collector != "" && !validHostPort(f.Collector) {
		v.add("flow_export.collector", "invalid collector %q: want host:port, e.g. 10.0.0.5:2055", f.Collector)
	}
	if f.ActiveTimeoutSeconds != 0 && (f.ActiveTimeoutSeconds < 10 || f.ActiveTimeoutSeconds > 600) {
		v.add("flow_export.active_timeout_seconds", "want 10-600 seconds")
	}
}
