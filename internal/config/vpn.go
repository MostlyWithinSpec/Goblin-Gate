package config

import "net/netip"

// VPN holds the remote-access VPNs.
type VPN struct {
	Tailscale Tailscale   `json:"tailscale,omitzero"`
	WireGuard []WireGuard `json:"wireguard,omitempty"`
	// WireGuardClients are tunnels to VPN providers used as gateways (task 049).
	WireGuardClients []WireGuardClient `json:"wireguard_clients,omitempty"`
}

// WireGuardKeyDir holds the WireGuard servers' private keys (one
// <name>.key per interface, created by goblind; the directory and files
// belong to group systemd-network, which networkd reads them as - a
// directory under /etc/goblingate would not be reachable). They are never
// part of the config or the API.
const WireGuardKeyDir = "/etc/systemd/network/goblin-wg/"

// DefaultWireGuardPort is the listen port when listen_port is 0.
const DefaultWireGuardPort = 51820

// WireGuard is a WireGuard server interface (task 036). Its name is also
// the kernel device and the interface name in policies (role vpn).
type WireGuard struct {
	Name       string       `json:"name"` // device name: 1-15 of [a-z0-9_-], e.g. wg0
	Enabled    bool         `json:"enabled"`
	ListenPort int          `json:"listen_port,omitempty"` // default 51820, opened on WAN
	Address    netip.Prefix `json:"address"`               // the box's tunnel address, e.g. 10.66.0.1/24
	// Endpoint is the public name or address clients connect to (put in
	// the client configs); empty leaves a placeholder.
	Endpoint    string          `json:"endpoint,omitempty"`
	AllowAccess []string        `json:"allow_access,omitempty"` // ping | https | ssh (DNS is always allowed)
	Peers       []WireGuardPeer `json:"peers,omitempty"`
}

// WireGuardPeer is one client. Only its public key is kept.
type WireGuardPeer struct {
	Name      string     `json:"name"`
	PublicKey string     `json:"public_key"` // base64, 32 bytes
	Address   netip.Addr `json:"address"`    // its tunnel address, inside the interface's subnet
	// AllowedIPs are extra subnets routed to this peer (site-to-site);
	// its tunnel address is always allowed.
	AllowedIPs []netip.Prefix `json:"allowed_ips,omitempty"`
	Keepalive  int            `json:"keepalive,omitempty"` // seconds, 0 = off
}

// TailscaleInterface is the interface name policies use for the tailnet
// (device TailscaleDevice) while Tailscale is enabled.
const (
	TailscaleInterface = "tailscale"
	TailscaleDevice    = "tailscale0"
)

// Tailscale joins the box to a tailnet (task 041). goblind applies these
// settings to tailscaled with its netfilter mode off: the firewall stays
// Goblin's, and traffic to and from the tailnet is controlled by policies
// on the "tailscale" interface (role vpn).
type Tailscale struct {
	Enabled  bool   `json:"enabled"`
	Hostname string `json:"hostname,omitempty"` // default: system hostname
	// AdvertiseRoutes are interface/VLAN names whose subnets the box offers
	// to the tailnet (subnet router). Routes must be approved in the
	// Tailscale admin console unless autoApprovers covers them.
	AdvertiseRoutes []string `json:"advertise_routes,omitempty"`
	ExitNode        bool     `json:"exit_node,omitempty"`     // offer the box as an exit node
	AcceptRoutes    bool     `json:"accept_routes,omitempty"` // use other nodes' subnet routes
	// AcceptDNS lets the tailnet's DNS settings change the box's own
	// resolver; off by default (the box keeps its own).
	AcceptDNS   bool     `json:"accept_dns,omitempty"`
	LoginServer string   `json:"login_server,omitempty"` // Headscale URL; default Tailscale's
	AllowAccess []string `json:"allow_access,omitempty"` // ping | https | ssh on the box from the tailnet
}
