package provider

import (
	"context"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

var (
	_ datasource.DataSource              = (*CustomModelsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*CustomModelsDataSource)(nil)
)

type CustomModelsDataSource struct{ client *client.Client }

type customModelsLookupModel struct {
	ID     types.String `tfsdk:"id"`
	Models types.List   `tfsdk:"models"`
}

func NewCustomModelsDataSource() datasource.DataSource { return &CustomModelsDataSource{} }

func (d *CustomModelsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_custom_models"
}

func (d *CustomModelsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Read the complete custom-model metadata inventory in deterministic UUID order. Requires custom_models:read. No credentials, raw extras or state tokens are returned, and no model or provider operation is invoked.",
		Attributes: map[string]schema.Attribute{
			"id":     schema.StringAttribute{Computed: true, MarkdownDescription: "Constant custom-models identifier scoped by this provider's organization credential."},
			"models": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: customModelLookupAttributes()}, MarkdownDescription: "All stored custom-model metadata, sorted by immutable UUID. Empty when none exist. Incomplete pagination is an error."},
		},
	}
}

func (d *CustomModelsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected custom-model inventory configuration", "Expected an Opper API client.")
		return
	}
	d.client = c
}

func (d *CustomModelsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data customModelsLookupModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	models, err := d.client.ListCustomModels(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read custom-model inventory", err.Error())
		return
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	values := make([]attr.Value, 0, len(models))
	for _, model := range models {
		values = append(values, customModelLookupObjectValue(model))
	}
	data.ID = types.StringValue("custom-models")
	modelsValue, diags := types.ListValue(customModelLookupObjectType(), values)
	resp.Diagnostics.Append(diags...)
	if !resp.Diagnostics.HasError() {
		data.Models = modelsValue
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	}
}
