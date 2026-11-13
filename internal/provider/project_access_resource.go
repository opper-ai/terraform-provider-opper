package provider

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

var (
	_ resource.ResourceWithConfigure      = (*ProjectAccessResource)(nil)
	_ resource.ResourceWithModifyPlan     = (*ProjectAccessResource)(nil)
	_ resource.ResourceWithImportState    = (*ProjectAccessResource)(nil)
	_ resource.ResourceWithValidateConfig = (*ProjectAccessResource)(nil)
)

// ProjectAccessResource owns a policy on an existing project, never the project
// itself. Forgetting this resource cannot broaden access or delete its host.
type ProjectAccessResource struct{ client *client.Client }

type projectAccessModel struct {
	ID                  types.String `tfsdk:"id"`
	OrganizationUUID    types.String `tfsdk:"organization_uuid"`
	ProjectUUID         types.String `tfsdk:"project_uuid"`
	Mode                types.String `tfsdk:"mode"`
	RoleKeys            types.Set    `tfsdk:"role_keys"`
	UserUUIDs           types.Set    `tfsdk:"user_uuids"`
	Revision            types.Int64  `tfsdk:"revision"`
	ChangeToken         types.String `tfsdk:"change_token"`
	ReviewedPolicyToken types.String `tfsdk:"reviewed_policy_token"`
}

func NewProjectAccessResource() resource.Resource { return &ProjectAccessResource{} }

func (r *ProjectAccessResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project_access"
}

func (r *ProjectAccessResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	identity := func(description string) schema.StringAttribute {
		return schema.StringAttribute{Required: true, MarkdownDescription: description,
			Validators: append(projectIDValidators(), stringvalidator.NoneOf("00000000-0000-0000-0000-000000000000"))}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage access on an existing project using reviewed conditional saves. Requires project_access:read for planning, refresh and import, and project_access:write for mutations. Import already Restricted projects first. Destroy retains the remote policy. Project creation is separate and this resource cannot make a new project private from creation.",
		Attributes: map[string]schema.Attribute{
			"id":                schema.StringAttribute{Computed: true, MarkdownDescription: "Existing project UUID, also used for import."},
			"organization_uuid": identity("Credential organization UUID. Must be known during planning, match the policy, and cannot change after adoption. Use a separate resource address for another policy."),
			"project_uuid":      identity("Existing project UUID. Must be known during planning and cannot change after adoption. Use a separate resource address for another policy."),
			"mode":              schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.OneOf("organization", "restricted")}},
			"role_keys": schema.SetAttribute{Required: true, ElementType: types.StringType,
				Validators:          []validator.Set{setvalidator.SizeAtMost(500), setvalidator.ValueStringsAre(stringvalidator.RegexMatches(regexp.MustCompile(`^(admin|developer|viewer|custom_[0-9a-f]{32})$`), "must identify a supported built-in or organization custom role"))},
				MarkdownDescription: "Explicit role grants. Use an empty set for none. Role and member grants are additive; built-in Owners and Admins always retain access."},
			"user_uuids": schema.SetAttribute{Required: true, ElementType: types.StringType,
				Validators:          []validator.Set{setvalidator.SizeAtMost(500), setvalidator.ValueStringsAre(append(projectIDValidators(), stringvalidator.NoneOf("00000000-0000-0000-0000-000000000000"))...)},
				MarkdownDescription: "Explicit existing-member grants. Use an empty set for none. A grant does not add action permissions."},
			"revision":              schema.Int64Attribute{Computed: true, MarkdownDescription: "Current policy revision. Membership cascades can alter grants without increasing this value."},
			"change_token":          schema.StringAttribute{Computed: true, MarkdownDescription: "Current full-policy observation token, including recipient identities. Not an access credential."},
			"reviewed_policy_token": schema.StringAttribute{Computed: true, MarkdownDescription: "Original policy validator pinned by the adoption plan or import. Saved update plans pin their current baseline separately. Not an access credential."},
		},
	}
}

