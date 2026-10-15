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
	Notifications Notifications  `json:"notifications,omitzero"`

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
	// SSHKeys may log in as root over SSH (where allow_access has ssh);
	// SSH password login is off.
	SSHKeys []SSHKey `json:"ssh_keys,omitempty"`
	// UpdateChannel picks the Goblin Gate package channel: stable, beta or
	// nightly (task 051). "" = beta until 1.0, then stable.
	UpdateChannel string `json:"update_channel,omitempty"`
	// UpdateURL replaces the Goblin Gate APT repository (a mirror, an
	// air-gapped network, tests); "" = the official repository.
	UpdateURL string `json:"update_url,omitempty"`
}

// SSHKey is one OpenSSH public key, e.g. "ssh-ed25519 AAAA... me@laptop".
type SSHKey struct {
	Name string `json:"name"`
	Key  string `json:"key"`
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
	IPv6        *IPv6         `json:"ipv6,omitempty"`
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
	IPv6        *IPv6         `json:"ipv6,omitempty"`
}

// IPv6 is the IPv6 addressing of an interface or VLAN (task 045). The IPv4
// fields (Mode, Address, Gateway) are independent of it.
type IPv6 struct {
	// Mode: none | dhcp6 (WAN: address by SLAAC/DHCPv6, default route from
	// router advertisements, and a delegated prefix) | static | delegated
	// (LAN side: one /64 of a dhcp6 WAN's delegated prefix).
	Mode    string        `json:"mode"`
	Address *netip.Prefix `json:"address,omitempty"` // static: the box's address, e.g. 2001:db8:1::1/64
	Gateway *netip.Addr   `json:"gateway,omitempty"` // static WAN: the upstream router
	// PDSize is the prefix length a dhcp6 WAN asks its ISP for (48-64; 0 = 56).
	PDSize int `json:"pd_size,omitempty"`
	// Uplink names the dhcp6 WAN whose delegated prefix a delegated
	// interface takes its /64 from ("" = the only dhcp6 WAN).
	Uplink string `json:"uplink,omitempty"`
	// PrefixID is the /64 inside the delegated prefix (0, 1, 2, ... up to
	// 2^(64-pd_size)-1); every delegated interface on an uplink needs its own.
	PrefixID int `json:"prefix_id,omitempty"`
	// RA configures router advertisements (SLAAC for clients) on static and
	// delegated interfaces. nil = on, announcing the box as DNS server.
	RA *RA `json:"ra,omitempty"`
}

// RA is the router advertisement setting of a LAN-side IPv6 interface.
type RA struct {
	Enabled bool `json:"enabled"`
	DNS     bool `json:"dns"`               // announce the box as DNS server (RDNSS)
	Managed bool `json:"managed,omitempty"` // M flag: addresses from a DHCPv6 server (none on the box yet)
	Other   bool `json:"other,omitempty"`   // O flag: other settings from DHCPv6
}

// DefaultPDSize is the prefix length a dhcp6 WAN requests when pd_size is 0.
const DefaultPDSize = 56

// EffectiveRA returns the router advertisement settings of a static or
// delegated interface (nil RA = on with DNS).
func (x *IPv6) EffectiveRA() RA {
	if x.RA == nil {
		return RA{Enabled: true, DNS: true}
	}
	return *x.RA
}

// IPv6Enabled reports whether any interface or VLAN uses IPv6.
func (c *Config) IPv6Enabled() bool {
	for _, i := range c.Interfaces {
		if i.Enabled && i.IPv6 != nil && i.IPv6.Mode != "" && i.IPv6.Mode != "none" {
			return true
		}
	}
	for _, v := range c.VLANs {
		if v.Enabled && v.IPv6 != nil && v.IPv6.Mode != "" && v.IPv6.Mode != "none" {
			return true
		}
	}
	return false
}
