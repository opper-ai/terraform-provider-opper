package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

var (
	_ datasource.DataSource              = (*CustomModelDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*CustomModelDataSource)(nil)
)

type CustomModelDataSource struct{ client *client.Client }

type customModelLookupModel struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	Identifier        types.String `tfsdk:"identifier"`
	Provider          types.String `tfsdk:"model_provider"`
	Type              types.String `tfsdk:"type"`
	CredentialPresent types.Bool   `tfsdk:"credential_present"`
	CreatedAt         types.String `tfsdk:"created_at"`
	UpdatedAt         types.String `tfsdk:"updated_at"`
}

func NewCustomModelDataSource() datasource.DataSource { return &CustomModelDataSource{} }

func (d *CustomModelDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_custom_model"
}

func customModelLookupAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id":                 schema.StringAttribute{Computed: true, MarkdownDescription: "Immutable custom-model UUID."},
		"name":               schema.StringAttribute{Computed: true, MarkdownDescription: "Stored custom-model name."},
		"identifier":         schema.StringAttribute{Computed: true, MarkdownDescription: "Stored provider/model identifier."},
		"model_provider":     schema.StringAttribute{Computed: true, MarkdownDescription: "Declared provider, or null on legacy definitions without one."},
		"type":               schema.StringAttribute{Computed: true, MarkdownDescription: "Declared model type, or null on legacy definitions without one."},
		"credential_present": schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the definition stores a nonempty credential. Does not verify its validity."},
		"created_at":         schema.StringAttribute{Computed: true, MarkdownDescription: "Creation timestamp."},
		"updated_at":         schema.StringAttribute{Computed: true, MarkdownDescription: "Latest stored update timestamp. Hidden credential/configuration changes may not be inferable from metadata."},
	}
}

func customModelLookupObjectType() types.ObjectType {
	return types.ObjectType{AttrTypes: map[string]attr.Type{
		"id": types.StringType, "name": types.StringType, "identifier": types.StringType,
		"model_provider": types.StringType, "type": types.StringType, "credential_present": types.BoolType,
		"created_at": types.StringType, "updated_at": types.StringType,
	}}
}

func customModelLookupObjectValue(model client.CustomModel) types.Object {
	return types.ObjectValueMust(customModelLookupObjectType().AttrTypes, map[string]attr.Value{
		"id": types.StringValue(model.ID), "name": types.StringValue(model.Name), "identifier": types.StringValue(model.Identifier),
		"model_provider": types.StringPointerValue(model.Provider), "type": types.StringPointerValue(model.Type),
		"credential_present": types.BoolValue(model.CredentialPresent),
		"created_at":         types.StringValue(model.CreatedAt), "updated_at": types.StringValue(model.UpdatedAt),
	})
}

func (d *CustomModelDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := customModelLookupAttributes()
	attrs["id"] = schema.StringAttribute{Required: true, Validators: []validator.String{customModelLookupUUIDValidator{}}, MarkdownDescription: "Exact canonical nonzero lowercase custom-model UUID in this provider's organization. Names are not selectors."}
	resp.Schema = schema.Schema{MarkdownDescription: "Read one custom-model definition's metadata by exact UUID. Requires custom_models:read. Credentials, connection extras and change tokens are not exposed; no provider request or inference is performed.", Attributes: attrs}
}

func (d *CustomModelDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected custom-model lookup configuration", "Expected an Opper API client.")
		return
	}
	d.client = c
}

func (d *CustomModelDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data customModelLookupModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	model, err := d.client.GetCustomModel(ctx, data.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to read custom model", err.Error())
		return
	}
	data.ID = types.StringValue(model.ID)
	data.Name, data.Identifier = types.StringValue(model.Name), types.StringValue(model.Identifier)
	data.Provider, data.Type = types.StringPointerValue(model.Provider), types.StringPointerValue(model.Type)
	data.CredentialPresent = types.BoolValue(model.CredentialPresent)
	data.CreatedAt, data.UpdatedAt = types.StringValue(model.CreatedAt), types.StringValue(model.UpdatedAt)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

type customModelLookupUUIDValidator struct{}

func (customModelLookupUUIDValidator) Description(context.Context) string {
	return "Use a canonical nonzero lowercase UUID to identify the exact custom model."
}
func (v customModelLookupUUIDValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (v customModelLookupUUIDValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	id := req.ConfigValue.ValueString()
	if !uuidPattern.MatchString(id) || id == "00000000-0000-0000-0000-000000000000" {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid custom-model UUID", v.Description(ctx))
	}
}
