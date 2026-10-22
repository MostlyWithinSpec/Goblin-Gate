package pfsense

import "encoding/xml"

// The parts of a pfSense config.xml (CE 2.7 / Plus 24.x) the importer reads.
// Presence-only flags (<enable/>, <disabled/>) are *string: non-nil = set.

type pfConfig struct {
	XMLName     xml.Name      // pfsense or opnsense (task 115)
	Version     string        `xml:"version"`
	System      pfSystem      `xml:"system"`
	Interfaces  pfInterfaces  `xml:"interfaces"`
	VLANs       []pfVLAN      `xml:"vlans>vlan"`
	Aliases     []pfAlias     `xml:"aliases>alias"`
	Filter      []pfRule      `xml:"filter>rule"`
	NAT         pfNAT         `xml:"nat"`
	DHCPD       pfAnyIfaces   `xml:"dhcpd"`
	DHCPDv6     pfAnyIfaces   `xml:"dhcpdv6"`
	Unbound     pfUnbound     `xml:"unbound"`
	Schedules   []pfSchedule  `xml:"schedules>schedule"`
	Gateways    pfGateways    `xml:"gateways"`
	Routes      []pfRoute     `xml:"staticroutes>route"`
	OpenVPN     pfOpenVPN     `xml:"openvpn"`
	DynDNS      []pfNamed     `xml:"dyndnses>dyndns"`
	VirtualIPs  []pfNamed     `xml:"virtualip>vip"`
	IfGroups    []pfNamed     `xml:"ifgroups>ifgroupentry"`
	CAs         []pfNamed     `xml:"ca"`
	Certs       []pfNamed     `xml:"cert"`
	Packages    pfPackages    `xml:"installedpackages"`
	ShaperQueue []pfNamed     `xml:"shaper>queue"`
	DNSMasq     *pfDNSMasq    `xml:"dnsmasq"`
	Syslog      *pfAnyElement `xml:"syslog>remoteserver"`
	OPN         *opnMVC       `xml:"OPNsense"` // OPNsense's newer models
}

type pfSystem struct {
	Hostname    string   `xml:"hostname"`
	Domain      string   `xml:"domain"`
	Timezone    string   `xml:"timezone"`
	TimeServers string   `xml:"timeservers"`
	DNSServers  []string `xml:"dnsserver"`
	Users       []pfUser `xml:"user"`
}

type pfUser struct {
	Name           string  `xml:"name"`
	Disabled       *string `xml:"disabled"`
	AuthorizedKeys string  `xml:"authorizedkeys"` // base64 of the authorized_keys lines
}

// pfInterfaces holds <wan>, <lan>, <opt1>... in document order.
type pfInterfaces struct {
	Items []pfInterface `xml:",any"`
}

type pfInterface struct {
	XMLName         xml.Name
	If              string  `xml:"if"`
	Descr           string  `xml:"descr"`
	Enable          *string `xml:"enable"`
	IPAddr          string  `xml:"ipaddr"` // dhcp | pppoe | IPv4
	Subnet          string  `xml:"subnet"`
	Gateway         string  `xml:"gateway"`  // gateway name
	IPAddrV6        string  `xml:"ipaddrv6"` // dhcp6 | slaac | track6 | 6rd | IPv6
	SubnetV6        string  `xml:"subnetv6"`
	GatewayV6       string  `xml:"gatewayv6"`
	Track6Interface string  `xml:"track6-interface"`
	Track6PrefixID  string  `xml:"track6-prefix-id"`
	PDLen           string  `xml:"dhcp6-ia-pd-len"` // 64 - delegated prefix length
	MTU             string  `xml:"mtu"`
	SpoofMAC        string  `xml:"spoofmac"`
	BlockPriv       *string `xml:"blockpriv"`
	BlockBogons     *string `xml:"blockbogons"`
}

type pfVLAN struct {
	If     string `xml:"if"` // parent device
	Tag    int    `xml:"tag"`
	VLANIf string `xml:"vlanif"`
	Descr  string `xml:"descr"`
}

type pfAlias struct {
	Name     string   `xml:"name"`
	Type     string   `xml:"type"` // host | network | port | url | urltable | url_ports | urltable_ports
	Address  string   `xml:"address"`
	URL      string   `xml:"url"`
	AliasURL []string `xml:"aliasurl"`
	Descr    string   `xml:"descr"`
}

