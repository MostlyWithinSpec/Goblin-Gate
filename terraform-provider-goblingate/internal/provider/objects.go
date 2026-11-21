package provider

import (
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/networkgoblinlabs/goblin-gate/terraform-provider-goblingate/internal/client"
)

// Schema pieces shared by the resources.

func idAttr() schema.StringAttribute {
	return schema.StringAttribute{
		Computed:      true,
		Description:   "The object's ID: its name (see each resource for the import format).",
		PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
}

// nameAttr is the name that identifies an object: changing it replaces the
// object (and every reference to the old name must change with it).
func nameAttr(desc string) schema.StringAttribute {
	return schema.StringAttribute{
		Required:      true,
		Description:   desc + " Changing it replaces the object.",
		PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
		Validators:    []validator.String{stringvalidator.LengthBetween(1, 64)},
	}
}

func optStringAttr(desc string) schema.StringAttribute {
	return schema.StringAttribute{Optional: true, Description: desc}
}

// byName finds an object by name in a top-level list such as addresses.
func byName(key string) func(cfg client.Config, name string) (any, string, bool) {
	return func(cfg client.Config, name string) (any, string, bool) {
		it, ok := findBy(list(cfg, key), "name", name, false)
		if !ok {
			return nil, "", false
		}
		return it, fmt.Sprintf("%s[name=%s]", key, name), true
	}
}

func nameID(id string) error {
	if id == "" {
		return fmt.Errorf("want the object's name")
	}
	return nil
}

// --- goblingate_address

type addressModel struct {
	ID      types.String `tfsdk:"id"`
	Name    types.String `tfsdk:"name"`
	Type    types.String `tfsdk:"type"`
	Value   types.String `tfsdk:"value"`
	Comment types.String `tfsdk:"comment"`
}

func addressResource() resource.Resource {
	find := byName("addresses")
	return &itemResource[addressModel]{spec: &itemSpec[addressModel]{
		typeName: "address",
		noun:     "address",
		schema: schema.Schema{
			Description: "An address object (Network → Addresses): a host, subnet, range, FQDN or countries, for use in policies, groups and port forwards. Import with the name.",
			Attributes: map[string]schema.Attribute{
				"id":   idAttr(),
				"name": nameAttr("The address name."),
				"type": schema.StringAttribute{
					Required:    true,
					Description: "host, subnet, range, fqdn or country.",
					Validators:  []validator.String{stringvalidator.OneOf("host", "subnet", "range", "fqdn", "country")},
				},
				"value": schema.StringAttribute{
					Required:    true,
					Description: `The address: "192.168.1.10", "192.168.1.0/24", "192.168.1.10-192.168.1.20", "nas.example.com", or country codes "US,CA".`,
				},
				"comment": optStringAttr("A comment."),
			},
		},
		id:     func(m *addressModel) string { return m.Name.ValueString() },
		label:  func(m *addressModel) string { return m.Name.ValueString() },
		setID:  func(m *addressModel, id string) { m.ID = types.StringValue(id) },
		fromID: func(id string, m *addressModel) error { m.Name = types.StringValue(id); return nameID(id) },
		lookup: func(cfg client.Config, m *addressModel) (any, string, bool) { return find(cfg, m.Name.ValueString()) },
		newPath: func(m *addressModel) string {
			return fmt.Sprintf("addresses[name=%s]", m.Name.ValueString())
		},
		value: func(m *addressModel, ex any) any {
			return merged(ex, map[string]any{"name": m.Name.ValueString(), "type": m.Type.ValueString(), "value": m.Value.ValueString(), "comment": optString(m.Comment)})
		},
		read: func(raw any, m *addressModel) {
			o, _ := raw.(map[string]any)
			m.Type, m.Value = reqString(o, "type"), reqString(o, "value")
			m.Comment = readString(o, "comment", m.Comment)
		},
	}}
}

// --- goblingate_address_group

type addressGroupModel struct {
	ID      types.String `tfsdk:"id"`
	Name    types.String `tfsdk:"name"`
	Members types.Set    `tfsdk:"members"`
}

func addressGroupResource() resource.Resource {
	find := byName("address_groups")
	return &itemResource[addressGroupModel]{spec: &itemSpec[addressGroupModel]{
		typeName: "address_group",
		noun:     "address group",
		schema: schema.Schema{
			Description: "An address group: a name for several address objects. Import with the name.",
			Attributes: map[string]schema.Attribute{
				"id":   idAttr(),
				"name": nameAttr("The group name."),
				"members": schema.SetAttribute{
					Required:    true,
					ElementType: types.StringType,
					Description: "The names of the addresses in the group.",
				},
			},
		},
		id:     func(m *addressGroupModel) string { return m.Name.ValueString() },
		label:  func(m *addressGroupModel) string { return m.Name.ValueString() },
		setID:  func(m *addressGroupModel, id string) { m.ID = types.StringValue(id) },
		fromID: func(id string, m *addressGroupModel) error { m.Name = types.StringValue(id); return nameID(id) },
		lookup: func(cfg client.Config, m *addressGroupModel) (any, string, bool) {
			return find(cfg, m.Name.ValueString())
		},
		newPath: func(m *addressGroupModel) string {
			return fmt.Sprintf("address_groups[name=%s]", m.Name.ValueString())
		},
		value: func(m *addressGroupModel, ex any) any {
			return merged(ex, map[string]any{"name": m.Name.ValueString(), "members": optStrings(m.Members)})
		},
		read: func(raw any, m *addressGroupModel) {
			o, _ := raw.(map[string]any)
			m.Members = readSet(o, "members", m.Members)
		},
	}}
}

// --- goblingate_service

type serviceModel struct {
	ID       types.String `tfsdk:"id"`
	Name     types.String `tfsdk:"name"`
	Protocol types.String `tfsdk:"protocol"`
	Ports    types.String `tfsdk:"ports"`
	Comment  types.String `tfsdk:"comment"`
}

func serviceResource() resource.Resource {
	find := byName("services")
	return &itemResource[serviceModel]{spec: &itemSpec[serviceModel]{
		typeName: "service",
		noun:     "service",
		schema: schema.Schema{
			Description: "A service object: a protocol and ports, for use in policies. Import with the name.",
			Attributes: map[string]schema.Attribute{
				"id":   idAttr(),
				"name": nameAttr("The service name."),
				"protocol": schema.StringAttribute{
					Required:    true,
					Description: "tcp, udp, icmp or any.",
					Validators:  []validator.String{stringvalidator.OneOf("tcp", "udp", "icmp", "any")},
				},
				"ports":   optStringAttr(`Ports and ranges, e.g. "80,443,8000-8100" (tcp and udp).`),
				"comment": optStringAttr("A comment."),
			},
		},
		id:      func(m *serviceModel) string { return m.Name.ValueString() },
		label:   func(m *serviceModel) string { return m.Name.ValueString() },
		setID:   func(m *serviceModel, id string) { m.ID = types.StringValue(id) },
		fromID:  func(id string, m *serviceModel) error { m.Name = types.StringValue(id); return nameID(id) },
		lookup:  func(cfg client.Config, m *serviceModel) (any, string, bool) { return find(cfg, m.Name.ValueString()) },
		newPath: func(m *serviceModel) string { return fmt.Sprintf("services[name=%s]", m.Name.ValueString()) },
		value: func(m *serviceModel, ex any) any {
			return merged(ex, map[string]any{"name": m.Name.ValueString(), "protocol": m.Protocol.ValueString(), "ports": optString(m.Ports), "comment": optString(m.Comment)})
		},
		read: func(raw any, m *serviceModel) {
			o, _ := raw.(map[string]any)
			m.Protocol = reqString(o, "protocol")
			m.Ports = readString(o, "ports", m.Ports)
			m.Comment = readString(o, "comment", m.Comment)
		},
	}}
}
