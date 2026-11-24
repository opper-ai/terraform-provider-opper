package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

var (
	_ resource.Resource                   = (*OrganizationModelListResource)(nil)
	_ resource.ResourceWithConfigure      = (*OrganizationModelListResource)(nil)
	_ resource.ResourceWithImportState    = (*OrganizationModelListResource)(nil)
	_ resource.ResourceWithValidateConfig = (*OrganizationModelListResource)(nil)
)

type OrganizationModelListResource struct{ client *client.Client }
type organizationModelListModel struct {
	ID               types.String `tfsdk:"id"`
	OrganizationUUID types.String `tfsdk:"organization_uuid"`
	CompatModelList  types.String `tfsdk:"compat_model_list"`
	DynamicRoutes    types.Bool   `tfsdk:"compat_model_list_dynamic_routes"`
}

func (m *organizationModelListModel) set(value client.OrganizationModelList) {
	m.ID = types.StringValue(value.OrganizationUUID)
	m.OrganizationUUID = types.StringValue(value.OrganizationUUID)
	m.CompatModelList = types.StringValue(value.CompatModelList)
	m.DynamicRoutes = types.BoolValue(value.DynamicRoutes)
}
func NewOrganizationModelListResource() resource.Resource { return &OrganizationModelListResource{} }
func (r *OrganizationModelListResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization_model_list"
}
func (r *OrganizationModelListResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage the organization's compatibility model-list preference. Requires controls:read and controls:write. This changes advertised models, not execution permissions. Creation adopts the existing singleton setting. Destroy relinquishes management and leaves the current preference unchanged. Coordinate other writers; this API has no compare-and-swap guarantee.",
		Attributes: map[string]schema.Attribute{
			"id":                               schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, MarkdownDescription: "Organization UUID, also used for import."},
			"organization_uuid":                schema.StringAttribute{Required: true, Validators: projectIDValidators(), PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}, MarkdownDescription: "Canonical lowercase organization UUID. Must match the management credential's organization before any write. Changing it replaces the Terraform resource, not the organization."},
			"compat_model_list":                schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.OneOf(client.CompatModelListValues...)}, MarkdownDescription: "Models advertised by compat listing endpoints: all, simplified, recommended, or none. With none, only the organization's dynamic routes are listed."},
			"compat_model_list_dynamic_routes": schema.BoolAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()}, MarkdownDescription: "Whether the organization's dynamic routes are listed alongside the models chosen by compat_model_list. Opper defaults it to true. When omitted, the provider leaves the current value unchanged and reports it. Must be true when compat_model_list is none."},
		},
	}
}
func (r *OrganizationModelListResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected model-list resource configuration", "Expected an Opper API client.")
		return
	}
	r.client = c
}

// ValidateConfig refuses at plan time the one combination the API refuses:
// none lists only dynamic routes, so turning them off would list nothing.
func (r *OrganizationModelListResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m organizationModelListModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || m.CompatModelList.IsUnknown() || m.CompatModelList.IsNull() || m.DynamicRoutes.IsUnknown() || m.DynamicRoutes.IsNull() {
		return
	}
	if m.CompatModelList.ValueString() == "none" && !m.DynamicRoutes.ValueBool() {
		resp.Diagnostics.AddAttributeError(path.Root("compat_model_list_dynamic_routes"), "Model list would be empty", "compat_model_list = \"none\" lists only the organization's dynamic routes, so compat_model_list_dynamic_routes must be true (or omitted while the routes are on). Opper refuses this combination because it would list no models at all.")
	}
}

// organizationModelListDesired is the configured target. A nil DynamicRoutes
// means the configuration leaves that field unmanaged: it is not sent and any
// current value is accepted.
type organizationModelListDesired struct {
	CompatModelList string
	DynamicRoutes   *bool
}

func (d organizationModelListDesired) matches(v client.OrganizationModelList) bool {
	return v.CompatModelList == d.CompatModelList && (d.DynamicRoutes == nil || v.DynamicRoutes == *d.DynamicRoutes)
}

// configuredOrganizationModelList reads the desired values from configuration,
// not the plan, so an omitted dynamic-routes value is never sent.
func configuredOrganizationModelList(ctx context.Context, config tfsdk.Config) (organizationModelListDesired, diag.Diagnostics) {
	var list types.String
	var routes types.Bool
	diags := config.GetAttribute(ctx, path.Root("compat_model_list"), &list)
	diags.Append(config.GetAttribute(ctx, path.Root("compat_model_list_dynamic_routes"), &routes)...)
	desired := organizationModelListDesired{CompatModelList: list.ValueString()}
	if !routes.IsNull() && !routes.IsUnknown() {
		value := routes.ValueBool()
		desired.DynamicRoutes = &value
	}
	return desired, diags
}

