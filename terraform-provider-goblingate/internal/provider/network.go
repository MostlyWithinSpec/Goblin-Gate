package provider

import (
	"context"
	"fmt"
	"net/netip"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/networkgoblinlabs/goblin-gate/terraform-provider-goblingate/internal/client"
)

// --- goblingate_port_forward

type portForwardModel struct {
	ID       types.String `tfsdk:"id"`
	Name     types.String `tfsdk:"name"`
	InIntf   types.String `tfsdk:"in_intf"`
	ExtPort  types.String `tfsdk:"ext_port"`
	Protocol types.String `tfsdk:"protocol"`
	ToAddr   types.String `tfsdk:"to_addr"`
	ToPort   types.String `tfsdk:"to_port"`
	Source   types.Set    `tfsdk:"source"`
}

func portForwardResource() resource.Resource {
	lookup := func(cfg client.Config, m *portForwardModel) (any, string, bool) {
		it, ok := findBy(list(cfg, "nat", "port_forwards"), "name", m.Name.ValueString(), false)
		if !ok {
			return nil, "", false
		}
		return it, fmt.Sprintf("nat.port_forwards[name=%s]", m.Name.ValueString()), true
	}
	return &itemResource[portForwardModel]{spec: &itemSpec[portForwardModel]{
		typeName: "port_forward",
		noun:     "port forward",
		schema: schema.Schema{
			Description: "A port forward (Network → Port Forwards): traffic to a port of a WAN interface goes to a host inside. Import with the name.",
			Attributes: map[string]schema.Attribute{
				"id":       idAttr(),
				"name":     nameAttr("The port forward name."),
				"in_intf":  schema.StringAttribute{Required: true, Description: "The interface the traffic comes in on, usually a WAN interface such as wan1."},
				"ext_port": schema.StringAttribute{Required: true, Description: `The outside port or range, e.g. "443" or "8000-8100".`},
				"protocol": schema.StringAttribute{
					Required: true, Description: "tcp or udp.",
					Validators: []validator.String{stringvalidator.OneOf("tcp", "udp")},
				},
				"to_addr": schema.StringAttribute{Required: true, Description: "The inside host's IP address."},
				"to_port": schema.StringAttribute{Required: true, Description: "The inside port or range."},
				"source": schema.SetAttribute{
					Optional: true, ElementType: types.StringType,
					Description: "Address or group names that may use the forward (countries too); unset = anyone.",
				},
			},
		},
		id:     func(m *portForwardModel) string { return m.Name.ValueString() },
		label:  func(m *portForwardModel) string { return m.Name.ValueString() },
		setID:  func(m *portForwardModel, id string) { m.ID = types.StringValue(id) },
		fromID: func(id string, m *portForwardModel) error { m.Name = types.StringValue(id); return nameID(id) },
		lookup: lookup,
		newPath: func(m *portForwardModel) string {
			return fmt.Sprintf("nat.port_forwards[name=%s]", m.Name.ValueString())
		},
		value: func(m *portForwardModel, ex any) any {
			return merged(ex, map[string]any{
				"name": m.Name.ValueString(), "in_intf": m.InIntf.ValueString(), "ext_port": m.ExtPort.ValueString(),
				"protocol": m.Protocol.ValueString(), "to_addr": m.ToAddr.ValueString(), "to_port": m.ToPort.ValueString(),
				"source": optStrings(m.Source),
			})
		},
		read: func(raw any, m *portForwardModel) {
			o, _ := raw.(map[string]any)
			m.InIntf, m.ExtPort, m.Protocol = reqString(o, "in_intf"), reqString(o, "ext_port"), reqString(o, "protocol")
			m.ToAddr, m.ToPort = reqString(o, "to_addr"), reqString(o, "to_port")
			m.Source = readSet(o, "source", m.Source)
		},
	}}
}

// --- goblingate_dhcp_static_lease

type dhcpLeaseModel struct {
	ID        types.String `tfsdk:"id"`
	Interface types.String `tfsdk:"interface"`
	MAC       types.String `tfsdk:"mac"`
	IP        types.String `tfsdk:"ip"`
	Hostname  types.String `tfsdk:"hostname"`
}

var macRE = regexp.MustCompile(`^[0-9a-f]{2}(:[0-9a-f]{2}){5}$`)

