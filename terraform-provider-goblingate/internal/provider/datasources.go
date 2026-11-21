package provider

import (
	"context"
	"fmt"
	"net/http"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/networkgoblinlabs/goblin-gate/terraform-provider-goblingate/internal/client"
)

type clientHolder struct{ c *client.Client }

func (h *clientHolder) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("got %T", req.ProviderData))
		return
	}
	h.c = c
}

// --- goblingate_interfaces

type interfacesDataSource struct{ clientHolder }

func newInterfacesDataSource() datasource.DataSource { return &interfacesDataSource{} }

type ifaceModel struct {
	Name        string `tfsdk:"name"`
	Device      string `tfsdk:"device"`
	Role        string `tfsdk:"role"`
	Mode        string `tfsdk:"mode"`
	Address     string `tfsdk:"address"`
	Enabled     bool   `tfsdk:"enabled"`
	Description string `tfsdk:"description"`
}

type vlanInfoModel struct {
	Name    string `tfsdk:"name"`
	Parent  string `tfsdk:"parent"`
	VLANID  int64  `tfsdk:"vlan_id"`
	Mode    string `tfsdk:"mode"`
	Address string `tfsdk:"address"`
	Enabled bool   `tfsdk:"enabled"`
}

type interfacesModel struct {
	Interfaces []ifaceModel    `tfsdk:"interfaces"`
	VLANs      []vlanInfoModel `tfsdk:"vlans"`
}

func (d *interfacesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_interfaces"
}

func (d *interfacesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	str := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{Computed: true, Description: desc}
	}
	resp.Schema = schema.Schema{
		Description: "The gate's interfaces and VLANs in the running config.",
		Attributes: map[string]schema.Attribute{
			"interfaces": schema.ListNestedAttribute{
				Computed:    true,
				Description: "The physical interfaces.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"name":        str("The name used in policies and settings, e.g. wan1, lan."),
					"device":      str("The Linux device, e.g. eth0."),
					"role":        str("wan or lan."),
					"mode":        str("static, dhcp, pppoe or none."),
					"address":     str("The gate's address with the prefix length, if static."),
					"enabled":     schema.BoolAttribute{Computed: true, Description: "Whether the interface is up."},
					"description": str("The description."),
				}},
			},
			"vlans": schema.ListNestedAttribute{
				Computed:    true,
				Description: "The VLAN interfaces.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"name":    str("The VLAN interface name."),
					"parent":  str("The physical interface it is tagged on."),
					"vlan_id": schema.Int64Attribute{Computed: true, Description: "The VLAN tag."},
					"mode":    str("static, dhcp or none."),
					"address": str("The gate's address with the prefix length, if static."),
					"enabled": schema.BoolAttribute{Computed: true, Description: "Whether the VLAN is up."},
				}},
			},
		},
	}
}

func (d *interfacesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	cfg, err := d.c.Running(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Could not read the running config", err.Error())
		return
	}
	m := interfacesModel{Interfaces: []ifaceModel{}, VLANs: []vlanInfoModel{}}
	str := func(o map[string]any, k string) string { s, _ := o[k].(string); return s }
	for _, o := range list(cfg, "interfaces") {
		b, _ := o["enabled"].(bool)
		m.Interfaces = append(m.Interfaces, ifaceModel{
			Name: str(o, "name"), Device: str(o, "device"), Role: str(o, "role"), Mode: str(o, "mode"),
			Address: str(o, "address"), Enabled: b, Description: str(o, "description"),
		})
	}
	for _, o := range list(cfg, "vlans") {
		b, _ := o["enabled"].(bool)
		m.VLANs = append(m.VLANs, vlanInfoModel{
			Name: str(o, "name"), Parent: str(o, "parent"), VLANID: readInt(o, "vlan_id"), Mode: str(o, "mode"),
			Address: str(o, "address"), Enabled: b,
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// --- goblingate_devices

type devicesDataSource struct{ clientHolder }

func newDevicesDataSource() datasource.DataSource { return &devicesDataSource{} }

type deviceModel struct {
	MAC       string       `tfsdk:"mac"`
	Name      string       `tfsdk:"name"`
	Hostname  string       `tfsdk:"hostname"`
	Vendor    string       `tfsdk:"vendor"`
	IPs       types.List   `tfsdk:"ips"`
	Interface string       `tfsdk:"interface"`
	VLAN      string       `tfsdk:"vlan"`
	FirstSeen types.String `tfsdk:"first_seen"`
	LastSeen  types.String `tfsdk:"last_seen"`
}

type devicesModel struct {
	Interface types.String  `tfsdk:"interface"`
	Devices   []deviceModel `tfsdk:"devices"`
}

func (d *devicesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_devices"
}

func (d *devicesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	str := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{Computed: true, Description: desc}
	}
	resp.Schema = schema.Schema{
		Description: "The device inventory (Network → Devices): every device the gate has seen, sorted by MAC address.",
		Attributes: map[string]schema.Attribute{
			"interface": schema.StringAttribute{Optional: true, Description: "Only devices on this interface or VLAN."},
			"devices": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"mac":        str("The MAC address."),
					"name":       str("The name given in the inventory."),
					"hostname":   str("The host name the device sent over DHCP."),
					"vendor":     str("The vendor, from the MAC address."),
					"ips":        schema.ListAttribute{Computed: true, ElementType: types.StringType, Description: "Its addresses, newest first."},
					"interface":  str("The interface or VLAN it was seen on."),
					"vlan":       str("The VLAN assigned to it for RADIUS, if any."),
					"first_seen": str("When it was first seen (RFC 3339)."),
					"last_seen":  str("When it was last seen (RFC 3339)."),
				}},
			},
		},
	}
}

func (d *devicesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m devicesModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var res struct {
		Devices []struct {
			MAC       string   `json:"mac"`
			IPs       []string `json:"ips"`
			Hostname  string   `json:"hostname"`
			Vendor    string   `json:"vendor"`
			Interface string   `json:"interface"`
			FirstSeen string   `json:"first_seen"`
			LastSeen  string   `json:"last_seen"`
			Name      string   `json:"name"`
			VLAN      string   `json:"vlan"`
		} `json:"devices"`
	}
	if err := d.c.Do(ctx, http.MethodGet, "/api/v1/devices", nil, &res); err != nil {
		resp.Diagnostics.AddError("Could not read the device inventory", err.Error())
		return
	}
	sort.Slice(res.Devices, func(i, j int) bool { return res.Devices[i].MAC < res.Devices[j].MAC })
	m.Devices = []deviceModel{}
	for _, dv := range res.Devices {
		if !m.Interface.IsNull() && m.Interface.ValueString() != dv.Interface {
			continue
		}
		ips := dv.IPs
		if ips == nil {
			ips = []string{}
		}
		m.Devices = append(m.Devices, deviceModel{
			MAC: dv.MAC, Name: dv.Name, Hostname: dv.Hostname, Vendor: dv.Vendor, IPs: listOf(ips),
			Interface: dv.Interface, VLAN: dv.VLAN, FirstSeen: types.StringValue(dv.FirstSeen), LastSeen: types.StringValue(dv.LastSeen),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
