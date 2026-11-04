package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

var (
	_ datasource.DataSource              = (*OrganizationModelListDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*OrganizationModelListDataSource)(nil)
)

type OrganizationModelListDataSource struct{ client *client.Client }

func NewOrganizationModelListDataSource() datasource.DataSource {
	return &OrganizationModelListDataSource{}
}
func (d *OrganizationModelListDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization_model_list"
}
func (d *OrganizationModelListDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{MarkdownDescription: "Read the management credential organization's compatibility model-list preference. Requires controls:read; performs no mutation.", Attributes: map[string]schema.Attribute{
		"id":                schema.StringAttribute{Computed: true, MarkdownDescription: "Organization UUID."},
		"organization_uuid": schema.StringAttribute{Computed: true, MarkdownDescription: "Organization UUID resolved from the management credential."},
		"compat_model_list": schema.StringAttribute{Computed: true, MarkdownDescription: "Current preference: all, simplified, or recommended. This affects advertised models, not execution permissions."},
	}}
}
func (d *OrganizationModelListDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected model-list data source configuration", "Expected an Opper API client.")
		return
	}
	d.client = c
}
func (d *OrganizationModelListDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	current, err := d.client.GetOrganizationModelList(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read organization model-list preference", err.Error())
		return
	}
	var data organizationModelListModel
	data.set(current)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