func dhcpStaticLeaseResource() resource.Resource {
	lookup := func(cfg client.Config, m *dhcpLeaseModel) (any, string, bool) {
		srv, ok := findBy(list(cfg, "dhcp"), "interface", m.Interface.ValueString(), false)
		if !ok {
			return nil, "", false
		}
		lease, ok := findBy(list(srv, "static_leases"), "mac", m.MAC.ValueString(), true)
		if !ok {
			return nil, "", false
		}
		stored, _ := lease["mac"].(string)
		return lease, fmt.Sprintf("dhcp[interface=%s].static_leases[mac=%s]", m.Interface.ValueString(), stored), true
	}
	return &itemResource[dhcpLeaseModel]{spec: &itemSpec[dhcpLeaseModel]{
		typeName: "dhcp_static_lease",
		noun:     "DHCP static lease",
		schema: schema.Schema{
			Description: "A fixed address that the DHCP server of an interface or VLAN always gives one device. The interface needs a DHCP server. Import with interface/mac, e.g. lan/aa:bb:cc:dd:ee:ff.",
			Attributes: map[string]schema.Attribute{
				"id": idAttr(),
				"interface": schema.StringAttribute{
					Required: true, Description: "The interface or VLAN whose DHCP server gives the address. Changing it replaces the lease.",
					PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				},
				"mac": schema.StringAttribute{
					Required: true, Description: "The device's MAC address, lower case with colons. Changing it replaces the lease.",
					PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
					Validators:    []validator.String{stringvalidator.RegexMatches(macRE, "want a lower-case MAC address like aa:bb:cc:dd:ee:ff")},
				},
				"ip": schema.StringAttribute{
					Required: true, Description: "The address the device gets.",
					Validators: []validator.String{ipValidator{}},
				},
				"hostname": optStringAttr("A host name for the device (also a DNS name under the local domain)."),
			},
		},
		id:    func(m *dhcpLeaseModel) string { return m.Interface.ValueString() + "/" + m.MAC.ValueString() },
		label: func(m *dhcpLeaseModel) string { return m.MAC.ValueString() + " on " + m.Interface.ValueString() },
		setID: func(m *dhcpLeaseModel, id string) { m.ID = types.StringValue(id) },
		fromID: func(id string, m *dhcpLeaseModel) error {
			intf, mac, ok := strings.Cut(id, "/")
			mac = strings.ToLower(mac)
			if !ok || intf == "" || !macRE.MatchString(mac) {
				return fmt.Errorf("want interface/mac, e.g. lan/aa:bb:cc:dd:ee:ff, got %q", id)
			}
			m.Interface, m.MAC = types.StringValue(intf), types.StringValue(mac)
			return nil
		},
		lookup: lookup,
		newPath: func(m *dhcpLeaseModel) string {
			return fmt.Sprintf("dhcp[interface=%s].static_leases[mac=%s]", m.Interface.ValueString(), m.MAC.ValueString())
		},
		prepare: func(cand client.Config, m *dhcpLeaseModel) error {
			if _, ok := findBy(list(cand, "dhcp"), "interface", m.Interface.ValueString(), false); !ok {
				return fmt.Errorf("there is no DHCP server on %s; turn DHCP on for it first", m.Interface.ValueString())
			}
			return nil
		},
		value: func(m *dhcpLeaseModel, ex any) any {
			mac := m.MAC.ValueString()
			if o, ok := ex.(map[string]any); ok {
				if s, ok := o["mac"].(string); ok {
					mac = s // keep the stored spelling
				}
			}
			return merged(ex, map[string]any{"mac": mac, "ip": m.IP.ValueString(), "hostname": optString(m.Hostname)})
		},
		read: func(raw any, m *dhcpLeaseModel) {
			o, _ := raw.(map[string]any)
			m.IP = reqString(o, "ip")
			m.Hostname = readString(o, "hostname", m.Hostname)
		},
	}}
}

// --- goblingate_dns_record

type dnsRecordModel struct {
	ID   types.String `tfsdk:"id"`
	Name types.String `tfsdk:"name"`
	IP   types.String `tfsdk:"ip"`
}

func dnsRecordResource() resource.Resource {
	recPath := func(name string) string { return "dns.local_records.[" + name + "]" }
	lookup := func(cfg client.Config, m *dnsRecordModel) (any, string, bool) {
		recs := object(cfg, "dns", "local_records")
		v, ok := recs[m.Name.ValueString()]
		if !ok {
			return nil, "", false
		}
		return v, recPath(m.Name.ValueString()), true
	}
	return &itemResource[dnsRecordModel]{spec: &itemSpec[dnsRecordModel]{
		typeName: "dns_record",
		noun:     "DNS record",
		schema: schema.Schema{
			Description: "A local DNS name (Network → DNS → Local records) that the gate's resolver answers with an address. Import with the name.",
			Attributes: map[string]schema.Attribute{
				"id": idAttr(),
				"name": schema.StringAttribute{
					Required: true, Description: "The full name, e.g. nas.home.lan. Changing it replaces the record.",
					PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
					Validators: []validator.String{
						stringvalidator.LengthBetween(1, 253),
						stringvalidator.RegexMatches(regexp.MustCompile(`^[A-Za-z0-9_.-]+$`), "want a DNS name such as nas.home.lan"),
					},
				},
				"ip": schema.StringAttribute{Required: true, Description: "The IPv4 or IPv6 address.", Validators: []validator.String{ipValidator{}}},
			},
		},
		id:      func(m *dnsRecordModel) string { return m.Name.ValueString() },
		label:   func(m *dnsRecordModel) string { return m.Name.ValueString() },
		setID:   func(m *dnsRecordModel, id string) { m.ID = types.StringValue(id) },
		fromID:  func(id string, m *dnsRecordModel) error { m.Name = types.StringValue(id); return nameID(id) },
		lookup:  lookup,
		newPath: func(m *dnsRecordModel) string { return recPath(m.Name.ValueString()) },
		value:   func(m *dnsRecordModel, _ any) any { return m.IP.ValueString() },
		read: func(raw any, m *dnsRecordModel) {
			s, _ := raw.(string)
			m.IP = types.StringValue(s)
		},
	}}
}