func (r *ProjectAccessResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	var ok bool
	r.client, ok = req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Invalid project access configuration", "Expected an Opper API client.")
	}
}

func (r *ProjectAccessResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m projectAccessModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() || m.Mode.IsUnknown() || m.Mode.IsNull() {
		return
	}
	if m.Mode.ValueString() == "organization" && ((!m.RoleKeys.IsUnknown() && len(m.RoleKeys.Elements()) > 0) || (!m.UserUUIDs.IsUnknown() && len(m.UserUUIDs.Elements()) > 0)) {
		resp.Diagnostics.AddError("Organization policy requires empty grants", "Set both role_keys and user_uuids to empty sets when mode is organization. This explicitly broadens project access.")
	}
}

func (m *projectAccessModel) observe(ctx context.Context, v client.ProjectAccessPolicy) diag.Diagnostics {
	m.ID = types.StringValue(v.ProjectUUID)
	m.OrganizationUUID = types.StringValue(v.OrganizationUUID)
	m.ProjectUUID = types.StringValue(v.ProjectUUID)
	m.Mode = types.StringValue(v.Mode)
	m.Revision = types.Int64Value(v.Revision)
	m.ChangeToken = types.StringValue(v.ChangeToken)
	var d, more diag.Diagnostics
	m.RoleKeys, d = types.SetValueFrom(ctx, types.StringType, v.RoleKeys)
	m.UserUUIDs, more = types.SetValueFrom(ctx, types.StringType, v.UserUUIDs)
	return append(d, more...)
}

func (m projectAccessModel) input(ctx context.Context, token string) (client.ProjectAccessInput, error) {
	roles, users := []string{}, []string{}
	if m.Mode.IsUnknown() || m.Mode.IsNull() || m.RoleKeys.IsUnknown() || m.RoleKeys.IsNull() || m.UserUUIDs.IsUnknown() || m.UserUUIDs.IsNull() {
		return client.ProjectAccessInput{}, errors.New("mode, role_keys and user_uuids must be fully known before applying the policy")
	}
	if m.RoleKeys.ElementsAs(ctx, &roles, false).HasError() || m.UserUUIDs.ElementsAs(ctx, &users, false).HasError() {
		return client.ProjectAccessInput{}, errors.New("project access grants must contain known string values")
	}
	slices.Sort(roles)
	slices.Sort(users)
	return client.ProjectAccessInput{Mode: m.Mode.ValueString(), RoleKeys: roles, UserUUIDs: users, ExpectedChangeToken: token}, nil
}

func projectAccessIdentity(v client.ProjectAccessPolicy, org, project string) error {
	if v.OrganizationUUID != org || v.ProjectUUID != project {
		return errors.New("the returned project or credential organization does not match the configured identity; no policy is adopted or modified")
	}
	return nil
}

func sameProjectAccess(v client.ProjectAccessPolicy, in client.ProjectAccessInput) bool {
	return v.Mode == in.Mode && slices.Equal(v.RoleKeys, in.RoleKeys) && slices.Equal(v.UserUUIDs, in.UserUUIDs)
}

const projectAccessPlanKey = "project_access_review_v1"

type projectAccessPlan struct{ Organization, Project, Token string }

func saveProjectAccessPlan(ctx context.Context, data customModelPrivateData, p projectAccessPlan) diag.Diagnostics {
	raw, err := json.Marshal(p)
	if err != nil {
		return diag.Diagnostics{diag.NewErrorDiagnostic("Unable to pin project policy", "Could not encode the reviewed policy baseline.")}
	}
	return data.SetKey(ctx, projectAccessPlanKey, raw)
}

