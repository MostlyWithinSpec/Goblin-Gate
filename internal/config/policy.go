package config

// Policy is a firewall rule. The src/dst/service fields are lists of object
// names; "all" (addresses) and "any" (services) match everything.
type Policy struct {
	ID               uint32   `json:"id"`
	Name             string   `json:"name"`
	Enabled          bool     `json:"enabled"`
	SrcIntf          []string `json:"src_intf,omitempty"`
	DstIntf          []string `json:"dst_intf,omitempty"`
	SrcAddr          []string `json:"src_addr,omitempty"` // address names or "all"
	DstAddr          []string `json:"dst_addr,omitempty"` // address names or "all"
	Service          []string `json:"service,omitempty"`  // service names or "any"
	Action           string   `json:"action"`             // accept | deny
	NAT              bool     `json:"nat,omitempty"`
	Log              string   `json:"log,omitempty"` // off | all | deny
	Schedule         string   `json:"schedule,omitempty"`
	IPSProfile       string   `json:"ips_profile,omitempty"`
	WebFilterProfile string   `json:"webfilter_profile,omitempty"`
	DNSFilterProfile string   `json:"dnsfilter_profile,omitempty"`
	AppCtrlProfile   string   `json:"appctrl_profile,omitempty"`
	ThreatFeeds      []string `json:"threat_feeds,omitempty"` // feed names
}

// NAT holds the outbound/inbound translation rules.
type NAT struct {
	PortForwards []PortForward `json:"port_forwards,omitempty"`
}

// PortForward maps an external port on in_intf to an internal address/port.
type PortForward struct {
	Name     string `json:"name"`
	InIntf   string `json:"in_intf"`
	ExtPort  string `json:"ext_port"` // "443" or a range like "8000-8100"
	Protocol string `json:"protocol"` // tcp | udp
	ToAddr   string `json:"to_addr"`
	ToPort   string `json:"to_port"`
}
