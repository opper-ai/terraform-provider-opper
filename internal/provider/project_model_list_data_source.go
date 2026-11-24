package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

var (
	_ datasource.DataSource              = (*ProjectModelListDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*ProjectModelListDataSource)(nil)
)

type ProjectModelListDataSource struct{ client *client.Client }

type projectModelListDataModel struct {
	ID                  types.String `tfsdk:"id"`
	ProjectUUID         types.String `tfsdk:"project_uuid"`
	FollowsOrganization types.Bool   `tfsdk:"follows_organization"`
	CompatModelList     types.String `tfsdk:"compat_model_list"`
	DynamicRoutes       types.Bool   `tfsdk:"compat_model_list_dynamic_routes"`
	Enforced            types.Bool   `tfsdk:"compat_model_list_enforced"`
}

func NewProjectModelListDataSource() datasource.DataSource { return &ProjectModelListDataSource{} }

func (d *ProjectModelListDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project_model_list"
}

func (d *ProjectModelListDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{MarkdownDescription: "Read whether a project has its own compatibility model list, and what it is. Requires controls:read; performs no mutation. A project without its own list follows the organization's (see the opper_organization_model_list data source). The API does not tell a project that follows the organization apart from a UUID that is not a live project of the credential's organization: both report follows_organization = true.", Attributes: map[string]schema.Attribute{
		"id":                               schema.StringAttribute{Computed: true, MarkdownDescription: "Project UUID."},
		"project_uuid":                     schema.StringAttribute{Required: true, Validators: projectIDValidators(), MarkdownDescription: "Canonical lowercase project UUID."},
		"follows_organization":             schema.BoolAttribute{Computed: true, MarkdownDescription: "True when the project has no list of its own and follows the organization's. The other attributes are then null."},
		"compat_model_list":                schema.StringAttribute{Computed: true, MarkdownDescription: "The project's own list: all, simplified, recommended, or none (only the organization's dynamic routes are listed)."},
		"compat_model_list_dynamic_routes": schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the organization's dynamic routes are listed to the project's API keys."},
		"compat_model_list_enforced":       schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the project limits its own API keys to what its list shows. While the organization enforces its list, every project is limited whatever this says."},
	}}
}

func (d *ProjectModelListDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected project model-list data source configuration", "Expected an Opper API client.")
		return
	}
	d.client = c
}

func (d *ProjectModelListDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data projectModelListDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	current, found, err := d.client.GetProjectModelList(ctx, data.ProjectUUID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to read project model list", err.Error())
		return
	}
	data.ID = data.ProjectUUID
	data.FollowsOrganization = types.BoolValue(!found)
	data.CompatModelList, data.DynamicRoutes, data.Enforced = types.StringNull(), types.BoolNull(), types.BoolNull()
	if found {
		data.CompatModelList = types.StringValue(current.CompatModelList)
		data.DynamicRoutes = types.BoolValue(current.DynamicRoutes)
		data.Enforced = types.BoolValue(current.Enforced)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