func loadProjectAccessPlan(ctx context.Context, data customModelPrivateData) (projectAccessPlan, error) {
	var p projectAccessPlan
	if data == nil {
		return p, errors.New("the saved policy baseline is missing; create a fresh reviewed plan")
	}
	raw, d := data.GetKey(ctx, projectAccessPlanKey)
	if d.HasError() || len(raw) == 0 || json.Unmarshal(raw, &p) != nil || p.Token == "" {
		return p, errors.New("the saved policy baseline is missing or invalid; create a fresh reviewed plan")
	}
	return p, nil
}

func (r *ProjectAccessResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	} // Destroy always retains remote policy.
	var m, old projectAccessModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if m.OrganizationUUID.IsUnknown() || m.ProjectUUID.IsUnknown() {
		resp.Diagnostics.AddError("Known existing project identity required", "organization_uuid and project_uuid must be known during planning. Create a project separately and review a new plan; creating then restricting does not make it private from creation.")
		return
	}
	if r.client == nil {
		return
	}
	creating := req.State.Raw.IsNull()
	if !creating {
		resp.Diagnostics.Append(req.State.Get(ctx, &old)...)
		if resp.Diagnostics.HasError() {
			return
		}
		if !m.OrganizationUUID.Equal(old.OrganizationUUID) || !m.ProjectUUID.Equal(old.ProjectUUID) {
			resp.Diagnostics.AddError("Project access identity cannot change in place", "Use a separate resource address for the new existing project and import it first if Restricted. Keep the original policy managed until the new policy has been adopted successfully, then explicitly remove its old resource to retain the policy. Automatic replacement could forget the original before a failed destination read or save.")
			return
		}
	}
	v, err := r.client.GetProjectAccess(ctx, m.ProjectUUID.ValueString())
	if err == nil {
		err = projectAccessIdentity(v, m.OrganizationUUID.ValueString(), m.ProjectUUID.ValueString())
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to inspect project access", err.Error())
		return
	}
	if creating {
		if v.Mode != "organization" {
			resp.Diagnostics.AddError("Import Restricted project access first", "Inspect the existing Restricted policy and import its project UUID before changing it. No policy was modified.")
			return
		}
		// CreateRequest does not expose planned private data. Carry its original
		// nonsecret validator in computed plan state, as member-role adoption does.
		if !m.ReviewedPolicyToken.IsUnknown() && !m.ReviewedPolicyToken.IsNull() && m.ReviewedPolicyToken.ValueString() != v.ChangeToken {
			resp.Diagnostics.AddError("Reviewed project policy changed", "The policy changed after the adoption plan. Review a fresh plan or import the existing policy; no new token is substituted.")
			return
		}
		m.ReviewedPolicyToken = types.StringValue(v.ChangeToken)
	} else {
		if old.ChangeToken.ValueString() != v.ChangeToken {
			resp.Diagnostics.AddError("Project access changed during planning", "Refresh and review a new plan. The policy changed after refresh; no new token is substituted.")
			return
		}
		m.ReviewedPolicyToken = old.ReviewedPolicyToken
	}
	resp.Diagnostics.Append(saveProjectAccessPlan(ctx, resp.Private, projectAccessPlan{v.OrganizationUUID, v.ProjectUUID, v.ChangeToken})...)
	m.ID = types.StringValue(v.ProjectUUID)
	in, inputErr := m.input(ctx, v.ChangeToken)
	if inputErr == nil && sameProjectAccess(v, in) {
		m.Revision = types.Int64Value(v.Revision)
		m.ChangeToken = types.StringValue(v.ChangeToken)
	} else {
		m.Revision = types.Int64Unknown()
		m.ChangeToken = types.StringUnknown()
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &m)...)
}