type pfAddr struct {
	Any     *string `xml:"any"`
	Network string  `xml:"network"` // lan | lanip | wanip | opt1 | (self) | ...
	Address string  `xml:"address"` // IP, CIDR or alias
	Port    string  `xml:"port"`
	Not     *string `xml:"not"`
}

type pfRule struct {
	Type             string  `xml:"type"` // pass | block | reject (empty = pass)
	Interface        string  `xml:"interface"`
	IPProtocol       string  `xml:"ipprotocol"` // inet | inet6 | inet46
	Protocol         string  `xml:"protocol"`   // tcp | udp | tcp/udp | icmp | ... (empty = any)
	ICMPType         string  `xml:"icmptype"`
	Source           pfAddr  `xml:"source"`
	Destination      pfAddr  `xml:"destination"`
	Disabled         *string `xml:"disabled"`
	Descr            string  `xml:"descr"`
	Log              *string `xml:"log"`
	Sched            string  `xml:"sched"`
	Floating         string  `xml:"floating"`
	Quick            string  `xml:"quick"`
	Direction        string  `xml:"direction"`
	Gateway          string  `xml:"gateway"`
	AssociatedRuleID string  `xml:"associated-rule-id"`
	Tracker          string  `xml:"tracker"`
}

type pfNAT struct {
	Rules    []pfPortForward `xml:"rule"`
	Outbound struct {
		Mode  string `xml:"mode"` // automatic | hybrid | advanced | disabled
		Rules []struct {
			Interface     string  `xml:"interface"`
			Source        pfAddr  `xml:"source"`
			Target        string  `xml:"target"`
			StaticNATPort *string `xml:"staticnatport"`
			Disabled      *string `xml:"disabled"`
			Descr         string  `xml:"descr"`
		} `xml:"rule"`
	} `xml:"outbound"`
}

type pfPortForward struct {
	Interface   string  `xml:"interface"`
	Protocol    string  `xml:"protocol"`
	Source      pfAddr  `xml:"source"`
	Destination pfAddr  `xml:"destination"`
	Target      string  `xml:"target"`
	LocalPort   string  `xml:"local-port"`
	Descr       string  `xml:"descr"`
	Disabled    *string `xml:"disabled"`
	NoRDR       *string `xml:"nordr"`
}

// pfAnyIfaces holds per-interface sections like <dhcpd><lan>...</lan></dhcpd>.
type pfAnyIfaces struct {
	Items []pfDHCP `xml:",any"`
}

type pfDHCP struct {
	XMLName xml.Name
	Enable  *string `xml:"enable"`
	Range   struct {
		From string `xml:"from"`
		To   string `xml:"to"`
	} `xml:"range"`
	StaticMaps []struct {
		MAC      string `xml:"mac"`
		IPAddr   string `xml:"ipaddr"`
		Hostname string `xml:"hostname"`
		Descr    string `xml:"descr"`
	} `xml:"staticmap"`
	DNSServers       []string `xml:"dnsserver"`
	Domain           string   `xml:"domain"`
	DefaultLeaseTime string   `xml:"defaultleasetime"`
	Gateway          string   `xml:"gateway"`
	RAMode           string   `xml:"ramode"`
}

type pfUnbound struct {
	Enable     *string `xml:"enable"`
	DNSSEC     *string `xml:"dnssec"`
	Forwarding *string `xml:"forwarding"`
	RegDHCP    *string `xml:"regdhcp"`
	Hosts      []struct {
		Host    string `xml:"host"`
		Domain  string `xml:"domain"`
		IP      string `xml:"ip"`
		Descr   string `xml:"descr"`
		Aliases struct {
			Items []struct {
				Host   string `xml:"host"`
				Domain string `xml:"domain"`
			} `xml:"item"`
		} `xml:"aliases"`
	} `xml:"hosts"`
	DomainOverrides []struct {
		Domain string `xml:"domain"`
		IP     string `xml:"ip"`
		Descr  string `xml:"descr"`
	} `xml:"domainoverrides"`
}

