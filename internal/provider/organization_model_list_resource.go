package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

var (
	_ resource.Resource                = (*OrganizationModelListResource)(nil)
	_ resource.ResourceWithConfigure   = (*OrganizationModelListResource)(nil)
	_ resource.ResourceWithImportState = (*OrganizationModelListResource)(nil)
)

type OrganizationModelListResource struct{ client *client.Client }
type organizationModelListModel struct {
	ID               types.String `tfsdk:"id"`
	OrganizationUUID types.String `tfsdk:"organization_uuid"`
	CompatModelList  types.String `tfsdk:"compat_model_list"`
}

func (m *organizationModelListModel) set(value client.OrganizationModelList) {
	m.ID = types.StringValue(value.OrganizationUUID)
	m.OrganizationUUID = types.StringValue(value.OrganizationUUID)
	m.CompatModelList = types.StringValue(value.CompatModelList)
}
func NewOrganizationModelListResource() resource.Resource { return &OrganizationModelListResource{} }
func (r *OrganizationModelListResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization_model_list"
}
func (r *OrganizationModelListResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage the organization's compatibility model-list preference. Requires controls:read and controls:write. This changes advertised models, not execution permissions. Creation adopts the existing singleton setting. Destroy relinquishes management and leaves the current preference unchanged. Coordinate other writers; this API has no compare-and-swap guarantee.",
		Attributes: map[string]schema.Attribute{
			"id":                schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, MarkdownDescription: "Organization UUID, also used for import."},
			"organization_uuid": schema.StringAttribute{Required: true, Validators: projectIDValidators(), PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}, MarkdownDescription: "Canonical lowercase organization UUID. Must match the management credential's organization before any write. Changing it replaces the Terraform resource, not the organization."},
			"compat_model_list": schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.OneOf("all", "simplified", "recommended")}, MarkdownDescription: "Models advertised by compat listing endpoints: all, simplified, or recommended."},
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
func (r *OrganizationModelListResource) setOrganizationModelList(ctx context.Context, expected, value string) (client.OrganizationModelList, error) {
	current, err := r.client.GetOrganizationModelList(ctx)
	if err != nil {
		return client.OrganizationModelList{}, err
	}
	if err = organizationModelListIdentity(current, expected); err != nil {
		return client.OrganizationModelList{}, err
	}
	if current.CompatModelList == value {
		return current, nil
	}
	updated, err := r.client.SetOrganizationModelList(ctx, value)
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
		if observed.CompatModelList == value {
			return observed, nil
		}
		return client.OrganizationModelList{}, fmt.Errorf("%w; a recovery GET did not observe the desired model-list preference; no PUT retry was sent", err)
	}
	if err = organizationModelListIdentity(updated, expected); err != nil {
		return client.OrganizationModelList{}, err
	}
	if updated.CompatModelList != value {
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
	current, err := r.setOrganizationModelList(ctx, data.OrganizationUUID.ValueString(), data.CompatModelList.ValueString())
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
	if data.CompatModelList.Equal(previous.CompatModelList) {
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		return
	}
	current, err := r.setOrganizationModelList(ctx, data.OrganizationUUID.ValueString(), data.CompatModelList.ValueString())
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
