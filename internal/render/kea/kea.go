// Package kea renders the Kea DHCPv4 configuration from the DHCP section.
package kea

import (
	"encoding/json"
	"fmt"
	"net/netip"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/render"
)

// Path is where the Kea config is written (Debian's kea-dhcp4-server reads it).
const Path = "/etc/kea/kea-dhcp4.conf"

// Paths used by Debian's kea-dhcp4-server package.
const (
	ControlSocket = "/run/kea/kea4-ctrl-socket"
	LeaseFile     = "/var/lib/kea/kea-leases4.csv"
)

// DefaultLeaseSeconds is used when a DHCP server sets no lease_seconds.
const DefaultLeaseSeconds = 3600

// Renderer implements commit.Renderer for Kea DHCPv4.
type Renderer struct{}

// Name returns "kea".
func (Renderer) Name() string { return "kea" }

// Render returns the Kea DHCPv4 configuration file keyed by Path.
// It never touches the OS. The file is always rendered, with no interfaces
// and no subnets when no DHCP server is enabled, so kea stays configured.
func (Renderer) Render(cfg *config.Config) (map[string][]byte, error) {
	b, err := build(cfg)
	if err != nil {
		return nil, fmt.Errorf("render kea: %w", err)
	}
	return map[string][]byte{Path: b}, nil
}

type file struct {
	Dhcp4 dhcp4 `json:"Dhcp4"`
}

type dhcp4 struct {
	InterfacesConfig interfacesConfig `json:"interfaces-config"`
	ControlSocket    controlSocket    `json:"control-socket"`
	LeaseDatabase    leaseDatabase    `json:"lease-database"`
	ValidLifetime    int              `json:"valid-lifetime"`
	Subnet4          []subnet         `json:"subnet4"`
}

type interfacesConfig struct {
	Interfaces []string `json:"interfaces"`
}

type controlSocket struct {
	SocketType string `json:"socket-type"`
	SocketName string `json:"socket-name"`
}

type leaseDatabase struct {
	Type    string `json:"type"`
	Persist bool   `json:"persist"`
	Name    string `json:"name"`
}

type subnet struct {
	ID            int           `json:"id"`
	Subnet        string        `json:"subnet"`
	Interface     string        `json:"interface"`
	Pools         []pool        `json:"pools"`
	OptionData    []optionData  `json:"option-data,omitempty"`
	ValidLifetime int           `json:"valid-lifetime"`
	Reservations  []reservation `json:"reservations,omitempty"`
}

type pool struct {
	Pool string `json:"pool"`
}

type optionData struct {
	Name string `json:"name"`
	Data string `json:"data"`
}

type reservation struct {
	HwAddress string `json:"hw-address"`
	IPAddress string `json:"ip-address"`
	Hostname  string `json:"hostname,omitempty"`
}

// link is the part of an interface or VLAN that DHCP needs.
type link struct {
	device  string
	address *netip.Prefix
	enabled bool
}

func links(cfg *config.Config) map[string]link {
	devs := render.Devices(cfg)
	parentUp := map[string]bool{}
	m := map[string]link{}
	for _, ifc := range cfg.Interfaces {
		parentUp[ifc.Name] = ifc.Enabled
		m[ifc.Name] = link{device: ifc.Device, address: ifc.Address, enabled: ifc.Enabled}
	}
	for _, v := range cfg.VLANs {
		if dev, ok := devs[v.Name]; ok {
			m[v.Name] = link{device: dev, address: v.Address, enabled: v.Enabled && parentUp[v.Parent]}
		}
	}
	return m
}

// build generates the Kea DHCPv4 configuration. Servers on disabled links are
// left out; a server on an unknown link or one without a static IPv4 address
// is an error (config.Validate rejects both).
func build(cfg *config.Config) ([]byte, error) {
	f := file{Dhcp4: dhcp4{
		InterfacesConfig: interfacesConfig{Interfaces: []string{}},
		ControlSocket:    controlSocket{SocketType: "unix", SocketName: ControlSocket},
		LeaseDatabase:    leaseDatabase{Type: "memfile", Persist: true, Name: LeaseFile},
		ValidLifetime:    DefaultLeaseSeconds,
		Subnet4:          []subnet{},
	}}
	ls := links(cfg)
	for i, d := range cfg.DHCP {
		l, ok := ls[d.Interface]
		if !ok {
			return nil, fmt.Errorf("dhcp server %d: unknown interface %q", i, d.Interface)
		}
		if !l.enabled {
			continue
		}
		if l.address == nil || !l.address.Addr().Is4() {
			return nil, fmt.Errorf("dhcp server on %q: interface has no static IPv4 address", d.Interface)
		}
		self := l.address.Addr()
		router := self
		if d.Gateway != nil {
			router = *d.Gateway
		}
		dns := []netip.Addr{self}
		if len(d.DNS) > 0 {
			dns = d.DNS
		}
		opts := []optionData{
			{Name: "routers", Data: router.String()},
			{Name: "domain-name-servers", Data: joinAddrs(dns)},
		}
		opts = append(opts, optionData{Name: "domain-name", Data: cfg.EffectiveLocalDomain()})
		lease := d.LeaseSeconds
		if lease <= 0 {
			lease = DefaultLeaseSeconds
		}
		s := subnet{
			// The id is the server's position + 1. Kea keys leases by subnet id,
			// so reordering DHCP servers makes clients re-request their address.
			ID:            i + 1,
			Subnet:        l.address.Masked().String(),
			Interface:     l.device,
			Pools:         []pool{{Pool: d.RangeStart.String() + " - " + d.RangeEnd.String()}},
			OptionData:    opts,
			ValidLifetime: lease,
		}
		for _, sl := range d.StaticLeases {
			s.Reservations = append(s.Reservations, reservation{HwAddress: sl.MAC, IPAddress: sl.IP.String(), Hostname: sl.Hostname})
		}
		f.Dhcp4.InterfacesConfig.Interfaces = append(f.Dhcp4.InterfacesConfig.Interfaces, l.device)
		f.Dhcp4.Subnet4 = append(f.Dhcp4.Subnet4, s)
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func joinAddrs(as []netip.Addr) string {
	s := ""
	for i, a := range as {
		if i > 0 {
			s += ", "
		}
		s += a.String()
	}
	return s
}