func organizationModelListIdentity(value client.OrganizationModelList, expected string) error {
	if value.OrganizationUUID != expected {
		return errors.New("the credential's organization UUID does not match the configured organization UUID; no organization is adopted or modified")
	}
	return nil
}

// setOrganizationModelList checks identity immediately before every PUT. An
// uncertain PUT is never retried: one pure GET can establish whether the same
// organization's desired value is current, without claiming writer ownership
// or atomic exclusion of concurrent writers.
func (r *OrganizationModelListResource) setOrganizationModelList(ctx context.Context, expected string, desired organizationModelListDesired) (client.OrganizationModelList, error) {
	current, err := r.client.GetOrganizationModelList(ctx)
	if err != nil {
		return client.OrganizationModelList{}, err
	}
	if err = organizationModelListIdentity(current, expected); err != nil {
		return client.OrganizationModelList{}, err
	}
	if desired.matches(current) {
		return current, nil
	}
	if desired.CompatModelList == "none" && desired.DynamicRoutes == nil && !current.DynamicRoutes {
		// The API would refuse this with a bare HTTP 400; explain it instead.
		return client.OrganizationModelList{}, errors.New("compat_model_list none lists only dynamic routes, and they are currently off for this organization; set compat_model_list_dynamic_routes = true")
	}
	value := desired.CompatModelList
	updated, err := r.client.SetOrganizationModelList(ctx, client.OrganizationModelListChange{CompatModelList: &value, DynamicRoutes: desired.DynamicRoutes})
	if err != nil {
		// A definitive client error is a refusal, not an uncertain success.
		var apiError *client.APIError
		if errors.As(err, &apiError) && apiError.StatusCode >= 400 && apiError.StatusCode < 500 {
			return client.OrganizationModelList{}, err
		}
		observed, readErr := r.client.GetOrganizationModelList(ctx)
		if readErr != nil {
			return client.OrganizationModelList{}, fmt.Errorf("%w; a recovery GET also failed: %v; inspect the current preference before retrying", err, readErr)
		}
		if identityErr := organizationModelListIdentity(observed, expected); identityErr != nil {
			return client.OrganizationModelList{}, identityErr
		}
		if desired.matches(observed) {
			return observed, nil
		}
		return client.OrganizationModelList{}, fmt.Errorf("%w; a recovery GET did not observe the desired model-list preference; no PUT retry was sent", err)
	}
	if err = organizationModelListIdentity(updated, expected); err != nil {
		return client.OrganizationModelList{}, err
	}
	if !desired.matches(updated) {
		return client.OrganizationModelList{}, errors.New("Opper returned a different model-list preference after PUT; inspect the current setting before retrying; no overwrite retry was sent")
	}
	return updated, nil
}

func (r *OrganizationModelListResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data organizationModelListModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	desired, diags := configuredOrganizationModelList(ctx, req.Config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	current, err := r.setOrganizationModelList(ctx, data.OrganizationUUID.ValueString(), desired)
	if err != nil {
		resp.State.RemoveResource(ctx)
		resp.Diagnostics.AddError("Unable to set organization model-list preference", err.Error()+" The singleton may already have changed; inspect the current value and import its exact organization UUID to reconcile it.")
		return
	}
	data.set(current)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
func (r *OrganizationModelListResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data organizationModelListModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	current, err := r.client.GetOrganizationModelList(ctx)
	if err == nil {
		err = organizationModelListIdentity(current, data.OrganizationUUID.ValueString())
	}
	if err != nil {
		// Even 404 is an error: this resource never creates or recreates an org.
		resp.Diagnostics.AddError("Unable to read organization model-list preference", err.Error())
		return
	}
	data.set(current)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
func (r *OrganizationModelListResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data, previous organizationModelListModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &previous)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if data.CompatModelList.Equal(previous.CompatModelList) && !data.DynamicRoutes.IsUnknown() && data.DynamicRoutes.Equal(previous.DynamicRoutes) {
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		return
	}
	desired, diags := configuredOrganizationModelList(ctx, req.Config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	current, err := r.setOrganizationModelList(ctx, data.OrganizationUUID.ValueString(), desired)
	if err != nil {
		resp.Diagnostics.Append(resp.State.Set(ctx, &previous)...)
		resp.Diagnostics.AddError("Unable to update organization model-list preference", err.Error())
		return
	}
	data.set(current)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
func (r *OrganizationModelListResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
	// This singleton is an existing organization property. Forgetting it must
	// never reset a preference, create an audit event, or delete an organization.
}
func (r *OrganizationModelListResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !uuidPattern.MatchString(req.ID) {
		resp.Diagnostics.AddError("Invalid organization model-list import ID", "Import the preference using its canonical lowercase organization UUID.")
		return
	}
	current, err := r.client.GetOrganizationModelList(ctx)
	if err == nil {
		err = organizationModelListIdentity(current, req.ID)
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to import organization model-list preference", err.Error())
		return
	}
	var data organizationModelListModel
	data.set(current)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
