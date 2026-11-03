package provider

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/go-uuid"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

var (
	_ resource.Resource                = (*DynamicRouteDeploymentResource)(nil)
	_ resource.ResourceWithConfigure   = (*DynamicRouteDeploymentResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*DynamicRouteDeploymentResource)(nil)
	_ resource.ResourceWithImportState = (*DynamicRouteDeploymentResource)(nil)
)

type DynamicRouteDeploymentResource struct{ client *client.Client }

type dynamicRouteDeploymentModel struct {
	ID                          types.String         `tfsdk:"id"`
	RouteName                   types.String         `tfsdk:"route_name"`
	RouteUUID                   types.String         `tfsdk:"route_uuid"`
	DraftRevision               types.Int64          `tfsdk:"draft_revision"`
	Note                        types.String         `tfsdk:"note"`
	Deployed                    types.Bool           `tfsdk:"deployed"`
	OperationID                 types.String         `tfsdk:"operation_id"`
	OperationPending            types.Bool           `tfsdk:"operation_pending"`
	VersionNumber               types.Int64          `tfsdk:"version_number"`
	VersionUUID                 types.String         `tfsdk:"version_uuid"`
	DeployedDraftRevision       types.Int64          `tfsdk:"deployed_draft_revision"`
	DeployedGraphJSON           jsontypes.Normalized `tfsdk:"deployed_graph_json"`
	ObservedActiveVersionNumber types.Int64          `tfsdk:"observed_active_version_number"`
	ObservedActiveVersionUUID   types.String         `tfsdk:"observed_active_version_uuid"`
	VersionCreatedAt            types.String         `tfsdk:"version_created_at"`
}

func NewDynamicRouteDeploymentResource() resource.Resource { return &DynamicRouteDeploymentResource{} }

func (r *DynamicRouteDeploymentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dynamic_route_deployment"
}

func (r *DynamicRouteDeploymentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage the active lifecycle of one Opper routing graph. Deploys atomically require the intended route UUID, draft revision and observed active version. Disable and destroy undeploy only the version this resource owns; the route and version history remain.",
		Attributes: map[string]schema.Attribute{
			"id":                             schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, MarkdownDescription: "Route name."},
			"route_name":                     schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.RegexMatches(dynamicRouteNameRE, "must be a bare route name")}, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}, MarkdownDescription: "Route name, normally opper_dynamic_route.example.name. Changing it replaces the deployment resource."},
			"route_uuid":                     schema.StringAttribute{Required: true, Validators: projectIDValidators(), PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}, MarkdownDescription: "Exact route UUID, normally opper_dynamic_route.example.uuid. Prevents mutation of a recreated same-named route."},
			"draft_revision":                 schema.Int64Attribute{Required: true, Validators: []validator.Int64{int64validator.AtLeast(1)}, MarkdownDescription: "Exact draft revision to publish. Reference opper_dynamic_route.example.draft_revision to deploy new saved graphs."},
			"note":                           schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""), MarkdownDescription: "Release note. Changing it while deployed publishes a new immutable version."},
			"deployed":                       schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true), MarkdownDescription: "Whether this resource's owned version should be active. False never authorizes stopping another writer's version."},
			"operation_id":                   schema.StringAttribute{Computed: true, MarkdownDescription: "Operation nonce generated once at apply and retained for API receipt recovery. It is not a credential."},
			"operation_pending":              schema.BoolAttribute{Computed: true, MarkdownDescription: "True when an uncertain API outcome still needs receipt lookup or explicit import recovery. New mutations are blocked."},
			"version_number":                 schema.Int64Attribute{Computed: true, MarkdownDescription: "Last immutable version owned by this resource; retained while disabled."},
			"version_uuid":                   schema.StringAttribute{Computed: true, MarkdownDescription: "Exact immutable UUID of the owned version."},
			"deployed_draft_revision":        schema.Int64Attribute{Computed: true, MarkdownDescription: "Draft revision captured by the owned immutable version."},
			"deployed_graph_json":            schema.StringAttribute{Computed: true, CustomType: jsontypes.NormalizedType{}, MarkdownDescription: "Exact graph on the owned immutable version, rather than the current draft."},
			"observed_active_version_number": schema.Int64Attribute{Computed: true, MarkdownDescription: "Current route active version, including a version deployed by another writer. Zero means inactive."},
			"observed_active_version_uuid":   schema.StringAttribute{Computed: true, MarkdownDescription: "Current active version's exact UUID, or null when inactive."},
			"version_created_at":             schema.StringAttribute{Computed: true, MarkdownDescription: "Owned immutable version's creation timestamp."},
		},
	}
}

