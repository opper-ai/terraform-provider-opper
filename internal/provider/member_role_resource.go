package provider

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

type MemberRoleResource struct{ client *client.Client }
type memberRoleModel struct {
	SourceToken       types.String `tfsdk:"reviewed_assignment_token"`
	ID                types.String `tfsdk:"id"`
	OrganizationUUID  types.String `tfsdk:"organization_uuid"`
	MemberUUID        types.String `tfsdk:"member_uuid"`
	Role              types.String `tfsdk:"role"`
	RoleToken         types.String `tfsdk:"role_change_token"`
	DestroyBehavior   types.String `tfsdk:"destroy_behavior"`
	DestroyRole       types.String `tfsdk:"destroy_role"`
	DestroyToken      types.String `tfsdk:"destroy_role_change_token"`
	StoredRole        types.String `tfsdk:"stored_role"`
	CurrentRole       types.String `tfsdk:"current_role"`
	DirectoryManaged  types.Bool   `tfsdk:"directory_managed"`
	Owner             types.Bool   `tfsdk:"owner"`
	Blocked           types.Bool   `tfsdk:"blocked"`
	ChangeToken       types.String `tfsdk:"change_token"`
	DefinitionChanged types.Bool   `tfsdk:"definition_changed"`
	OperationID       types.String `tfsdk:"operation_id"`
	OperationKind     types.String `tfsdk:"operation_kind"`
	OperationPending  types.Bool   `tfsdk:"operation_pending"`
}

