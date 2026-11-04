package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

var _ provider.Provider = (*OpperProvider)(nil)

type OpperProvider struct{ version string }

type providerModel struct {
	ManagementAPIKey types.String `tfsdk:"management_api_key"`
	BaseURL          types.String `tfsdk:"base_url"`
}

func New(version string) func() provider.Provider {
	return func() provider.Provider { return &OpperProvider{version: version} }
}

func (p *OpperProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "opper"
	resp.Version = p.version
}

func (p *OpperProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage Opper resources through the Management API.",
		Attributes: map[string]schema.Attribute{
			"management_api_key": schema.StringAttribute{
				Optional: true, Sensitive: true,
				MarkdownDescription: "Management API key. Defaults to `OPPER_MANAGEMENT_API_KEY`. Grant read and write scopes for each managed surface, plus projects:delete when managing projects.",
			},
			"base_url": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Opper API origin. Defaults to `OPPER_BASE_URL`, then `https://api.opper.ai`. HTTPS is required except for loopback test servers.",
			},
		},
	}
}

func (p *OpperProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var data providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if data.ManagementAPIKey.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("management_api_key"), "Unknown management API key", "Configure a management API key that is known before applying resources.")
	}
	if data.BaseURL.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("base_url"), "Unknown API origin", "Configure an API origin that is known before applying resources.")
	}
	if resp.Diagnostics.HasError() {
		return
	}
	token := os.Getenv("OPPER_MANAGEMENT_API_KEY")
	if !data.ManagementAPIKey.IsNull() {
		token = data.ManagementAPIKey.ValueString()
	}
	if token == "" {
		resp.Diagnostics.AddAttributeError(path.Root("management_api_key"), "Missing management API key", "Set management_api_key or the OPPER_MANAGEMENT_API_KEY environment variable. Runtime API keys cannot manage projects.")
		return
	}
	baseURL := os.Getenv("OPPER_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.opper.ai"
	}
	if !data.BaseURL.IsNull() {
		baseURL = data.BaseURL.ValueString()
	}
	c, err := client.New(baseURL, token, p.version)
	if err != nil {
		resp.Diagnostics.AddError("Invalid provider configuration", err.Error())
		return
	}
	resp.ResourceData = c
	resp.DataSourceData = c
}

func (p *OpperProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{NewProjectResource, NewAPIKeyResource, NewRuleResource, NewDynamicRouteResource, NewDynamicRouteDeploymentResource, NewProjectWebhookResource}
}

func (p *OpperProvider) DataSources(context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{NewProjectDataSource, NewMemberDataSource, NewMembersDataSource}
}
