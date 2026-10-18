package config

import (
	"fmt"
	"net/netip"
)

// checkAddressing validates the mode/address/gateway/mtu/allow_access fields
// shared by interfaces and VLANs. kind is "interface" or "VLAN".
func (v *validator) checkAddressing(p, kind, mode string, addr *netip.Prefix, gw *netip.Addr, mtu int, access []string) {
	if !oneOf(mode, "static", "dhcp", "none") {
		v.add(p+".mode", "unknown mode %q: want static, dhcp or none", mode)
	}
	if mode == "static" && addr == nil {
		v.add(p+".address", "static %s has no address", kind)
	}
	if addr != nil && !addr.Addr().Unmap().Is4() {
		v.add(p+".address", "%s is IPv6: set it under ipv6 (mode static)", addr)
	}
	if gw != nil {
		switch {
		case !gw.Unmap().Is4():
			v.add(p+".gateway", "%s is IPv6: set it under ipv6.gateway", gw)
		case addr != nil && !addr.Contains(*gw):
			v.add(p+".gateway", "gateway %s is outside the %s subnet %s", gw, kind, addr)
		}
	}
	if mtu != 0 && (mtu < 68 || mtu > 9216) {
		v.add(p+".mtu", "mtu %d out of range 68-9216", mtu)
	}
	for j, a := range access {
		if !oneOf(a, "ping", "https", "ssh") {
			v.add(fmt.Sprintf("%s.allow_access[%d]", p, j), "unknown allow_access %q: want ping, https or ssh", a)
		}
	}
}

func (v *validator) checkInterfaces() {
	devSeen := make(map[string]int, len(v.cfg.Interfaces))
	for i, in := range v.cfg.Interfaces {
		p := fmt.Sprintf("interfaces[%d]", i)
		if in.Device == "" {
			v.add(p+".device", "device is required")
		} else if j, dup := devSeen[in.Device]; dup {
			v.add(p+".device", "device %q already used by interface %q (interfaces[%d])", in.Device, v.cfg.Interfaces[j].Name, j)
		} else {
			devSeen[in.Device] = i
		}
		if !oneOf(in.Role, "wan", "lan", "dmz", "guest", "mgmt") {
			v.add(p+".role", "unknown role %q: want wan, lan, dmz, guest or mgmt", in.Role)
		}
		v.checkAddressing(p, "interface", in.Mode, in.Address, in.Gateway, in.MTU, in.AllowAccess)
		if q := in.SQM; q != nil {
			switch {
			case in.Role != "wan":
				v.add(p+".sqm", "smart queue is for WAN interfaces only")
			case q.Enabled && (q.DownloadMbit < 1 || q.DownloadMbit > MaxSQMMbit):
				v.add(p+".sqm.download_mbit", "download speed must be 1-%d Mbit/s (about 90%% of the measured speed)", MaxSQMMbit)
			case q.Enabled && (q.UploadMbit < 1 || q.UploadMbit > MaxSQMMbit):
				v.add(p+".sqm.upload_mbit", "upload speed must be 1-%d Mbit/s (about 90%% of the measured speed)", MaxSQMMbit)
			}
		}
	}
}

func (v *validator) checkVLANs() {
	intfs := toSet(nameList(v.cfg.Interfaces, func(x Interface) string { return x.Name }))
	seen := make(map[string]int, len(v.cfg.VLANs))
	for i, vl := range v.cfg.VLANs {
		p := fmt.Sprintf("vlans[%d]", i)
		if !intfs[vl.Parent] {
			v.add(p+".parent", "unknown parent interface %q", vl.Parent)
		}
		if vl.VLANID < 1 || vl.VLANID > 4094 {
			v.add(p+".vlan_id", "vlan_id %d out of range 1-4094", vl.VLANID)
		}
		key := fmt.Sprintf("%s/%d", vl.Parent, vl.VLANID)
		if j, dup := seen[key]; dup {
			v.add(p+".vlan_id", "duplicate vlan_id %d on parent %q (first used at vlans[%d])", vl.VLANID, vl.Parent, j)
		} else {
			seen[key] = i
		}
		if intfs[vl.Name] {
			v.add(p+".name", "name %q collides with a physical interface; interfaces and VLANs share one namespace", vl.Name)
		}
		v.checkAddressing(p, "VLAN", vl.Mode, vl.Address, vl.Gateway, vl.MTU, vl.AllowAccess)
	}
}