func NewMemberRoleResource() resource.Resource { return &MemberRoleResource{} }
func (r *MemberRoleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_member_role"
}
func (r *MemberRoleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	var ok bool
	r.client, ok = req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Invalid member role configuration", "Expected an Opper API client.")
	}
}
func (r *MemberRoleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{MarkdownDescription: "Manage an existing member's role with pinned role-definition and assignment preconditions. Never creates/removes memberships or transfers ownership. Explicitly choose whether destroy retains the role or assigns a pinned built-in fallback.", Attributes: map[string]schema.Attribute{
		"id":                        schema.StringAttribute{Computed: true},
		"reviewed_assignment_token": schema.StringAttribute{Computed: true, MarkdownDescription: "Original assignment validator pinned by the initial plan, before adoption. Not an access credential."},
		"organization_uuid":         schema.StringAttribute{Required: true, Validators: projectIDValidators(), PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
		"member_uuid":               schema.StringAttribute{Required: true, Validators: projectIDValidators(), PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
		"role":                      schema.StringAttribute{Required: true, MarkdownDescription: "admin, developer, viewer, or an organization's immutable custom role name. Owner cannot be assigned."},
		"role_change_token":         schema.StringAttribute{Required: true, MarkdownDescription: "Reviewed destination definition token from opper_role or opper_custom_role. No just-in-time token is substituted."},
		"destroy_behavior":          schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.OneOf("retain", "assign_role")}},
		"destroy_role":              schema.StringAttribute{Optional: true, Validators: []validator.String{stringvalidator.OneOf("admin", "developer", "viewer")}},
		"destroy_role_change_token": schema.StringAttribute{Optional: true, MarkdownDescription: "Pinned built-in fallback token; required only with destroy_behavior=assign_role."},
		"stored_role":               schema.StringAttribute{Computed: true}, "current_role": schema.StringAttribute{Computed: true}, "directory_managed": schema.BoolAttribute{Computed: true}, "owner": schema.BoolAttribute{Computed: true}, "blocked": schema.BoolAttribute{Computed: true}, "change_token": schema.StringAttribute{Computed: true}, "definition_changed": schema.BoolAttribute{Computed: true}, "operation_id": schema.StringAttribute{Computed: true}, "operation_kind": schema.StringAttribute{Computed: true}, "operation_pending": schema.BoolAttribute{Computed: true},
	}}
}
func (m *memberRoleModel) disposition() diag.Diagnostics {
	if m.DestroyBehavior.IsUnknown() {
		return nil
	}
	if m.DestroyBehavior.IsNull() {
		return diag.Diagnostics{diag.NewAttributeErrorDiagnostic(path.Root("destroy_behavior"), "Explicit destroy disposition required", "Configure retain or assign_role and apply before changing or destroying an imported assignment.")}
	}
	if m.DestroyBehavior.ValueString() == "assign_role" {
		if m.DestroyRole.IsNull() || m.DestroyToken.IsNull() {
			return diag.Diagnostics{diag.NewAttributeErrorDiagnostic(path.Root("destroy_role"), "Pinned fallback required", "assign_role requires destroy_role and destroy_role_change_token for an explicitly selected built-in role.")}
		}
	} else if !m.DestroyRole.IsNull() || !m.DestroyToken.IsNull() {
		return diag.Diagnostics{diag.NewAttributeErrorDiagnostic(path.Root("destroy_role"), "Fallback conflicts with retain", "Omit destroy_role and destroy_role_change_token when destroy_behavior is retain.")}
	}
	return nil
}
func (r *MemberRoleResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m memberRoleModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(m.disposition()...)
	}
}
func (m *memberRoleModel) observe(v client.MemberRole) {
	m.ID = types.StringValue(v.OrganizationUUID + "/" + v.MemberUUID)
	m.OrganizationUUID = types.StringValue(v.OrganizationUUID)
	m.MemberUUID = types.StringValue(v.MemberUUID)
	m.Role = types.StringValue(v.Role)
	m.CurrentRole = types.StringValue(v.Role)
	m.StoredRole = types.StringPointerValue(v.StoredRole)
	m.DirectoryManaged = types.BoolValue(v.DirectoryManaged)
	m.Owner = types.BoolValue(v.Owner)
	m.Blocked = types.BoolValue(v.Blocked)
	m.ChangeToken = types.StringValue(v.ChangeToken)
}
func (m *memberRoleModel) protected() bool {
	return m.Owner.ValueBool() || m.Blocked.ValueBool() || m.DirectoryManaged.ValueBool()
}
func (m *memberRoleModel) intent(token, kind string) roleIntent {
	role, proof := m.Role.ValueString(), m.RoleToken.ValueString()
	if kind == "destroy" {
		role, proof = m.DestroyRole.ValueString(), m.DestroyToken.ValueString()
	}
	key := roleOperationKey("member-"+kind, m.MemberUUID.ValueString(), token, struct{ Role, Token string }{role, proof})
	return roleIntent{Kind: kind, Key: key, ResourceID: m.MemberUUID.ValueString(), ExpectedToken: token, Role: role, RoleToken: proof}
}
func (m *memberRoleModel) setOperation(kind, key string, pending bool) {
	m.OperationKind = types.StringValue(kind)
	m.OperationID = types.StringValue(key)
	m.OperationPending = types.BoolValue(pending)
}
func (r *MemberRoleResource) verifyDestination(ctx context.Context, m memberRoleModel, i roleIntent) error {
	v, e := r.client.GetRole(ctx, i.Role)
	if e != nil {
		return e
	}
	if e = roleOrg(v.OrganizationUUID, m.OrganizationUUID.ValueString()); e != nil {
		return e
	}
	if v.ChangeToken != i.RoleToken {
		return roleRecoveryError()
	}
	return nil
}
func (r *MemberRoleResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		if req.State.Raw.IsNull() {
			return
		}
		var old memberRoleModel
		resp.Diagnostics.Append(req.State.Get(ctx, &old)...)
		if old.DestroyBehavior.ValueString() == "retain" {
			return
		}
		resp.Diagnostics.Append(old.disposition()...)
		if resp.Diagnostics.HasError() {
			return
		}
		p, e := loadRolePrivate(ctx, req.Private)
		if e != nil || p.AckToken == "" {
			resp.Diagnostics.AddError("Missing assignment baseline", roleRecoveryError().Error())
			return
		}
		if old.OperationPending.ValueBool() || old.DefinitionChanged.ValueBool() {
			i := old.intent(p.AckToken, "destroy")
			if x, e := r.client.GetMemberRoleOperation(ctx, i.Key); e != nil || !roleReceiptMatches(x, i.ResourceID) {
				resp.Diagnostics.AddError("Assignment deletion needs review", roleRecoveryError().Error())
			}
		}
		return
	}
	var m memberRoleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(m.disposition()...)
	if resp.Diagnostics.HasError() {
		return
	}
	if req.State.Raw.IsNull() {
		if m.MemberUUID.IsUnknown() || m.OrganizationUUID.IsUnknown() {
			resp.Diagnostics.AddError("Known member identity required", "The existing organization's member UUID must be known during planning so the original assignment can be reviewed and pinned.")
			return
		}
		if r.client == nil {
			return
		}
		v, e := r.client.GetMemberRole(ctx, m.MemberUUID.ValueString())
		if e == nil {
			e = roleOrg(v.OrganizationUUID, m.OrganizationUUID.ValueString())
		}
		if e != nil {
			resp.Diagnostics.AddError("Unable to inspect existing membership", e.Error())
			return
		}
		if !m.SourceToken.IsUnknown() && !m.SourceToken.IsNull() && m.SourceToken.ValueString() != v.ChangeToken {
			resp.Diagnostics.AddError("Reviewed assignment changed", "The membership changed after its initial plan. Replan after inspecting the existing member; no fresh validator is substituted.")
			return
		}
		desired := m.Role
		m.observe(v)
		m.Role = desired
		m.SourceToken = types.StringValue(v.ChangeToken)
		if v.Role != desired.ValueString() || desired.IsUnknown() {
			m.ChangeToken = types.StringUnknown()
			m.CurrentRole = types.StringUnknown()
			m.StoredRole = types.StringUnknown()
		}
		if m.protected() {
			resp.Diagnostics.AddError("Protected member assignment", "Owner, blocked and directory-managed assignments cannot be managed.")
			return
		}
		m.DefinitionChanged = types.BoolValue(false)
		m.OperationPending = types.BoolValue(false)
		m.OperationID = types.StringNull()
		m.OperationKind = types.StringNull()
		if v.Role != desired.ValueString() || desired.IsUnknown() {
			m.OperationID = types.StringUnknown()
			m.OperationKind = types.StringUnknown()
		}
		resp.Diagnostics.Append(resp.Plan.Set(ctx, &m)...)
		return
	}
	var old memberRoleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &old)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, e := loadRolePrivate(ctx, req.Private)
	if e != nil || p.AckToken == "" {
		resp.Diagnostics.AddError("Missing assignment baseline", roleRecoveryError().Error())
		return
	}
	if old.OperationPending.ValueBool() {
		resp.Diagnostics.AddError("Assignment needs recovery", roleRecoveryError().Error())
		return
	}
	m.ID = old.ID
	m.SourceToken = old.SourceToken
	// Changing only the explicit disposition does not touch a member. It also
	// provides the required adoption step after import's unknown disposition.
	sameAssignment := m.Role.Equal(old.Role) && m.RoleToken.Equal(old.RoleToken)
	if sameAssignment {
		m.ChangeToken = old.ChangeToken
		m.CurrentRole = old.CurrentRole
		m.StoredRole = old.StoredRole
		m.DirectoryManaged = old.DirectoryManaged
		m.Owner = old.Owner
		m.Blocked = old.Blocked
		m.DefinitionChanged = old.DefinitionChanged
		m.OperationID = old.OperationID
		m.OperationKind = old.OperationKind
		m.OperationPending = old.OperationPending
		resp.Diagnostics.Append(resp.Plan.Set(ctx, &m)...)
		return
	}
	if old.DefinitionChanged.ValueBool() {
		if m.Role.IsUnknown() || m.RoleToken.IsUnknown() {
			resp.Diagnostics.AddError("Assignment changed", roleRecoveryError().Error())
			return
		}
		i := m.intent(p.AckToken, "update")
		x, e := r.client.GetMemberRoleOperation(ctx, i.Key)
		if e != nil || !roleReceiptMatches(x, i.ResourceID) {
			resp.Diagnostics.AddError("Assignment changed", roleRecoveryError().Error())
			return
		}
	}
	if old.protected() {
		resp.Diagnostics.AddError("Protected member assignment", "Owner, blocked and directory-managed assignments cannot be changed.")
		return
	}
	if !m.Role.IsUnknown() && !m.RoleToken.IsUnknown() {
		i := m.intent(p.AckToken, "update")
		p.Intent = &i
		resp.Diagnostics.Append(saveRolePrivate(ctx, resp.Private, p)...)
	}
	m.ChangeToken = types.StringUnknown()
	m.CurrentRole = types.StringUnknown()
	m.StoredRole = types.StringUnknown()
	m.DirectoryManaged = old.DirectoryManaged
	m.Owner = old.Owner
	m.Blocked = old.Blocked
	m.DefinitionChanged = types.BoolUnknown()
	m.OperationID = types.StringUnknown()
	m.OperationKind = types.StringValue("update")
	m.OperationPending = types.BoolValue(false)
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &m)...)
}
func (r *MemberRoleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m memberRoleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(m.disposition()...)
	if resp.Diagnostics.HasError() {
		return
	}
	if m.SourceToken.IsNull() || m.SourceToken.IsUnknown() {
		resp.Diagnostics.AddError("Missing reviewed assignment", "Replan to pin the existing assignment before applying.")
		return
	}
	i := m.intent(m.SourceToken.ValueString(), "create")
	if e := r.verifyDestination(ctx, m, i); e != nil {
		resp.Diagnostics.AddError("Destination role changed", e.Error())
		return
	}
	v, e := r.client.GetMemberRole(ctx, m.MemberUUID.ValueString())
	if e == nil {
		e = roleOrg(v.OrganizationUUID, m.OrganizationUUID.ValueString())
	}
	if e != nil || v.ChangeToken != i.ExpectedToken {
		resp.Diagnostics.AddError("Assignment changed", roleRecoveryError().Error())
		return
	}
	if v.Owner || v.Blocked || v.DirectoryManaged {
		resp.Diagnostics.AddError("Protected member assignment", "Owner, blocked and directory-managed assignments cannot be managed.")
		return
	}
	if v.Role == i.Role {
		m.observe(v)
		m.DefinitionChanged = types.BoolValue(false)
		m.OperationPending = types.BoolValue(false)
		m.OperationID = types.StringNull()
		m.OperationKind = types.StringNull()
		resp.Diagnostics.Append(saveRolePrivate(ctx, resp.Private, rolePrivate{AckToken: v.ChangeToken})...)
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
		return
	}
	// New assignment adoption has no CreateRequest.Private. Its reviewed source
	// token is carried as a known computed plan value; the deterministic receipt
	// identity survives a process loss without minting a new command.
	x, e := r.client.GetMemberRoleOperation(ctx, i.Key)
	if e != nil && !client.IsNotFound(e) {
		resp.Diagnostics.AddError("Unable to inspect assignment receipt", e.Error())
		return
	}
	if client.IsNotFound(e) {
		_, e = r.client.SetMemberRole(ctx, i.ResourceID, client.MemberRoleInput{Role: i.Role, ExpectedChangeToken: i.ExpectedToken, ExpectedRoleChangeToken: i.RoleToken, IdempotencyKey: i.Key})
		if e != nil && !roleUncertain(e) {
			resp.Diagnostics.AddError("Unable to assign member role", e.Error())
			return
		}
	} else if !roleReceiptMatches(x, i.ResourceID) {
		e = roleRecoveryError()
	}
	confirmed, proof := confirmMemberRole(ctx, r.client, m.OrganizationUUID.ValueString(), i.ResourceID, i.Key, i.Role)
	if proof != nil {
		// Failed creates must still persist only known state. Retain the
		// inspected source snapshot rather than the plan's unknown result.
		m.observe(v)
		m.setOperation("create", i.Key, true)
		resp.Diagnostics.Append(saveRolePrivate(ctx, resp.Private, rolePrivate{AckToken: i.ExpectedToken, Intent: &i})...)
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
		resp.Diagnostics.AddError("Member role creation needs explicit import", "The original operation is retained. Inspect and import the membership; no PUT or fallback is repeated.")
		return
	}
	m.observe(confirmed)
	m.DefinitionChanged = types.BoolValue(false)
	m.setOperation("create", i.Key, false)
	resp.Diagnostics.Append(saveRolePrivate(ctx, resp.Private, rolePrivate{AckToken: confirmed.ChangeToken})...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *MemberRoleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m memberRoleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, e := loadRolePrivate(ctx, req.Private)
	if e != nil {
		resp.Diagnostics.AddError("Invalid assignment baseline", e.Error())
		return
	}
	if m.OperationPending.ValueBool() {
		if m.OperationKind.ValueString() == "create" {
			resp.Diagnostics.AddError("Assignment needs explicit import", "Inspect the original receipt and import the exact organization/member identity; an uncertain initial assignment is never healed into automatic deletion.")
			return
		}
		if p.Intent == nil {
			resp.Diagnostics.AddError("Assignment needs recovery", roleRecoveryError().Error())
			return
		}
		v, e := confirmMemberRole(ctx, r.client, m.OrganizationUUID.ValueString(), m.MemberUUID.ValueString(), p.Intent.Key, p.Intent.Role)
		if e != nil {
			resp.Diagnostics.AddError("Assignment needs recovery", e.Error())
			return
		}
		if p.Intent.Kind == "destroy" {
			resp.State.RemoveResource(ctx)
			return
		}
		m.observe(v)
		m.RoleToken = types.StringValue(p.Intent.RoleToken)
		m.DefinitionChanged = types.BoolValue(false)
		m.OperationPending = types.BoolValue(false)
		p.AckToken = v.ChangeToken
		p.Intent = nil
		resp.Diagnostics.Append(saveRolePrivate(ctx, resp.Private, p)...)
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
		return
	}
	v, e := r.client.GetMemberRole(ctx, m.MemberUUID.ValueString())
	if client.IsNotFound(e) {
		m.DefinitionChanged = types.BoolValue(true)
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
		resp.Diagnostics.AddWarning("Membership is absent", "The original membership is retained in state. Inspect/import a rejoined membership; retain destruction can relinquish management without changing a member.")
		return
	}
	if e == nil {
		e = roleOrg(v.OrganizationUUID, m.OrganizationUUID.ValueString())
	}
	if e != nil {
		resp.Diagnostics.AddError("Unable to read member role", e.Error())
		return
	}
	m.observe(v)
	m.DefinitionChanged = types.BoolValue(v.ChangeToken != p.AckToken)
	if m.DefinitionChanged.ValueBool() {
		resp.Diagnostics.AddWarning("Member assignment changed", "The observed assignment or admission facts differ from the confirmed baseline. Inspect and import before further assignment or fallback; retain can relinquish management without a write.")
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *MemberRoleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, old memberRoleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &old)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &old)...)
	resp.Diagnostics.Append(m.disposition()...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, e := loadRolePrivate(ctx, req.Private)
	if e != nil || p.AckToken == "" || old.OperationPending.ValueBool() {
		resp.Diagnostics.AddError("Assignment needs review", roleRecoveryError().Error())
		return
	}
	if m.Role.Equal(old.Role) && m.RoleToken.Equal(old.RoleToken) {
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
		return
	}
	i := m.intent(p.AckToken, "update")
	p.Intent = nil
	resp.Diagnostics.Append(saveRolePrivate(ctx, resp.Private, p)...)
	x, e := r.client.GetMemberRoleOperation(ctx, i.Key)
	if e != nil && !client.IsNotFound(e) {
		resp.Diagnostics.AddError("Unable to inspect assignment receipt", e.Error())
		return
	}
	if client.IsNotFound(e) {
		if old.DefinitionChanged.ValueBool() || old.protected() {
			resp.Diagnostics.AddError("Assignment changed", roleRecoveryError().Error())
			return
		}
		if e = r.verifyDestination(ctx, m, i); e != nil {
			resp.Diagnostics.AddError("Destination role changed", e.Error())
			return
		}
		_, e = r.client.SetMemberRole(ctx, i.ResourceID, client.MemberRoleInput{Role: i.Role, ExpectedChangeToken: i.ExpectedToken, ExpectedRoleChangeToken: i.RoleToken, IdempotencyKey: i.Key})
		if e != nil && !roleUncertain(e) {
			resp.Diagnostics.AddError("Unable to assign member role", e.Error())
			return
		}
	} else if !roleReceiptMatches(x, i.ResourceID) {
		e = roleRecoveryError()
	}
	v, proof := confirmMemberRole(ctx, r.client, m.OrganizationUUID.ValueString(), i.ResourceID, i.Key, i.Role)
	if proof != nil {
		p.Intent = &i
		old.setOperation("update", i.Key, true)
		resp.Diagnostics.Append(saveRolePrivate(ctx, resp.Private, p)...)
		resp.Diagnostics.Append(resp.State.Set(ctx, &old)...)
		resp.Diagnostics.AddError("Assignment needs recovery", roleRecoveryError().Error())
		return
	}
	m.observe(v)
	m.DefinitionChanged = types.BoolValue(false)
	m.setOperation("update", i.Key, false)
	p.AckToken = v.ChangeToken
	resp.Diagnostics.Append(saveRolePrivate(ctx, resp.Private, p)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *MemberRoleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m memberRoleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if m.DestroyBehavior.ValueString() == "retain" {
		return
	}
	resp.Diagnostics.Append(m.disposition()...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, e := loadRolePrivate(ctx, req.Private)
	if e != nil || p.AckToken == "" {
		resp.Diagnostics.AddError("Missing assignment baseline", roleRecoveryError().Error())
		return
	}
	i := m.intent(p.AckToken, "destroy")
	x, e := r.client.GetMemberRoleOperation(ctx, i.Key)
	if e != nil && !client.IsNotFound(e) {
		resp.Diagnostics.AddError("Unable to inspect fallback receipt", e.Error())
		return
	}
	if client.IsNotFound(e) {
		if m.DefinitionChanged.ValueBool() || m.OperationPending.ValueBool() || m.protected() {
			resp.Diagnostics.AddError("Fallback assignment needs review", roleRecoveryError().Error())
			return
		}
		if e = r.verifyDestination(ctx, m, i); e != nil {
			resp.Diagnostics.AddError("Fallback role changed", e.Error())
			return
		}
		_, e = r.client.SetMemberRole(ctx, i.ResourceID, client.MemberRoleInput{Role: i.Role, ExpectedChangeToken: i.ExpectedToken, ExpectedRoleChangeToken: i.RoleToken, IdempotencyKey: i.Key})
		if e != nil && !roleUncertain(e) {
			resp.Diagnostics.AddError("Unable to assign fallback role", e.Error())
			return
		}
	} else if !roleReceiptMatches(x, i.ResourceID) {
		e = roleRecoveryError()
	}
	if _, proof := confirmMemberRole(ctx, r.client, m.OrganizationUUID.ValueString(), i.ResourceID, i.Key, i.Role); proof != nil {
		p.Intent = &i
		m.setOperation("destroy", i.Key, true)
		resp.Diagnostics.Append(saveRolePrivate(ctx, resp.Private, p)...)
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
		resp.Diagnostics.AddError("Fallback assignment needs recovery", "The original receipt identity is retained; no PUT retry or refreshed token is used.")
	}
}
func (r *MemberRoleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 2 || !uuidPattern.MatchString(parts[0]) || !uuidPattern.MatchString(parts[1]) {
		resp.Diagnostics.AddError("Invalid member role import", "Use organization_uuid/member_uuid. Import never creates a member or infers a historical destroy role.")
		return
	}
	v, e := r.client.GetMemberRole(ctx, parts[1])
	if e == nil {
		e = roleOrg(v.OrganizationUUID, parts[0])
	}
	if e != nil {
		resp.Diagnostics.AddError("Unable to import member role", e.Error())
		return
	}
	var m memberRoleModel
	m.observe(v)
	m.SourceToken = types.StringValue(v.ChangeToken)
	if m.protected() {
		resp.Diagnostics.AddError("Protected member assignment", "Owner, blocked and directory-managed assignments cannot be imported for management.")
		return
	}
	dest, e := r.client.GetRole(ctx, v.Role)
	if e != nil {
		resp.Diagnostics.AddError("Unable to inspect imported role", e.Error())
		return
	}
	m.RoleToken = types.StringValue(dest.ChangeToken)
	m.DestroyBehavior = types.StringNull()
	m.DestroyRole = types.StringNull()
	m.DestroyToken = types.StringNull()
	m.DefinitionChanged = types.BoolValue(false)
	m.OperationPending = types.BoolValue(false)
	m.OperationID = types.StringNull()
	m.OperationKind = types.StringNull()
	resp.Diagnostics.Append(saveRolePrivate(ctx, resp.Private, rolePrivate{AckToken: v.ChangeToken})...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
