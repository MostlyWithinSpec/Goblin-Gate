package provider

import (
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/networkgoblinlabs/goblin-gate/terraform-provider-goblingate/internal/client"
)

type policyModel struct {
	ID               types.String `tfsdk:"id"`
	PolicyID         types.Int64  `tfsdk:"policy_id"`
	Name             types.String `tfsdk:"name"`
	Enabled          types.Bool   `tfsdk:"enabled"`
	SrcIntf          types.Set    `tfsdk:"src_intf"`
	DstIntf          types.Set    `tfsdk:"dst_intf"`
	SrcAddr          types.Set    `tfsdk:"src_addr"`
	DstAddr          types.Set    `tfsdk:"dst_addr"`
	Service          types.Set    `tfsdk:"service"`
	Action           types.String `tfsdk:"action"`
	NAT              types.Bool   `tfsdk:"nat"`
	Log              types.String `tfsdk:"log"`
	Schedule         types.String `tfsdk:"schedule"`
	IPSProfile       types.String `tfsdk:"ips_profile"`
	WebFilterProfile types.String `tfsdk:"webfilter_profile"`
	DNSFilterProfile types.String `tfsdk:"dnsfilter_profile"`
	AppCtrlProfile   types.String `tfsdk:"appctrl_profile"`
	ThreatFeeds      types.Set    `tfsdk:"threat_feeds"`
	Gateway          types.String `tfsdk:"gateway"`
}

func wildcard(v string) types.Set {
	return types.SetValueMust(types.StringType, []attr.Value{types.StringValue(v)})
}

func refSet(desc, def string) schema.SetAttribute {
	a := schema.SetAttribute{ElementType: types.StringType, Description: desc}
	if def != "" {
		a.Optional, a.Computed = true, true
		a.Default = setdefault.StaticValue(wildcard(def))
		a.Description += fmt.Sprintf(` Default: ["%s"].`, def)
	} else {
		a.Required = true
	}
	return a
}

func policyLookup(cfg client.Config, m *policyModel) (any, string, bool) {
	if m.PolicyID.IsNull() || m.PolicyID.IsUnknown() {
		return nil, "", false
	}
	id := m.PolicyID.ValueInt64()
	for _, p := range list(cfg, "policies") {
		if readInt(p, "id") == id {
			return p, fmt.Sprintf("policies[id=%d]", id), true
		}
	}
	return nil, "", false
}

