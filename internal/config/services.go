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
	// LocalDomain is the LAN's own domain (task 055): DHCP hands it out as
	// the search domain, static leases and DHCP clients get names in it,
	// and Unbound answers it itself. Default: system.domain, else home.arpa.
	LocalDomain string `json:"local_domain,omitempty"`
	// NoDHCPNames stops registering dynamic DHCP clients' hostnames.
	NoDHCPNames bool `json:"no_dhcp_names,omitempty"`
	// DomainForwards send chosen domains to chosen servers (split DNS).
	DomainForwards []DomainForward `json:"domain_forwards,omitempty"`
	// AllowPrivateAnswers turns off DNS rebinding protection: when true,
	// public names may answer with private addresses (no Unbound
	// private-address/private-domain lines are rendered).
	AllowPrivateAnswers bool `json:"allow_private_answers,omitempty"`
}

// DomainForward sends queries for Domain (and its subdomains) to Servers.
type DomainForward struct {
	Domain  string       `json:"domain"`
	Servers []netip.Addr `json:"servers"`
}

// EffectiveLocalDomain is dns.local_domain, else system.domain, else
// home.arpa (RFC 8375).
func (c *Config) EffectiveLocalDomain() string {
	switch {
	case c.DNS.LocalDomain != "":
		return c.DNS.LocalDomain
	case c.System.Domain != "":
		return c.System.Domain
	}
	return "home.arpa"
}