func (r *DynamicRouteDeploymentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected deployment configuration", "Expected an Opper API client.")
		return
	}
	r.client = c
}

func (r *DynamicRouteDeploymentResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var data, previous dynamicRouteDeploymentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if !req.State.Raw.IsNull() {
		resp.Diagnostics.Append(req.State.Get(ctx, &previous)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	if !req.State.Raw.IsNull() && previous.OperationPending.ValueBool() {
		resp.Diagnostics.AddError("Deployment operation needs recovery", "An earlier API outcome is unresolved. Refresh its receipt or explicitly import the confirmed active version before planning another mutation.")
		return
	}
	mutates := req.State.Raw.IsNull() || !data.Deployed.Equal(previous.Deployed) || (data.Deployed.ValueBool() && (!data.DraftRevision.Equal(previous.DraftRevision) || !data.Note.Equal(previous.Note)))
	if mutates {
		// Keep the nonce unknown during planning. Terraform may refine a plan
		// after upstream route outputs become known, and a new random known
		// value on that refinement would invalidate the saved plan.
		data.OperationID = types.StringUnknown()
	} else {
		data.OperationID = previous.OperationID
	}
	data.OperationPending = types.BoolValue(false)
	if !mutates && !req.State.Raw.IsNull() {
		data.copyObserved(previous)
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &data)...)
}

func initializeDeploymentOperation(data *dynamicRouteDeploymentModel) error {
	if !data.OperationID.IsNull() && !data.OperationID.IsUnknown() && data.OperationID.ValueString() != "" {
		return nil
	}
	id, err := uuid.GenerateUUID()
	if err != nil {
		return err
	}
	data.OperationID = types.StringValue(id)
	return nil
}

func (m *dynamicRouteDeploymentModel) copyObserved(previous dynamicRouteDeploymentModel) {
	m.VersionNumber, m.VersionUUID = previous.VersionNumber, previous.VersionUUID
	m.DeployedDraftRevision, m.DeployedGraphJSON = previous.DeployedDraftRevision, previous.DeployedGraphJSON
	m.VersionCreatedAt = previous.VersionCreatedAt
	m.ObservedActiveVersionNumber, m.ObservedActiveVersionUUID = previous.ObservedActiveVersionNumber, previous.ObservedActiveVersionUUID
}

func (m *dynamicRouteDeploymentModel) noOwnedVersion() {
	m.VersionNumber, m.DeployedDraftRevision = types.Int64Null(), types.Int64Null()
	m.VersionUUID, m.VersionCreatedAt = types.StringNull(), types.StringNull()
	m.DeployedGraphJSON = jsontypes.NewNormalizedNull()
}

func (m *dynamicRouteDeploymentModel) observe(d client.DynamicRouteDetail) {
	m.ID = types.StringValue(d.Route.Name)
	m.ObservedActiveVersionNumber = types.Int64Value(0)
	m.ObservedActiveVersionUUID = types.StringNull()
	if d.ActiveVersion != nil {
		m.ObservedActiveVersionNumber = types.Int64Value(d.ActiveVersion.VersionNumber)
		m.ObservedActiveVersionUUID = types.StringValue(d.ActiveVersion.UUID)
	}
	m.Deployed = types.BoolValue(!m.VersionUUID.IsNull() && m.VersionUUID.ValueString() != "" && m.ObservedActiveVersionUUID.Equal(m.VersionUUID) && m.ObservedActiveVersionNumber.Equal(m.VersionNumber))
}

func (m *dynamicRouteDeploymentModel) own(d client.DynamicRouteDetail, version client.DynamicRouteVersion) {
	m.VersionUUID, m.VersionNumber = types.StringValue(version.UUID), types.Int64Value(version.VersionNumber)
	m.DeployedDraftRevision = types.Int64Value(version.DraftRevision)
	m.DeployedGraphJSON = jsontypes.NewNormalizedValue(string(version.Graph))
	m.VersionCreatedAt = types.StringNull()
	if version.CreatedAt != "" {
		m.VersionCreatedAt = types.StringValue(version.CreatedAt)
	}
	m.OperationPending = types.BoolValue(false)
	m.observe(d)
}

func deploymentUncertain(err error) bool {
	var apiError *client.APIError
	return !errors.As(err, &apiError) || apiError.StatusCode < 400 || apiError.StatusCode >= 500
}

func (r *DynamicRouteDeploymentResource) deploy(ctx context.Context, data dynamicRouteDeploymentModel, expectedActive int64) (client.DynamicRouteDeploymentResult, error) {
	result, err := r.client.DeployDynamicRoute(ctx, data.RouteName.ValueString(), client.DynamicRouteDeployOptions{RouteUUID: data.RouteUUID.ValueString(), DraftRevision: data.DraftRevision.ValueInt64(), ActiveVersionNumber: expectedActive, IdempotencyKey: data.OperationID.ValueString(), Note: data.Note.ValueString()})
	if err != nil && deploymentUncertain(err) {
		if receipt, lookupErr := r.client.GetDynamicRouteDeploymentReceipt(ctx, data.RouteName.ValueString(), data.OperationID.ValueString()); lookupErr == nil {
			return receipt, nil
		}
	}
	return result, err
}

func (r *DynamicRouteDeploymentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data dynamicRouteDeploymentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := initializeDeploymentOperation(&data); err != nil {
		resp.Diagnostics.AddError("Unable to generate deployment operation ID", "Secure randomness is unavailable.")
		return
	}

	data.ID = data.RouteName
	data.noOwnedVersion()
	data.ObservedActiveVersionNumber, data.ObservedActiveVersionUUID = types.Int64Null(), types.StringNull()
	data.OperationPending = types.BoolValue(false)
	if !data.Deployed.ValueBool() {
		d, err := r.client.GetDynamicRoute(ctx, data.RouteName.ValueString())
		if err != nil {
			resp.State.RemoveResource(ctx)
			resp.Diagnostics.AddError("Unable to inspect deployment route", err.Error())
			return
		}
		if d.Route.UUID != data.RouteUUID.ValueString() || d.ActiveVersion != nil {
			resp.State.RemoveResource(ctx)
			resp.Diagnostics.AddError("Existing route deployment needs import", "The route identity changed or is already active. Import its exact active version before managing it.")
			return
		}
		data.observe(d)
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		return
	}
	result, err := r.deploy(ctx, data, 0)
	if err != nil {
		if deploymentUncertain(err) {
			data.OperationPending, data.Deployed = types.BoolValue(true), types.BoolValue(false)
			resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		} else {
			resp.State.RemoveResource(ctx)
		}
		resp.Diagnostics.AddError("Unable to deploy dynamic route", err.Error()+" Initial creation requires an inactive route, the exact route UUID and the intended current draft revision. Import an existing active version instead of adopting it.")
		return
	}
	if result.Detail.Route.UUID != data.RouteUUID.ValueString() || result.DeployedVersion == nil || result.DeployedVersion.DraftRevision != data.DraftRevision.ValueInt64() {
		data.OperationPending, data.Deployed = types.BoolValue(true), types.BoolValue(false)
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		resp.Diagnostics.AddError("Invalid deployment receipt", "The receipt does not identify the requested route and draft revision. Its operation ID is retained for recovery.")
		return
	}
	data.own(result.Detail, *result.DeployedVersion)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if !data.Deployed.ValueBool() {
		resp.Diagnostics.AddError("Deployment changed concurrently", "The exact created version was recovered, but it is no longer active. Its identity is retained; inspect the current writer before importing or reconciling.")
	}
}

func (r *DynamicRouteDeploymentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data dynamicRouteDeploymentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	d, err := r.client.GetDynamicRoute(ctx, data.RouteName.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read dynamic route deployment", err.Error())
		return
	}
	if d.Route.UUID != data.RouteUUID.ValueString() {
		resp.Diagnostics.AddError("Dynamic route identity changed", "A different route now uses this name. State is preserved; no deployment or undeployment is authorized for the replacement route.")
		return
	}
	if data.OperationPending.ValueBool() {
		// Undeploy receipts have their own backend namespace. A pure route
		// read is sufficient to confirm that our version is inactive.
		if !data.Deployed.ValueBool() && !data.VersionNumber.IsNull() {
			data.observe(d)
			if d.ActiveVersion == nil {
				data.OperationPending = types.BoolValue(false)
				resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
				return
			}
			if d.ActiveVersion.UUID != data.VersionUUID.ValueString() {
				data.OperationPending = types.BoolValue(false)
				resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
				resp.Diagnostics.AddError("Route changed after uncertain undeployment", "Another writer owns the active version. The old owned version is inactive; no additional undeploy was sent. Review a new plan.")
				return
			}
			resp.Diagnostics.AddError("Undeployment outcome is unresolved", "The owned version is still active. Import its confirmed active version to authorize a new plan; no mutation was sent during refresh.")
			return
		}
		result, err := r.client.GetDynamicRouteDeploymentReceipt(ctx, data.RouteName.ValueString(), data.OperationID.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Deployment receipt is unresolved", err.Error()+" New mutations are blocked. Inspect the operation and import the confirmed active version before retrying.")
			return
		}
		if result.Detail.Route.UUID != data.RouteUUID.ValueString() {
			resp.Diagnostics.AddError("Deployment receipt route mismatch", "The receipt belongs to another route identity; state is preserved.")
			return
		}
		if data.VersionNumber.IsNull() && result.DeployedVersion != nil {
			// Terraform taints a failed Create even when its identity was saved.
			// Automatically clearing pending here could make a retry destroy and
			// recreate that recovered live deployment. Require explicit import.
			data.observe(result.Detail)
			resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
			resp.Diagnostics.AddError("Recovered deployment needs explicit import", fmt.Sprintf("Operation receipt confirms version %d. After backing up state, remove only this deployment address with terraform state rm, then import %s/%d to recover it without a tainted destroy/create cycle. Do not reapply before recovery; no mutation was sent during refresh.", result.DeployedVersion.VersionNumber, data.RouteName.ValueString(), result.DeployedVersion.VersionNumber))
			return
		}
		if result.DeployedVersion != nil {
			data.own(result.Detail, *result.DeployedVersion)
		} else {
			data.OperationPending = types.BoolValue(false)
			data.observe(result.Detail)
		}
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		if result.Detail.ActiveVersion != nil && !data.Deployed.ValueBool() {
			resp.Diagnostics.AddError("Deployment changed concurrently", "The operation receipt was recovered, but another immutable version is active. Review a fresh plan before reconciling.")
		}
		return
	}
	if !data.VersionNumber.IsNull() {
		version, err := r.client.GetDynamicRouteVersion(ctx, data.RouteName.ValueString(), data.VersionNumber.ValueInt64())
		if err != nil {
			resp.Diagnostics.AddError("Unable to read owned route version", err.Error())
			return
		}
		if version.UUID != data.VersionUUID.ValueString() {
			resp.Diagnostics.AddError("Owned deployment identity changed", "The immutable version UUID does not match state. No mutation is authorized.")
			return
		}
		data.own(d, version)
	} else {
		data.observe(d)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *DynamicRouteDeploymentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data, previous dynamicRouteDeploymentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &previous)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := initializeDeploymentOperation(&data); err != nil {
		resp.Diagnostics.AddError("Unable to generate deployment operation ID", "Secure randomness is unavailable.")
		return
	}

	data.copyObserved(previous)
	data.OperationPending = types.BoolValue(false)
	if data.Deployed.ValueBool() {
		if previous.Deployed.ValueBool() && data.DraftRevision.Equal(previous.DraftRevision) && data.Note.Equal(previous.Note) {
			resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
			return
		}
		result, err := r.deploy(ctx, data, previous.ObservedActiveVersionNumber.ValueInt64())
		if err != nil {
			if deploymentUncertain(err) {
				data.OperationPending = types.BoolValue(true)
				resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
			} else {
				resp.Diagnostics.Append(resp.State.Set(ctx, &previous)...)
			}
			resp.Diagnostics.AddError("Unable to update live route", err.Error()+" The route UUID, draft revision or active version may have changed since the plan. Review a new plan; the provider does not retry deployment POSTs.")
			return
		}
		if result.Detail.Route.UUID != data.RouteUUID.ValueString() || result.DeployedVersion == nil || result.DeployedVersion.DraftRevision != data.DraftRevision.ValueInt64() {
			data.OperationPending = types.BoolValue(true)
			resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
			resp.Diagnostics.AddError("Invalid deployment receipt", "The receipt does not match the requested route and draft. Its operation ID is retained.")
			return
		}
		data.own(result.Detail, *result.DeployedVersion)
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		if !data.Deployed.ValueBool() {
			resp.Diagnostics.AddError("Deployment changed concurrently", "The recorded version is no longer active. Another writer changed the active pointer; review a fresh plan.")
		}
		return
	}
	if !previous.Deployed.ValueBool() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		return
	}
	result, err := r.client.UndeployDynamicRoute(ctx, data.RouteName.ValueString(), data.RouteUUID.ValueString(), previous.VersionNumber.ValueInt64(), data.OperationID.ValueString())
	if err != nil && deploymentUncertain(err) {
		if current, lookupErr := r.client.GetDynamicRoute(ctx, data.RouteName.ValueString()); lookupErr == nil && current.Route.UUID == data.RouteUUID.ValueString() && current.ActiveVersion == nil {
			number := previous.VersionNumber.ValueInt64()
			result, err = client.DynamicRouteDeploymentResult{Detail: current, UndeployedVersionNumber: &number}, nil
		}
	}
	if err != nil {
		if deploymentUncertain(err) {
			data.OperationPending = types.BoolValue(true)
			resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		} else {
			resp.Diagnostics.Append(resp.State.Set(ctx, &previous)...)
		}
		resp.Diagnostics.AddError("Unable to undeploy owned route version", err.Error()+" The provider refuses to stop a different writer's version.")
		return
	}
	if result.Detail.Route.UUID != data.RouteUUID.ValueString() || result.UndeployedVersionNumber == nil || *result.UndeployedVersionNumber != previous.VersionNumber.ValueInt64() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &previous)...)
		resp.Diagnostics.AddError("Invalid undeployment receipt", "The receipt does not identify the owned version. State is preserved.")
		return
	}
	data.observe(result.Detail)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if result.Detail.ActiveVersion != nil {
		resp.Diagnostics.AddError("Route changed after undeployment", "Another writer now owns the active version. No additional undeploy was sent.")
	}
}

