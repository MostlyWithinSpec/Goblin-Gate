package config

import "net/netip"

// DHCPServer is a Kea DHCPv4 server on one interface.
type DHCPServer struct {
	Interface    string            `json:"interface"`
	RangeStart   netip.Addr        `json:"range_start"`
	RangeEnd     netip.Addr        `json:"range_end"`
	LeaseSeconds int               `json:"lease_seconds,omitempty"`
	DNS          []netip.Addr      `json:"dns,omitempty"` // handed to clients
	Gateway      *netip.Addr       `json:"gateway,omitempty"`
	StaticLeases []DHCPStaticLease `json:"static_leases,omitempty"`
}

// DHCPStaticLease pins a MAC address to an IP.
type DHCPStaticLease struct {
	MAC      string     `json:"mac"`
	IP       netip.Addr `json:"ip"`
	Hostname string     `json:"hostname,omitempty"`
}

// DNS is the Unbound resolver configuration.
type DNS struct {
	Forwarders   []netip.Addr      `json:"forwarders,omitempty"`
	DNSSEC       bool              `json:"dnssec,omitempty"`
	LocalRecords map[string]string `json:"local_records,omitempty"` // name -> ip
}
