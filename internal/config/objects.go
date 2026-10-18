package config

// Address is a named IP object: a single host, a subnet, an address range or
// an FQDN.
type Address struct {
	Name    string `json:"name"`
	Type    string `json:"type"` // subnet | host | range | fqdn | country ("US,CA")
	Value   string `json:"value"`
	Comment string `json:"comment,omitempty"`
}

// AddressGroup is a named set of address names.
type AddressGroup struct {
	Name    string   `json:"name"`
	Members []string `json:"members,omitempty"` // address names
}

// Service is a named protocol/port object.
type Service struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol"`        // tcp | udp | icmp | any
	Ports    string `json:"ports,omitempty"` // e.g. "80,443,8000-8100"
	Comment  string `json:"comment,omitempty"`
}

// ServiceGroup is a named set of service names.
type ServiceGroup struct {
	Name    string   `json:"name"`
	Members []string `json:"members,omitempty"` // service names
}