func (r *DynamicRouteDeploymentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data dynamicRouteDeploymentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if data.OperationPending.ValueBool() {
		resp.Diagnostics.AddError("Deployment operation needs recovery", "Resolve the pending receipt or explicitly import the confirmed version before destroying. An unknown deployment must not be stopped automatically.")
		return
	}
	d, err := r.client.GetDynamicRoute(ctx, data.RouteName.ValueString())
	if client.IsNotFound(err) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to inspect route before undeployment", err.Error())
		return
	}
	if d.Route.UUID != data.RouteUUID.ValueString() {
		resp.Diagnostics.AddError("Route identity changed", "Refusing to undeploy a replacement route with the same name.")
		return
	}
	if d.ActiveVersion == nil {
		return
	}
	if data.VersionNumber.IsNull() || d.ActiveVersion.VersionNumber != data.VersionNumber.ValueInt64() || d.ActiveVersion.UUID != data.VersionUUID.ValueString() {
		resp.Diagnostics.AddError("Another writer owns the active route version", "Destroy refuses to undeploy a version this resource does not own. Reconcile or explicitly import the intended active version first.")
		return
	}
	// Destroy has no planned new state. Derive its receipt from the immutable
	// owned identity so repeating an uncertain destroy uses the same key.
	token := fmt.Sprintf("tf-undeploy-%x", sha256.Sum256([]byte(data.RouteUUID.ValueString()+"/"+data.VersionUUID.ValueString())))
	result, err := r.client.UndeployDynamicRoute(ctx, data.RouteName.ValueString(), data.RouteUUID.ValueString(), data.VersionNumber.ValueInt64(), token)
	if err != nil && deploymentUncertain(err) {
		if current, lookupErr := r.client.GetDynamicRoute(ctx, data.RouteName.ValueString()); lookupErr == nil && current.Route.UUID == data.RouteUUID.ValueString() && current.ActiveVersion == nil {
			number := data.VersionNumber.ValueInt64()
			result, err = client.DynamicRouteDeploymentResult{Detail: current, UndeployedVersionNumber: &number}, nil
		}
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to undeploy owned route version", err.Error()+" A concurrent replacement is never undeployed.")
		return
	}
	if result.Detail.Route.UUID != data.RouteUUID.ValueString() || result.UndeployedVersionNumber == nil || *result.UndeployedVersionNumber != data.VersionNumber.ValueInt64() || result.Detail.ActiveVersion != nil {
		resp.Diagnostics.AddError("Route changed during undeployment", "The receipt does not confirm an inactive route after undeploying the owned version. State is preserved; inspect the current writer.")
	}
}

