package provider

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

type CustomRoleResource struct{ client *client.Client }
type customRoleModel struct {
	ID                   types.String `tfsdk:"id"`
	OrganizationUUID     types.String `tfsdk:"organization_uuid"`
	Name                 types.String `tfsdk:"name"`
	DisplayName          types.String `tfsdk:"display_name"`
	Permissions          types.Set    `tfsdk:"permissions"`
	EffectivePermissions types.Set    `tfsdk:"effective_permissions"`
	Priority             types.Int64  `tfsdk:"priority"`
	CreationKey          types.String `tfsdk:"creation_idempotency_key"`
	ChangeToken          types.String `tfsdk:"change_token"`
	DefinitionChanged    types.Bool   `tfsdk:"definition_changed"`
	OperationID          types.String `tfsdk:"operation_id"`
	OperationKind        types.String `tfsdk:"operation_kind"`
	OperationPending     types.Bool   `tfsdk:"operation_pending"`
}

var customRoleNamePattern = regexp.MustCompile(`^custom_[0-9a-f]{32}$`)

type roleLabelValidator struct{}

func (roleLabelValidator) Description(context.Context) string {
	return "a canonical nonblank custom role label, distinct from built-in roles"
}
func (v roleLabelValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (roleLabelValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	s := req.ConfigValue.ValueString()
	invalid := strings.TrimSpace(s) != s || s == ""
	switch strings.ToLower(s) {
	case "owner", "admin", "developer", "viewer", "member":
		invalid = true
	}
	if invalid {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid custom role label", "Use a nonblank, trimmed display_name distinct from built-in role names.")
	}
}
func NewCustomRoleResource() resource.Resource { return &CustomRoleResource{} }
func (r *CustomRoleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_custom_role"
}
func (r *CustomRoleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	var ok bool
	r.client, ok = req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Invalid role provider configuration", "Expected an Opper Management API client.")
	}
}
func (r *CustomRoleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{MarkdownDescription: "Manage an Enterprise custom role with conditional updates and deletion. Built-ins are lookup-only. Reads never silently adopt outside changes; inspect and import before reconciling drift.", Attributes: map[string]schema.Attribute{
		"id":                       schema.StringAttribute{Computed: true},
		"organization_uuid":        schema.StringAttribute{Required: true, Validators: projectIDValidators(), PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
		"name":                     schema.StringAttribute{Computed: true, MarkdownDescription: "Immutable generated custom_<hex> role key, used for assignment and import."},
		"display_name":             schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.LengthBetween(1, 64), roleLabelValidator{}}},
		"permissions":              schema.SetAttribute{Required: true, ElementType: types.StringType, Validators: []validator.Set{setvalidator.SizeBetween(1, 50)}, MarkdownDescription: "Declared admin-ceiling grants. Owner-only and unknown permissions are refused by the API."},
		"effective_permissions":    schema.SetAttribute{Computed: true, ElementType: types.StringType},
		"priority":                 schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(150), Validators: []validator.Int64{int64validator.Between(1, 299)}},
		"creation_idempotency_key": schema.StringAttribute{Optional: true, Validators: []validator.String{stringvalidator.RegexMatches(regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`), "must be a bounded operation key")}, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}, MarkdownDescription: "Required for creation; fresh for every replacement. Imported roles can omit it."},
		"change_token":             schema.StringAttribute{Computed: true}, "definition_changed": schema.BoolAttribute{Computed: true}, "operation_id": schema.StringAttribute{Computed: true}, "operation_kind": schema.StringAttribute{Computed: true}, "operation_pending": schema.BoolAttribute{Computed: true},
	}}
}
func (m *customRoleModel) definition(ctx context.Context) (client.RoleCreateInput, error) {
	var grants []string
	d := m.Permissions.ElementsAs(ctx, &grants, false)
	if d.HasError() {
		return client.RoleCreateInput{}, roleRecoveryError()
	}
	sort.Strings(grants)
	return client.RoleCreateInput{DisplayName: m.DisplayName.ValueString(), Permissions: grants, Priority: m.Priority.ValueInt64()}, nil
}
func (m *customRoleModel) observe(ctx context.Context, v client.Role) {
	m.ID = types.StringValue(v.Name)
	m.Name = types.StringValue(v.Name)
	m.OrganizationUUID = types.StringValue(v.OrganizationUUID)
	m.DisplayName = types.StringPointerValue(v.DisplayName)
	m.Permissions, _ = types.SetValueFrom(ctx, types.StringType, v.Permissions)
	m.EffectivePermissions, _ = types.SetValueFrom(ctx, types.StringType, v.EffectivePermissions)
	m.Priority = types.Int64Value(v.Priority)
	m.ChangeToken = types.StringValue(v.ChangeToken)
}
func (m *customRoleModel) pending(kind, key, id string) {
	m.ID = types.StringValue(id)
	m.Name = types.StringNull()
	if customRoleNamePattern.MatchString(id) {
		m.Name = types.StringValue(id)
	}
	m.ChangeToken = types.StringNull()
	m.EffectivePermissions = types.SetNull(types.StringType)
	m.DefinitionChanged = types.BoolValue(false)
	m.OperationID = types.StringValue(key)
	m.OperationKind = types.StringValue(kind)
	m.OperationPending = types.BoolValue(true)
}
func (r *CustomRoleResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		if req.State.Raw.IsNull() {
			return
		}
		var old customRoleModel
		resp.Diagnostics.Append(req.State.Get(ctx, &old)...)
		if old.OperationPending.ValueBool() || old.DefinitionChanged.ValueBool() {
			resp.Diagnostics.AddError("Role deletion needs review", roleRecoveryError().Error())
		}
		return
	}
	var m customRoleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Validate the planned organization before Terraform can execute the
	// destructive half of replacement. An apply-time Create guard is too late
	// when organization_uuid changes together with a fresh creation key.
	if m.OrganizationUUID.IsUnknown() || m.OrganizationUUID.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("organization_uuid"), "Known role organization required", "The organization UUID must be known before planning creation or replacement.")
		return
	}
	if r.client != nil {
		catalog, err := r.client.GetRoleCatalog(ctx)
		if err != nil {
			resp.Diagnostics.AddError("Unable to verify planned role organization", err.Error())
			return
		}
		if len(catalog.Roles) == 0 || roleOrg(catalog.Roles[0].OrganizationUUID, m.OrganizationUUID.ValueString()) != nil {
			resp.Diagnostics.AddAttributeError(path.Root("organization_uuid"), "Role organization mismatch", "The management credential must identify the planned organization before creation or replacement. No existing role is deleted.")
			return
		}
	}
	if req.State.Raw.IsNull() {
		if m.CreationKey.IsNull() || m.CreationKey.IsUnknown() {
			resp.Diagnostics.AddAttributeError(path.Root("creation_idempotency_key"), "Fresh creation key required", "Configure a fresh known creation key. Import existing roles instead of recreating a completed operation.")
			return
		}
		if r.client != nil {
			if _, e := r.client.GetRoleOperation(ctx, "create", m.CreationKey.ValueString()); e == nil {
				resp.Diagnostics.AddError("Creation key already completed", "Use a fresh key for replacement, or inspect and import the original role.")
			} else if !client.IsNotFound(e) {
				resp.Diagnostics.AddError("Unable to inspect creation receipt", e.Error())
			}
		}
		return
	}
	var old customRoleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &old)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, e := loadRolePrivate(ctx, req.Private)
	if e != nil || p.AckToken == "" {
		resp.Diagnostics.AddError("Missing role baseline", roleRecoveryError().Error())
		return
	}
	if old.OperationPending.ValueBool() {
		resp.Diagnostics.AddError("Role operation needs recovery", roleRecoveryError().Error())
		return
	}
	m.ID, m.Name = old.ID, old.Name
	if m.DisplayName.IsUnknown() || m.Permissions.IsUnknown() || m.Priority.IsUnknown() {
		m.ChangeToken = types.StringUnknown()
		resp.Diagnostics.Append(resp.Plan.Set(ctx, &m)...)
		return
	}
	changed := !m.DisplayName.Equal(old.DisplayName) || !m.Permissions.Equal(old.Permissions) || !m.Priority.Equal(old.Priority)
	if !changed && !old.DefinitionChanged.ValueBool() {
		m.ChangeToken = old.ChangeToken
		m.EffectivePermissions = old.EffectivePermissions
		m.DefinitionChanged = old.DefinitionChanged
		m.OperationID = old.OperationID
		m.OperationKind = old.OperationKind
		m.OperationPending = old.OperationPending
		resp.Diagnostics.Append(resp.Plan.Set(ctx, &m)...)
		return
	}
	def, e := m.definition(ctx)
	if e != nil {
		resp.Diagnostics.AddError("Invalid role definition", e.Error())
		return
	}
	key := roleOperationKey("role-update", old.ID.ValueString(), p.AckToken, def)
	if old.DefinitionChanged.ValueBool() {
		x, e := r.client.GetRoleOperation(ctx, "update", key)
		if e != nil || !roleReceiptMatches(x, old.ID.ValueString()) {
			resp.Diagnostics.AddError("Role definition changed", roleRecoveryError().Error())
			return
		}
	}
	p.Intent = &roleIntent{Kind: "update", Key: key, ResourceID: old.ID.ValueString(), ExpectedToken: p.AckToken, Definition: &def}
	resp.Diagnostics.Append(saveRolePrivate(ctx, resp.Private, p)...)
	m.ChangeToken = types.StringUnknown()
	m.EffectivePermissions = types.SetUnknown(types.StringType)
	m.DefinitionChanged = types.BoolUnknown()
	m.OperationID = types.StringUnknown()
	m.OperationKind = types.StringValue("update")
	m.OperationPending = types.BoolValue(false)
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &m)...)
}
func (r *CustomRoleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m customRoleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	def, e := m.definition(ctx)
	if e != nil {
		resp.Diagnostics.AddError("Invalid role definition", e.Error())
		return
	}
	def.IdempotencyKey = m.CreationKey.ValueString()
	// Pin organization before sending a creation; a role read alone cannot
	// identify the organization when the role does not yet exist.
	catalog, e := r.client.GetRoleCatalog(ctx)
	if e != nil {
		resp.Diagnostics.AddError("Unable to verify role organization", e.Error())
		return
	}
	if len(catalog.Roles) == 0 || roleOrg(catalog.Roles[0].OrganizationUUID, m.OrganizationUUID.ValueString()) != nil {
		resp.Diagnostics.AddError("Role organization mismatch", "The role catalog must identify the configured organization before creation.")
		return
	}
	if _, e = r.client.GetRoleOperation(ctx, "create", def.IdempotencyKey); e == nil {
		resp.Diagnostics.AddError("Creation key already completed", "Inspect and import its original role; creation is not repeated.")
		return
	} else if !client.IsNotFound(e) {
		resp.Diagnostics.AddError("Unable to inspect creation receipt", e.Error())
		return
	}
	result, e := r.client.CreateRole(ctx, def)
	if e != nil {
		if !roleUncertain(e) {
			resp.Diagnostics.AddError("Unable to create custom role", e.Error())
			return
		}
		id := "pending-create:" + def.IdempotencyKey
		if x, re := r.client.GetRoleOperation(ctx, "create", def.IdempotencyKey); re == nil {
			id = x.ResourceID
		}
		m.pending("create", def.IdempotencyKey, id)
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
		resp.Diagnostics.AddError("Role creation needs recovery", "The original operation is retained. Inspect and import its original role; no POST is repeated.")
		return
	}
	v, e := confirmRole(ctx, r.client, m.OrganizationUUID.ValueString(), result.Role.Name, "create", def.IdempotencyKey, &def)
	if e != nil || result.Replayed {
		m.pending("create", def.IdempotencyKey, result.Role.Name)
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
		resp.Diagnostics.AddError("Role creation needs review", "Inspect and import the original role; its original result could not be safely adopted.")
		return
	}
	m.observe(ctx, v)
	m.DefinitionChanged = types.BoolValue(false)
	m.OperationPending = types.BoolValue(false)
	m.OperationID = types.StringValue(def.IdempotencyKey)
	m.OperationKind = types.StringValue("create")
	resp.Diagnostics.Append(saveRolePrivate(ctx, resp.Private, rolePrivate{AckToken: v.ChangeToken})...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *CustomRoleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m customRoleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if m.OperationPending.ValueBool() && m.OperationKind.ValueString() == "create" {
		resp.Diagnostics.AddError("Role creation needs explicit import", "Recover the original creation receipt, inspect the role and import its immutable name. No create or delete is repeated.")
		return
	}
	p, e := loadRolePrivate(ctx, req.Private)
	if e != nil {
		resp.Diagnostics.AddError("Unable to read role baseline", e.Error())
		return
	}
	if m.OperationPending.ValueBool() && p.Intent != nil {
		if v, e := confirmRole(ctx, r.client, m.OrganizationUUID.ValueString(), m.Name.ValueString(), "update", p.Intent.Key, p.Intent.Definition); e == nil {
			m.observe(ctx, v)
			m.DefinitionChanged = types.BoolValue(false)
			m.OperationPending = types.BoolValue(false)
			p.AckToken = v.ChangeToken
			p.Intent = nil
			resp.Diagnostics.Append(saveRolePrivate(ctx, resp.Private, p)...)
			resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
			return
		}
		resp.Diagnostics.AddError("Role operation needs recovery", roleRecoveryError().Error())
		return
	}
	v, e := r.client.GetRole(ctx, m.Name.ValueString())
	if client.IsNotFound(e) {
		catalog, verify := r.client.GetRoleCatalog(ctx)
		if verify != nil || len(catalog.Roles) == 0 || roleOrg(catalog.Roles[0].OrganizationUUID, m.OrganizationUUID.ValueString()) != nil {
			resp.Diagnostics.AddError("Unable to verify missing role organization", "The configured organization must be verified before removing an absent role from state.")
			return
		}
		resp.State.RemoveResource(ctx)
		return
	}
	if e == nil {
		e = roleOrg(v.OrganizationUUID, m.OrganizationUUID.ValueString())
	}
	if e != nil {
		resp.Diagnostics.AddError("Unable to read role", e.Error())
		return
	}
	m.observe(ctx, v)
	m.DefinitionChanged = types.BoolValue(v.ChangeToken != p.AckToken)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *CustomRoleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, old customRoleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &old)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &old)...)
	p, e := loadRolePrivate(ctx, req.Private)
	if e != nil || p.AckToken == "" || old.OperationPending.ValueBool() {
		resp.Diagnostics.AddError("Role update needs review", roleRecoveryError().Error())
		return
	}
	def, e := m.definition(ctx)
	if e != nil {
		resp.Diagnostics.AddError("Invalid role definition", e.Error())
		return
	}
	key := roleOperationKey("role-update", old.ID.ValueString(), p.AckToken, def)
	intent := roleIntent{Kind: "update", Key: key, ResourceID: old.Name.ValueString(), ExpectedToken: p.AckToken, Definition: &def}
	p.Intent = nil
	resp.Diagnostics.Append(saveRolePrivate(ctx, resp.Private, p)...)
	x, e := r.client.GetRoleOperation(ctx, "update", key)
	if e != nil && !client.IsNotFound(e) {
		resp.Diagnostics.AddError("Unable to inspect role operation", e.Error())
		return
	}
	if e != nil {
		if old.DefinitionChanged.ValueBool() {
			resp.Diagnostics.AddError("Role definition changed", roleRecoveryError().Error())
			return
		}
		_, e = r.client.UpdateRole(ctx, old.Name.ValueString(), client.RoleUpdateInput{DisplayName: def.DisplayName, Permissions: def.Permissions, Priority: def.Priority, ExpectedChangeToken: p.AckToken, IdempotencyKey: key})
		if e != nil && !roleUncertain(e) {
			resp.Diagnostics.AddError("Unable to update role", e.Error())
			return
		}
	} else if !roleReceiptMatches(x, old.Name.ValueString()) {
		e = roleRecoveryError()
	}
	v, proof := confirmRole(ctx, r.client, m.OrganizationUUID.ValueString(), old.Name.ValueString(), "update", key, &def)
	if proof != nil {
		p.Intent = &intent
		old.OperationID = types.StringValue(key)
		old.OperationKind = types.StringValue("update")
		old.OperationPending = types.BoolValue(true)
		resp.Diagnostics.Append(saveRolePrivate(ctx, resp.Private, p)...)
		resp.Diagnostics.Append(resp.State.Set(ctx, &old)...)
		resp.Diagnostics.AddError("Role update needs recovery", roleRecoveryError().Error())
		return
	}
	m.observe(ctx, v)
	m.DefinitionChanged = types.BoolValue(false)
	m.OperationPending = types.BoolValue(false)
	m.OperationID = types.StringValue(key)
	m.OperationKind = types.StringValue("update")
	p.AckToken = v.ChangeToken
	resp.Diagnostics.Append(saveRolePrivate(ctx, resp.Private, p)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *CustomRoleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m customRoleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, e := loadRolePrivate(ctx, req.Private)
	if e != nil || p.AckToken == "" || m.OperationPending.ValueBool() || m.DefinitionChanged.ValueBool() {
		resp.Diagnostics.AddError("Role deletion needs review", roleRecoveryError().Error())
		return
	}
	catalog, verify := r.client.GetRoleCatalog(ctx)
	if verify != nil || len(catalog.Roles) == 0 || roleOrg(catalog.Roles[0].OrganizationUUID, m.OrganizationUUID.ValueString()) != nil {
		resp.Diagnostics.AddError("Unable to verify role organization", "The configured organization must be verified before deletion.")
		return
	}
	v, e := r.client.GetRole(ctx, m.Name.ValueString())
	if e == nil {
		e = roleOrg(v.OrganizationUUID, m.OrganizationUUID.ValueString())
	}
	if client.IsNotFound(e) {
		return
	}
	if e != nil {
		resp.Diagnostics.AddError("Unable to verify role organization", e.Error())
		return
	}
	if e = r.client.DeleteRole(ctx, m.Name.ValueString(), p.AckToken); e != nil {
		resp.Diagnostics.AddError("Unable to delete role", e.Error()+" No refreshed token is used to force deletion.")
	}
}
func (r *CustomRoleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !customRoleNamePattern.MatchString(req.ID) {
		resp.Diagnostics.AddError("Invalid custom role import", "Import the immutable custom_<32 lowercase hex> role name. Built-ins are lookup-only.")
		return
	}
	v, e := r.client.GetRole(ctx, req.ID)
	if e != nil {
		resp.Diagnostics.AddError("Unable to import custom role", e.Error())
		return
	}
	var m customRoleModel
	m.observe(ctx, v)
	m.CreationKey = types.StringNull()
	m.DefinitionChanged = types.BoolValue(false)
	m.OperationPending = types.BoolValue(false)
	m.OperationID = types.StringNull()
	m.OperationKind = types.StringNull()
	resp.Diagnostics.Append(saveRolePrivate(ctx, resp.Private, rolePrivate{AckToken: v.ChangeToken})...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
