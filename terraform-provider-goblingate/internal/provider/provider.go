// Package provider is the Terraform / OpenTofu provider for Goblin Gate.
package provider

import (
	"context"
	"os"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/networkgoblinlabs/goblin-gate/terraform-provider-goblingate/internal/client"
)

// New returns the provider factory for providerserver.
func New(version string) func() provider.Provider {
	return func() provider.Provider { return &gateProvider{version: version} }
}

type gateProvider struct{ version string }

type providerModel struct {
	URL            types.String `tfsdk:"url"`
	Token          types.String `tfsdk:"token"`
	Insecure       types.Bool   `tfsdk:"insecure"`
	CAFile         types.String `tfsdk:"ca_file"`
	ConfirmMinutes types.Int64  `tfsdk:"confirm_minutes"`
}

func (p *gateProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "goblingate"
	resp.Version = p.version
}

func (p *gateProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Goblin Gate firewall through its API. Every apply step is one change of the candidate config and one commit.",
		Attributes: map[string]schema.Attribute{
			"url": schema.StringAttribute{
				Optional:    true,
				Description: "The gate's address, e.g. https://gate.home.lan. Default: the GOBLINGATE_URL environment variable.",
			},
			"token": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "An API token (System → API Tokens) with the scopes read, commit and write:firewall and/or write:network. Default: GOBLINGATE_TOKEN.",
			},
			"insecure": schema.BoolAttribute{
				Optional:    true,
				Description: "Skip the TLS certificate check (a gate with its self-signed certificate). Prefer ca_file. Default: GOBLINGATE_INSECURE.",
			},
			"ca_file": schema.StringAttribute{
				Optional:    true,
				Description: "A PEM file with the CA that signed the gate's certificate, such as the gate's internal CA root. Default: GOBLINGATE_CA_FILE.",
			},
			"confirm_minutes": schema.Int64Attribute{
				Optional:    true,
				Description: "Commit with a confirm timer of this many minutes and confirm once the gate still answers; a change that cuts Terraform off is rolled back. 0 (default) commits without a timer.",
			},
		},
	}
}

func (p *gateProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var m providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	str := func(v types.String, env string) string {
		if !v.IsNull() && !v.IsUnknown() {
			return v.ValueString()
		}
		return os.Getenv(env)
	}
	o := client.Options{
		URL:       str(m.URL, "GOBLINGATE_URL"),
		Token:     str(m.Token, "GOBLINGATE_TOKEN"),
		CAFile:    str(m.CAFile, "GOBLINGATE_CA_FILE"),
		UserAgent: "terraform-provider-goblingate/" + p.version,
	}
	if !m.Insecure.IsNull() && !m.Insecure.IsUnknown() {
		o.Insecure = m.Insecure.ValueBool()
	} else if v := os.Getenv("GOBLINGATE_INSECURE"); v != "" {
		o.Insecure, _ = strconv.ParseBool(v)
	}
	if !m.ConfirmMinutes.IsNull() && !m.ConfirmMinutes.IsUnknown() {
		o.ConfirmMinutes = int(m.ConfirmMinutes.ValueInt64())
	}
	c, err := client.New(o)
	if err != nil {
		resp.Diagnostics.AddError("Goblin Gate provider", err.Error())
		return
	}
	resp.ResourceData = c
	resp.DataSourceData = c
}

func (p *gateProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		addressResource,
		addressGroupResource,
		serviceResource,
		policyResource,
		portForwardResource,
		dhcpStaticLeaseResource,
		dnsRecordResource,
		vlanResource,
	}
}

func (p *gateProvider) DataSources(context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{newInterfacesDataSource, newDevicesDataSource}
}
