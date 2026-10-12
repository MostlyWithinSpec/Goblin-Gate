package config

import "net/netip"

// SchemaVersion is the schema version written by this build. Load does not
// enforce it yet; version gating arrives with validation (task 003).
const SchemaVersion = 1

// Config is the root of the Goblin Gate configuration tree. GUI and CLI both
// edit a candidate copy through the API; commit validates, renders and
// applies it. JSON keys are snake_case; optional fields use omitempty.
type Config struct {
	SchemaVersion int            `json:"schema_version"`
	System        System         `json:"system"`
	Interfaces    []Interface    `json:"interfaces,omitempty"`
	VLANs         []VLAN         `json:"vlans,omitempty"`
	Addresses     []Address      `json:"addresses,omitempty"`
	AddressGroups []AddressGroup `json:"address_groups,omitempty"`
	Services      []Service      `json:"services,omitempty"`
	ServiceGroups []ServiceGroup `json:"service_groups,omitempty"`
	Policies      []Policy       `json:"policies,omitempty"`
	NAT           NAT            `json:"nat"`
	DHCP          []DHCPServer   `json:"dhcp,omitempty"`
	DNS           DNS            `json:"dns"`
	Profiles      Profiles       `json:"profiles"`
	Feeds         []Feed         `json:"feeds,omitempty"`
	Admins        []Admin        `json:"admins"`
	VPN           VPN            `json:"vpn,omitzero"`

	// AllowNoAdmin is a runtime flag, not part of the JSON document. The
	// first-boot wizard sets it while no admin exists yet so Validate does not
	// complain about a missing admin (task 022).
	AllowNoAdmin bool `json:"-"`
}

// System holds the box-level settings.
type System struct {
	Hostname   string       `json:"hostname"`
	Domain     string       `json:"domain,omitempty"`
	Timezone   string       `json:"timezone"`
	AdminPort  int          `json:"admin_port"` // management plane port, default 443
	NTPServers []string     `json:"ntp_servers,omitempty"`
	DNSServers []netip.Addr `json:"dns_servers,omitempty"` // upstream DNS used by the box itself
}

// Interface is a physical network interface. Address and Gateway are pointers
// so that dhcp/none interfaces can leave them unset (omitempty).
type Interface struct {
	Name        string        `json:"name"`              // wan1, lan, ...
	Device      string        `json:"device"`            // eth0
	Role        string        `json:"role"`              // wan | lan | dmz | guest | mgmt
	Mode        string        `json:"mode"`              // static | dhcp | none
	Address     *netip.Prefix `json:"address,omitempty"` // CIDR, e.g. 192.168.1.1/24
	Gateway     *netip.Addr   `json:"gateway,omitempty"`
	MTU         int           `json:"mtu,omitempty"`
	Enabled     bool          `json:"enabled"`
	Description string        `json:"description,omitempty"`
	AllowAccess []string      `json:"allow_access,omitempty"` // ping | https | ssh
}

// VLAN is an 802.1Q sub-interface on a physical interface, carrying the same
// addressing fields as Interface.
type VLAN struct {
	Name        string        `json:"name"`
	Parent      string        `json:"parent"`  // physical interface name
	VLANID      int           `json:"vlan_id"` // 1-4094
	Mode        string        `json:"mode"`    // static | dhcp | none
	Address     *netip.Prefix `json:"address,omitempty"`
	Gateway     *netip.Addr   `json:"gateway,omitempty"`
	MTU         int           `json:"mtu,omitempty"`
	Enabled     bool          `json:"enabled"`
	Description string        `json:"description,omitempty"`
	AllowAccess []string      `json:"allow_access,omitempty"` // ping | https | ssh
}
