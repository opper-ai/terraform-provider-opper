package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

var (
	_ datasource.DataSource              = (*ProjectDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*ProjectDataSource)(nil)
)

type ProjectDataSource struct{ client *client.Client }

func NewProjectDataSource() datasource.DataSource { return &ProjectDataSource{} }

func (d *ProjectDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project"
}

func (d *ProjectDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Look up an existing Opper project by UUID without managing its lifecycle.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Required: true, Validators: projectIDValidators(), MarkdownDescription: "Project UUID."},
			"name":        schema.StringAttribute{Computed: true, MarkdownDescription: "Project name."},
			"description": schema.StringAttribute{Computed: true, MarkdownDescription: "Project description. Empty when unset."},
			"created_at":  schema.StringAttribute{Computed: true, MarkdownDescription: "Creation timestamp, when provided by Opper."},
		},
	}
}

func (d *ProjectDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected data source configuration", "Expected an Opper API client. Report this provider error to the maintainers.")
		return
	}
	d.client = c
}

func (d *ProjectDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data projectModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := d.client.GetProject(ctx, data.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to read project", err.Error())
		return
	}
	data.setProject(p)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
