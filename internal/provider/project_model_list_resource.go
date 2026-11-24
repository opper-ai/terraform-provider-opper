package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

var (
	_ resource.Resource                   = (*ProjectModelListResource)(nil)
	_ resource.ResourceWithConfigure      = (*ProjectModelListResource)(nil)
	_ resource.ResourceWithImportState    = (*ProjectModelListResource)(nil)
	_ resource.ResourceWithValidateConfig = (*ProjectModelListResource)(nil)
)

type ProjectModelListResource struct{ client *client.Client }

type projectModelListModel struct {
	ID              types.String `tfsdk:"id"`
	ProjectUUID     types.String `tfsdk:"project_uuid"`
	CompatModelList types.String `tfsdk:"compat_model_list"`
	DynamicRoutes   types.Bool   `tfsdk:"compat_model_list_dynamic_routes"`
	Enforced        types.Bool   `tfsdk:"compat_model_list_enforced"`
}

func (m *projectModelListModel) set(value client.ProjectModelList) {
	m.ID = types.StringValue(value.ProjectUUID)
	m.ProjectUUID = types.StringValue(value.ProjectUUID)
	m.CompatModelList = types.StringValue(value.CompatModelList)
	m.DynamicRoutes = types.BoolValue(value.DynamicRoutes)
	m.Enforced = types.BoolValue(value.Enforced)
}

func NewProjectModelListResource() resource.Resource { return &ProjectModelListResource{} }

func (r *ProjectModelListResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project_model_list"
}

func (r *ProjectModelListResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Give one project its own compatibility model list, which replaces the organization's list for the project's API keys. Requires controls:read and controls:write. Creation refuses a project that already has its own list; import it instead. Destroy removes the project's own list, so the project follows the organization's list again. Coordinate other writers; this API has no compare-and-swap guarantee.",
		Attributes: map[string]schema.Attribute{
			"id":                               schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, MarkdownDescription: "Project UUID, also used for import."},
			"project_uuid":                     schema.StringAttribute{Required: true, Validators: projectIDValidators(), PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}, MarkdownDescription: "Canonical lowercase UUID of a live project in the management credential's organization. Changing it moves the list to another project: the old project follows the organization again."},
			"compat_model_list":                schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.OneOf(client.CompatModelListValues...)}, MarkdownDescription: "Models advertised by compat listing endpoints to the project's API keys: all, simplified, recommended, or none. With none, only the organization's dynamic routes are listed."},
			"compat_model_list_dynamic_routes": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true), MarkdownDescription: "Whether the organization's dynamic routes are listed alongside the models chosen by compat_model_list. Defaults to true. Must be true when compat_model_list is none."},
			"compat_model_list_enforced":       schema.BoolAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()}, MarkdownDescription: "Whether the project's API keys can only call what its list shows (after the rules); any other LLM or embedding model is refused with HTTP 403 and reason model_list_blocked. While the organization enforces its own list, every project is limited whatever this says, its keys can only call what both lists show, and a list wider than the organization's is refused. When omitted, the provider leaves the current value unchanged and reports it (false for a project that followed the organization)."},
		},
	}
}

func (r *ProjectModelListResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected project model-list resource configuration", "Expected an Opper API client.")
		return
	}
	r.client = c
}

// ValidateConfig refuses at plan time the combination the API refuses.
func (r *ProjectModelListResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m projectModelListModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || m.CompatModelList.IsUnknown() || m.CompatModelList.IsNull() || m.DynamicRoutes.IsUnknown() || m.DynamicRoutes.IsNull() {
		return
	}
	if m.CompatModelList.ValueString() == "none" && !m.DynamicRoutes.ValueBool() {
		resp.Diagnostics.AddAttributeError(path.Root("compat_model_list_dynamic_routes"), "Model list would be empty", "compat_model_list = \"none\" lists only the organization's dynamic routes, so compat_model_list_dynamic_routes must be true (or omitted). Opper refuses this combination because it would list no models at all.")
	}
}

// projectModelListDesired is the configured target. A nil Enforced leaves
// that field unmanaged: it is not sent and any current value is accepted.
type projectModelListDesired struct {
	CompatModelList string
	DynamicRoutes   bool
	Enforced        *bool
}

func (d projectModelListDesired) matches(v client.ProjectModelList) bool {
	return v.CompatModelList == d.CompatModelList && v.DynamicRoutes == d.DynamicRoutes && (d.Enforced == nil || v.Enforced == *d.Enforced)
}

// desiredProjectModelList takes the list and routes from the plan, where the
// routes default applies, and enforced from configuration, so an omitted
// value is never sent.
func desiredProjectModelList(ctx context.Context, plan projectModelListModel, config tfsdk.Config) (projectModelListDesired, diag.Diagnostics) {
	var enforced types.Bool
	diags := config.GetAttribute(ctx, path.Root("compat_model_list_enforced"), &enforced)
	return projectModelListDesired{CompatModelList: plan.CompatModelList.ValueString(), DynamicRoutes: plan.DynamicRoutes.ValueBool(), Enforced: configuredBool(enforced)}, diags
}