type pfSchedule struct {
	Name       string `xml:"name"`
	Descr      string `xml:"descr"`
	TimeRanges []struct {
		Position   string `xml:"position"` // days of week, 1 = Monday ... 7 = Sunday
		Month      string `xml:"month"`    // specific dates instead of weekdays
		Day        string `xml:"day"`
		Hour       string `xml:"hour"` // "8:00-17:00"
		RangeDescr string `xml:"rangedescr"`
	} `xml:"timerange"`
}

type pfGatewayItem struct {
	Interface  string  `xml:"interface"`
	Gateway    string  `xml:"gateway"` // IP or "dynamic"
	Name       string  `xml:"name"`
	IPProtocol string  `xml:"ipprotocol"`
	Descr      string  `xml:"descr"`
	DefaultGW  *string `xml:"defaultgw"`
	Monitor    string  `xml:"monitor"`
	MonitorOff *string `xml:"monitor_disable"`
	Disabled   *string `xml:"disabled"`
}

type pfGateways struct {
	Items      []pfGatewayItem `xml:"gateway_item"`
	DefaultGW4 string          `xml:"defaultgw4"`
	DefaultGW6 string          `xml:"defaultgw6"`
	Groups     []struct {
		Name  string   `xml:"name"`
		Items []string `xml:"item"` // "GATEWAY|tier|virtual ip"
		Descr string   `xml:"descr"`
	} `xml:"gateway_group"`
}

type pfRoute struct {
	Network  string  `xml:"network"`
	Gateway  string  `xml:"gateway"`
	Descr    string  `xml:"descr"`
	Disabled *string `xml:"disabled"`
	Enabled  string  `xml:"enabled"` // OPNsense: "0" = disabled
}

type pfOpenVPN struct {
	Servers []pfNamed `xml:"openvpn-server"`
	Clients []pfNamed `xml:"openvpn-client"`
}

// pfNamed is any list element where only its name/description matters.
type pfNamed struct {
	Name        string `xml:"name"`
	Descr       string `xml:"descr"`
	Description string `xml:"description"`
}

func (n pfNamed) label() string {
	for _, s := range []string{n.Descr, n.Description, n.Name} {
		if s != "" {
			return s
		}
	}
	return "(unnamed)"
}

type pfAnyElement struct {
	Value string `xml:",chardata"`
}

type pfPackages struct {
	Packages []struct {
		Name string `xml:"name"`
	} `xml:"package"`
	MiniUPnPd struct {
		Config *struct {
			Enable       *string `xml:"enable"`
			EnableUPnP   *string `xml:"enable_upnp"`
			EnableNATPMP *string `xml:"enable_natpmp"`
			EnableSTUN   *string `xml:"enable_stun"`
			ExtIface     string  `xml:"ext_iface"`
			IfaceArray   string  `xml:"iface_array"`
			PermDefault  *string `xml:"permdefault"`
			Rows         []struct {
				PermUser string `xml:"permuser"`
			} `xml:"row"`
		} `xml:"config"`
	} `xml:"miniupnpd"`
	PfBlockerIPv4  []pfBlockerList `xml:"pfblockernglistsv4>config"`
	PfBlockerDNSBL []pfBlockerList `xml:"pfblockerngdnsbl>config"`
	Suricata       []pfIDSRule     `xml:"suricata>rule"`
	Snort          []pfIDSRule     `xml:"snortglobal>rule"`
	WireGuard      []pfNamed       `xml:"wireguard>tunnels>item"`
	Tailscale      *pfAnyElement   `xml:"tailscale"`
}

type pfBlockerList struct {
	AliasName   string `xml:"aliasname"`
	Description string `xml:"description"`
	Action      string `xml:"action"` // Deny_Inbound | Deny_Outbound | Deny_Both | Permit_* | Disabled | unbound
	Rows        []struct {
		URL    string `xml:"url"`
		Header string `xml:"header"`
		State  string `xml:"state"` // Enabled | Disabled
		Format string `xml:"format"`
	} `xml:"row"`
}

type pfIDSRule struct {
	Interface       string `xml:"interface"`
	Enable          string `xml:"enable"`
	Descr           string `xml:"descr"`
	IPSMode         string `xml:"ips_mode"` // ips_mode_inline | ips_mode_legacy
	BlockOffenders  string `xml:"blockoffenders"`
	BlockOffenders7 string `xml:"blockoffenders7"`
}