// apply sends at most one PUT with the original planned token. A subsequent
// read could observe the desired state but cannot establish who wrote it.
func (r *ProjectAccessResource) apply(ctx context.Context, m projectAccessModel, p projectAccessPlan) (client.ProjectAccessPolicy, error) {
	if p.Organization != m.OrganizationUUID.ValueString() || p.Project != m.ProjectUUID.ValueString() || p.Token == "" {
		return client.ProjectAccessPolicy{}, errors.New("the saved policy identity or baseline does not match the planned resource; review a new plan")
	}
	in, err := m.input(ctx, p.Token)
	if err != nil {
		return client.ProjectAccessPolicy{}, err
	}
	v, err := r.client.GetProjectAccess(ctx, p.Project)
	if err == nil {
		err = projectAccessIdentity(v, p.Organization, p.Project)
	}
	if err != nil {
		return client.ProjectAccessPolicy{}, err
	}
	if v.ChangeToken != p.Token {
		return client.ProjectAccessPolicy{}, errors.New("the reviewed policy changed after planning; inspect it and create a fresh plan; no policy was modified")
	}
	if sameProjectAccess(v, in) {
		return v, nil
	}
	v, err = r.client.ReplaceProjectAccess(ctx, p.Project, in)
	if err != nil {
		return client.ProjectAccessPolicy{}, err
	}
	if err = projectAccessIdentity(v, p.Organization, p.Project); err != nil {
		return client.ProjectAccessPolicy{}, err
	}
	if !sameProjectAccess(v, in) {
		return client.ProjectAccessPolicy{}, errors.New("the saved response does not match the planned policy; inspect and import the current policy before retrying")
	}
	return v, nil
}

func (r *ProjectAccessResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m projectAccessModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p := projectAccessPlan{m.OrganizationUUID.ValueString(), m.ProjectUUID.ValueString(), m.ReviewedPolicyToken.ValueString()}
	v, err := r.apply(ctx, m, p)
	if err != nil {
		resp.State.RemoveResource(ctx)
		resp.Diagnostics.AddError("Unable to adopt project access", err.Error()+" A submitted save may have changed the policy. Inspect it and import its project UUID before retrying. No write is automatically retried.")
		return
	}
	resp.Diagnostics.Append(m.observe(ctx, v)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *ProjectAccessResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m projectAccessModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	v, err := r.client.GetProjectAccess(ctx, m.ProjectUUID.ValueString())
	if err == nil {
		err = projectAccessIdentity(v, m.OrganizationUUID.ValueString(), m.ProjectUUID.ValueString())
	}
	if err != nil {
		// This resource cannot recreate a missing host or distinguish a foreign
		// project from deletion. Preserve ownership rather than treating 404 as drift.
		resp.Diagnostics.AddError("Unable to read project access", err.Error()+" Existing Terraform state is retained. Resolve access or explicitly forget the policy if its project was deleted.")
		return
	}
	resp.Diagnostics.Append(m.observe(ctx, v)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *ProjectAccessResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, old projectAccessModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &old)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := loadProjectAccessPlan(ctx, req.Private)
	var v client.ProjectAccessPolicy
	if err == nil {
		v, err = r.apply(ctx, m, p)
	}
	if err != nil {
		resp.Diagnostics.Append(resp.State.Set(ctx, &old)...)
		resp.Diagnostics.AddError("Unable to update project access", err.Error()+" Inspect the current policy and review a new plan before another save. Existing Terraform state is retained and no write is automatically retried.")
		return
	}
	resp.Diagnostics.Append(m.observe(ctx, v)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *ProjectAccessResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
	// Retain the policy, including any role references. Broadening access must
	// be an explicit reviewed mode=organization update with empty grant lists.
}

func (r *ProjectAccessResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !uuidPattern.MatchString(req.ID) || req.ID == "00000000-0000-0000-0000-000000000000" {
		resp.Diagnostics.AddError("Invalid project access import ID", "Import using the existing project's canonical lowercase nonzero UUID.")
		return
	}
	v, err := r.client.GetProjectAccess(ctx, req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Unable to import project access", err.Error())
		return
	}
	var m projectAccessModel
	m.ReviewedPolicyToken = types.StringValue(v.ChangeToken)
	resp.Diagnostics.Append(m.observe(ctx, v)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