func (r *DynamicRouteDeploymentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 2 || !dynamicRouteNameRE.MatchString(parts[0]) {
		resp.Diagnostics.AddError("Invalid deployment import ID", "Use <route_name>/<active_version_number>.")
		return
	}
	number, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || number < 1 || strconv.FormatInt(number, 10) != parts[1] {
		resp.Diagnostics.AddError("Invalid deployment import ID", "Use <route_name>/<positive_active_version_number>.")
		return
	}
	d, err := r.client.GetDynamicRoute(ctx, parts[0])
	if err != nil {
		resp.Diagnostics.AddError("Unable to inspect imported deployment", err.Error())
		return
	}
	version, err := r.client.GetDynamicRouteVersion(ctx, parts[0], number)
	if err != nil {
		resp.Diagnostics.AddError("Unable to inspect imported version", err.Error())
		return
	}
	if d.ActiveVersion == nil || d.ActiveVersion.VersionNumber != number || d.ActiveVersion.UUID != version.UUID {
		resp.Diagnostics.AddError("Import requires the exact active version", "The requested immutable version is not currently active. Inspect the route and import its active version explicitly.")
		return
	}
	id, err := uuid.GenerateUUID()
	if err != nil {
		resp.Diagnostics.AddError("Unable to initialize deployment import", "Secure randomness is unavailable.")
		return
	}
	data := dynamicRouteDeploymentModel{ID: types.StringValue(parts[0]), RouteName: types.StringValue(parts[0]), RouteUUID: types.StringValue(d.Route.UUID), DraftRevision: types.Int64Value(version.DraftRevision), Note: types.StringValue(version.Note), OperationID: types.StringValue(id), OperationPending: types.BoolValue(false)}
	data.own(d, version)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