// projectModelListRefusal explains the refusals the API reports without a
// body the provider may show.
func projectModelListRefusal(err error) error {
	var apiError *client.APIError
	if !errors.As(err, &apiError) {
		return err
	}
	switch apiError.StatusCode {
	case http.StatusBadRequest:
		return fmt.Errorf("%w. Opper refused the list. While the organization enforces its own list (compat_model_list_enforced on opper_organization_model_list), a project's list cannot be wider than the organization's: no broader compat_model_list, and no dynamic routes when the organization does not list them.", err)
	case http.StatusNotFound:
		return fmt.Errorf("%w. The project is missing, deleted or in another organization than the management credential.", err)
	}
	return err
}

// setProjectModelList sends one PUT and never retries it. After an uncertain
// outcome, one pure read establishes whether the desired list is current.
func (r *ProjectModelListResource) setProjectModelList(ctx context.Context, projectUUID string, desired projectModelListDesired) (client.ProjectModelList, error) {
	updated, err := r.client.SetProjectModelList(ctx, projectUUID, client.ProjectModelListChange{CompatModelList: desired.CompatModelList, DynamicRoutes: desired.DynamicRoutes, Enforced: desired.Enforced})
	if err != nil {
		var apiError *client.APIError
		if errors.As(err, &apiError) && apiError.StatusCode >= 400 && apiError.StatusCode < 500 {
			return client.ProjectModelList{}, projectModelListRefusal(err)
		}
		observed, found, readErr := r.client.GetProjectModelList(ctx, projectUUID)
		if readErr != nil {
			return client.ProjectModelList{}, fmt.Errorf("%w; a recovery GET also failed: %v; inspect the project's model list before retrying", err, readErr)
		}
		if found && desired.matches(observed) {
			return observed, nil
		}
		return client.ProjectModelList{}, fmt.Errorf("%w; a recovery GET did not observe the desired project model list; no PUT retry was sent", err)
	}
	if !desired.matches(updated) {
		return client.ProjectModelList{}, errors.New("Opper returned a different project model list after PUT; inspect the current setting before retrying; no overwrite retry was sent")
	}
	return updated, nil
}

func (r *ProjectModelListResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data projectModelListModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	desired, diags := desiredProjectModelList(ctx, data, req.Config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	projectUUID := data.ProjectUUID.ValueString()
	// Creation never silently takes over a list someone else set.
	_, found, err := r.client.GetProjectModelList(ctx, projectUUID)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read project model lists", err.Error())
		return
	}
	if found {
		resp.Diagnostics.AddError("Project already has its own model list", "Import it with its project UUID to manage it with Terraform, or remove it in Opper first. No change was made.")
		return
	}
	current, err := r.setProjectModelList(ctx, projectUUID, desired)
	if err != nil {
		detail := err.Error()
		var apiError *client.APIError
		if !errors.As(err, &apiError) || apiError.StatusCode >= 500 {
			detail += " The project may now have its own list; inspect it and import the project UUID to reconcile it."
		}
		resp.Diagnostics.AddError("Unable to set project model list", detail)
		return
	}
	data.set(current)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ProjectModelListResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data projectModelListModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	current, found, err := r.client.GetProjectModelList(ctx, data.ProjectUUID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to read project model list", err.Error())
		return
	}
	if !found {
		// The project follows the organization again, or no longer exists.
		resp.State.RemoveResource(ctx)
		return
	}
	data.set(current)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ProjectModelListResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data, previous projectModelListModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &previous)...)
	if resp.Diagnostics.HasError() {
		return
	}
	desired, diags := desiredProjectModelList(ctx, data, req.Config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	current, err := r.setProjectModelList(ctx, data.ProjectUUID.ValueString(), desired)
	if err != nil {
		resp.Diagnostics.Append(resp.State.Set(ctx, &previous)...)
		resp.Diagnostics.AddError("Unable to update project model list", err.Error())
		return
	}
	data.set(current)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ProjectModelListResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data projectModelListModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// A deleted project has no list left to clear.
	if err := r.client.ClearProjectModelList(ctx, data.ProjectUUID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Unable to clear project model list", err.Error())
	}
}

func (r *ProjectModelListResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !uuidPattern.MatchString(req.ID) {
		resp.Diagnostics.AddError("Invalid project model-list import ID", "Import the list using its canonical lowercase project UUID.")
		return
	}
	current, found, err := r.client.GetProjectModelList(ctx, req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Unable to import project model list", err.Error())
		return
	}
	if !found {
		resp.Diagnostics.AddError("Project has no model list of its own", "The project follows the organization's model list, or is not a live project of the management credential's organization. Create the resource instead of importing it.")
		return
	}
	var data projectModelListModel
	data.set(current)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