// checkInterfaceOverlap reports addresses that overlap between interfaces.
func (v *validator) checkInterfaceOverlap() {
	type netifAddr struct {
		path, label string
		prefix      netip.Prefix
	}
	var addrs []netifAddr
	for i, in := range v.cfg.Interfaces {
		if in.Address != nil {
			addrs = append(addrs, netifAddr{fmt.Sprintf("interfaces[%d].address", i), fmt.Sprintf("interface %q", in.Name), *in.Address})
		}
	}
	for i, vl := range v.cfg.VLANs {
		if vl.Address != nil {
			addrs = append(addrs, netifAddr{fmt.Sprintf("vlans[%d].address", i), fmt.Sprintf("VLAN %q", vl.Name), *vl.Address})
		}
	}
	for i := 1; i < len(addrs); i++ {
		for j := 0; j < i; j++ {
			if prefixesOverlap(addrs[i].prefix, addrs[j].prefix) {
				v.add(addrs[i].path, "address %s overlaps with %s (%s)", addrs[i].prefix, addrs[j].label, addrs[j].prefix)
			}
		}
	}
}

// netifRole returns the role of an interface or VLAN (VLANs inherit their
// parent's role) and whether the name exists.
func (v *validator) netifRole(name string) (string, bool) {
	for _, in := range v.cfg.Interfaces {
		if in.Name == name {
			return in.Role, true
		}
	}
	for _, vl := range v.cfg.VLANs {
		if vl.Name == name {
			for _, in := range v.cfg.Interfaces {
				if in.Name == vl.Parent {
					return in.Role, true
				}
			}
			return "", true
		}
	}
	return "", false
}

func (v *validator) checkDHCP() {
	type netifInfo struct {
		mode string
		addr *netip.Prefix
	}
	info := map[string]netifInfo{}
	for _, in := range v.cfg.Interfaces {
		info[in.Name] = netifInfo{in.Mode, in.Address}
	}
	for _, vl := range v.cfg.VLANs {
		info[vl.Name] = netifInfo{vl.Mode, vl.Address}
	}
	served := map[string]int{}
	for i, s := range v.cfg.DHCP {
		p := fmt.Sprintf("dhcp[%d]", i)
		if j, dup := served[s.Interface]; dup {
			v.add(p+".interface", "interface %q already has a DHCP server (dhcp[%d])", s.Interface, j)
		} else {
			served[s.Interface] = i
		}
		if c, ok := addrCmp(s.RangeStart, s.RangeEnd); !ok {
			v.add(p+".range_end", "range_start and range_end have different address families")
		} else if c > 0 {
			v.add(p+".range_end", "range_end %s is before range_start %s", s.RangeEnd, s.RangeStart)
		}
		if s.LeaseSeconds < 0 {
			v.add(p+".lease_seconds", "lease_seconds must not be negative")
		}
		ni, exists := info[s.Interface]
		if !exists {
			v.add(p+".interface", "unknown interface %q", s.Interface)
			continue
		}
		if ni.mode != "static" {
			v.add(p+".interface", "interface %q is not static (mode %q)", s.Interface, ni.mode)
			continue
		}
		if ni.addr == nil {
			continue // missing address already reported on the interface itself
		}
		if !ni.addr.Contains(s.RangeStart) {
			v.add(p+".range_start", "%s is outside the interface subnet %s", s.RangeStart, ni.addr)
		}
		if !ni.addr.Contains(s.RangeEnd) {
			v.add(p+".range_end", "%s is outside the interface subnet %s", s.RangeEnd, ni.addr)
		}
		for j, l := range s.StaticLeases {
			if !ni.addr.Contains(l.IP) {
				v.add(fmt.Sprintf("%s.static_leases[%d].ip", p, j), "%s is outside the interface subnet %s", l.IP, ni.addr)
			}
		}
	}
}