func policyResource() resource.Resource {
	return &itemResource[policyModel]{spec: &itemSpec[policyModel]{
		typeName: "policy",
		noun:     "policy",
		schema: schema.Schema{
			Description: "A firewall policy (Policy & Objects → Firewall Policy). New policies are added at the end of the list; the gate checks policies from the top, so reorder them in the web UI if one must come before an existing policy. Import with the policy ID.",
			Attributes: map[string]schema.Attribute{
				"id": idAttr(),
				"policy_id": schema.Int64Attribute{
					Optional:      true,
					Computed:      true,
					Description:   "The policy's number. Default: the highest one in use plus one. Changing it replaces the policy.",
					PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown(), int64planmodifier.RequiresReplace()},
				},
				"name": schema.StringAttribute{Required: true, Description: "The policy name."},
				"enabled": schema.BoolAttribute{
					Optional: true, Computed: true, Default: booldefault.StaticBool(true),
					Description: "Whether the policy is on. Default: true.",
				},
				"src_intf": refSet("Interfaces, VLANs or zones the traffic comes from.", ""),
				"dst_intf": refSet("Interfaces, VLANs or zones the traffic goes to.", ""),
				"src_addr": refSet("Source address or group names, or \"all\".", "all"),
				"dst_addr": refSet("Destination address or group names, or \"all\".", "all"),
				"service":  refSet("Service names, or \"any\".", "any"),
				"action": schema.StringAttribute{
					Required:    true,
					Description: "accept or deny.",
					Validators:  []validator.String{stringvalidator.OneOf("accept", "deny")},
				},
				"nat": schema.BoolAttribute{
					Optional: true, Computed: true, Default: booldefault.StaticBool(false),
					Description: "Masquerade the source address (for traffic to the internet). Default: false.",
				},
				"log": schema.StringAttribute{
					Optional:    true,
					Description: "off, all or deny.",
					Validators:  []validator.String{stringvalidator.OneOf("off", "all", "deny")},
				},
				"schedule":          optStringAttr("The name of a schedule; unset = always."),
				"ips_profile":       optStringAttr("The name of an IPS profile."),
				"webfilter_profile": optStringAttr("The name of a web filter profile."),
				"dnsfilter_profile": optStringAttr("The name of a DNS filter profile."),
				"appctrl_profile":   optStringAttr("The name of an application control profile."),
				"threat_feeds": schema.SetAttribute{
					Optional: true, ElementType: types.StringType,
					Description: "Names of threat feeds to block in this policy.",
				},
				"gateway": optStringAttr("Send the accepted traffic through this gateway or gateway group (policy routing); unset = the default route."),
			},
		},
		id: func(m *policyModel) string { return strconv.FormatInt(m.PolicyID.ValueInt64(), 10) },
		label: func(m *policyModel) string {
			if m.PolicyID.IsNull() || m.PolicyID.IsUnknown() {
				return m.Name.ValueString()
			}
			return fmt.Sprintf("%d (%s)", m.PolicyID.ValueInt64(), m.Name.ValueString())
		},
		setID: func(m *policyModel, id string) { m.ID = types.StringValue(id) },
		fromID: func(id string, m *policyModel) error {
			n, err := strconv.ParseInt(id, 10, 64)
			if err != nil || n <= 0 {
				return fmt.Errorf("want the policy ID (a number), got %q", id)
			}
			m.PolicyID = types.Int64Value(n)
			return nil
		},
		lookup:  policyLookup,
		newPath: func(m *policyModel) string { return fmt.Sprintf("policies[id=%d]", m.PolicyID.ValueInt64()) },
		prepare: func(cand client.Config, m *policyModel) error {
			if !m.PolicyID.IsNull() && !m.PolicyID.IsUnknown() {
				if m.PolicyID.ValueInt64() <= 0 {
					return fmt.Errorf("policy_id must be 1 or more")
				}
				return nil
			}
			var top int64
			for _, p := range list(cand, "policies") {
				top = max(top, readInt(p, "id"))
			}
			m.PolicyID = types.Int64Value(top + 1)
			return nil
		},
		value: func(m *policyModel, ex any) any {
			return merged(ex, map[string]any{
				"id":                m.PolicyID.ValueInt64(),
				"name":              m.Name.ValueString(),
				"enabled":           m.Enabled.ValueBool(),
				"src_intf":          optStrings(m.SrcIntf),
				"dst_intf":          optStrings(m.DstIntf),
				"src_addr":          optStrings(m.SrcAddr),
				"dst_addr":          optStrings(m.DstAddr),
				"service":           optStrings(m.Service),
				"action":            m.Action.ValueString(),
				"nat":               m.NAT.ValueBool(),
				"log":               optString(m.Log),
				"schedule":          optString(m.Schedule),
				"ips_profile":       optString(m.IPSProfile),
				"webfilter_profile": optString(m.WebFilterProfile),
				"dnsfilter_profile": optString(m.DNSFilterProfile),
				"appctrl_profile":   optString(m.AppCtrlProfile),
				"threat_feeds":      optStrings(m.ThreatFeeds),
				"gateway":           optString(m.Gateway),
			})
		},
		read: func(raw any, m *policyModel) {
			o, _ := raw.(map[string]any)
			m.PolicyID = types.Int64Value(readInt(o, "id"))
			m.Name = reqString(o, "name")
			m.Enabled = readBool(o, "enabled")
			m.SrcIntf = readSet(o, "src_intf", m.SrcIntf)
			m.DstIntf = readSet(o, "dst_intf", m.DstIntf)
			m.SrcAddr = readSet(o, "src_addr", m.SrcAddr)
			m.DstAddr = readSet(o, "dst_addr", m.DstAddr)
			m.Service = readSet(o, "service", m.Service)
			m.Action = reqString(o, "action")
			m.NAT = readBool(o, "nat")
			m.Log = readString(o, "log", m.Log)
			m.Schedule = readString(o, "schedule", m.Schedule)
			m.IPSProfile = readString(o, "ips_profile", m.IPSProfile)
			m.WebFilterProfile = readString(o, "webfilter_profile", m.WebFilterProfile)
			m.DNSFilterProfile = readString(o, "dnsfilter_profile", m.DNSFilterProfile)
			m.AppCtrlProfile = readString(o, "appctrl_profile", m.AppCtrlProfile)
			m.ThreatFeeds = readSet(o, "threat_feeds", m.ThreatFeeds)
			m.Gateway = readString(o, "gateway", m.Gateway)
		},
	}}
}