// --- goblingate_vlan

type vlanModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Parent      types.String `tfsdk:"parent"`
	VLANID      types.Int64  `tfsdk:"vlan_id"`
	Mode        types.String `tfsdk:"mode"`
	Address     types.String `tfsdk:"address"`
	Gateway     types.String `tfsdk:"gateway"`
	MTU         types.Int64  `tfsdk:"mtu"`
	Enabled     types.Bool   `tfsdk:"enabled"`
	Description types.String `tfsdk:"description"`
	AllowAccess types.Set    `tfsdk:"allow_access"`
}

func vlanResource() resource.Resource {
	find := byName("vlans")
	return &itemResource[vlanModel]{spec: &itemSpec[vlanModel]{
		typeName: "vlan",
		noun:     "VLAN",
		schema: schema.Schema{
			Description: "A VLAN interface (Network → Interfaces). Its IPv6 settings, set in the web UI, are kept. Import with the name.",
			Attributes: map[string]schema.Attribute{
				"id":     idAttr(),
				"name":   nameAttr("The VLAN interface name, used in policies, DHCP and the other settings."),
				"parent": schema.StringAttribute{Required: true, Description: "The physical interface the VLAN is tagged on, e.g. lan."},
				"vlan_id": schema.Int64Attribute{
					Required: true, Description: "The VLAN tag, 1-4094.",
					Validators: []validator.Int64{int64validator.Between(1, 4094)},
				},
				"mode": schema.StringAttribute{
					Optional: true, Computed: true, Default: stringdefault.StaticString("static"),
					Description: "static (the gate has an address in it), dhcp (the gate is a DHCP client) or none. Default: static.",
					Validators:  []validator.String{stringvalidator.OneOf("static", "dhcp", "none")},
				},
				"address": optStringAttr("The gate's address in the VLAN with the prefix length, e.g. 192.168.20.1/24 (mode static)."),
				"gateway": optStringAttr("An upstream gateway in the VLAN, if it is an uplink."),
				"mtu": schema.Int64Attribute{
					Optional: true, Description: "The MTU; unset = the parent's.",
					Validators: []validator.Int64{int64validator.Between(576, 9216)},
				},
				"enabled": schema.BoolAttribute{
					Optional: true, Computed: true, Default: booldefault.StaticBool(true),
					Description: "Whether the VLAN is up. Default: true.",
				},
				"description": optStringAttr("A description."),
				"allow_access": schema.SetAttribute{
					Optional: true, ElementType: types.StringType,
					Description: `What devices in the VLAN may reach on the gate itself: "ping", "https", "ssh".`,
				},
			},
		},
		id:      func(m *vlanModel) string { return m.Name.ValueString() },
		label:   func(m *vlanModel) string { return m.Name.ValueString() },
		setID:   func(m *vlanModel, id string) { m.ID = types.StringValue(id) },
		fromID:  func(id string, m *vlanModel) error { m.Name = types.StringValue(id); return nameID(id) },
		lookup:  func(cfg client.Config, m *vlanModel) (any, string, bool) { return find(cfg, m.Name.ValueString()) },
		newPath: func(m *vlanModel) string { return fmt.Sprintf("vlans[name=%s]", m.Name.ValueString()) },
		value: func(m *vlanModel, ex any) any {
			return merged(ex, map[string]any{
				"name": m.Name.ValueString(), "parent": m.Parent.ValueString(), "vlan_id": m.VLANID.ValueInt64(),
				"mode": m.Mode.ValueString(), "address": optString(m.Address), "gateway": optString(m.Gateway),
				"mtu": optInt(m.MTU), "enabled": m.Enabled.ValueBool(), "description": optString(m.Description),
				"allow_access": optStrings(m.AllowAccess),
			})
		},
		read: func(raw any, m *vlanModel) {
			o, _ := raw.(map[string]any)
			m.Parent = reqString(o, "parent")
			m.VLANID = types.Int64Value(readInt(o, "vlan_id"))
			m.Mode = reqString(o, "mode")
			m.Address = readString(o, "address", m.Address)
			m.Gateway = readString(o, "gateway", m.Gateway)
			m.MTU = readOptInt(o, "mtu", m.MTU)
			m.Enabled = readBool(o, "enabled")
			m.Description = readString(o, "description", m.Description)
			m.AllowAccess = readSet(o, "allow_access", m.AllowAccess)
		},
	}}
}

// ipValidator accepts an IPv4 or IPv6 address.
type ipValidator struct{}

func (ipValidator) Description(_ context.Context) string             { return "an IPv4 or IPv6 address" }
func (v ipValidator) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }
func (ipValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if _, err := netip.ParseAddr(req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid IP address", fmt.Sprintf("%q is not an IP address", req.ConfigValue.ValueString()))
	}
}
