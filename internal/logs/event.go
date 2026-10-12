package logs

// Event represents a log entry from a security system. Time is Unix seconds.
// The JSON names are the API field names and the events table columns.
type Event struct {
	// Time of the event
	Time int64 `json:"time"`

	// Kind of event: traffic, ips, dns, webfilter, system
	Kind string `json:"kind"`

	// Action taken: allow, deny, alert, drop
	Action string `json:"action"`

	// Policy ID that triggered the event
	PolicyID string `json:"policy_id,omitempty"`

	// Source IP address
	SrcIP string `json:"src_ip,omitempty"`

	// Source port
	SrcPort int `json:"src_port,omitempty"`

	// Destination IP address
	DstIP string `json:"dst_ip,omitempty"`

	// Destination port
	DstPort int `json:"dst_port,omitempty"`

	// Protocol (tcp, udp, icmp)
	Proto string `json:"proto,omitempty"`

	// Input interface
	InIntf string `json:"in_intf,omitempty"`

	// Output interface
	OutIntf string `json:"out_intf,omitempty"`

	// Bytes transferred
	Bytes int `json:"bytes,omitempty"`

	// Application
	App string `json:"app,omitempty"`

	// Category
	Category string `json:"category,omitempty"`

	// Signature ID
	Signature string `json:"signature,omitempty"`

	// Severity
	Severity string `json:"severity,omitempty"`

	// Message
	Message string `json:"message,omitempty"`
}
