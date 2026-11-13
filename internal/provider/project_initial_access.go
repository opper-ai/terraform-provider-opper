package provider

import (
	"context"
	"errors"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

var (
	_ resource.ResourceWithModifyPlan     = (*ProjectResource)(nil)
	_ resource.ResourceWithValidateConfig = (*ProjectResource)(nil)
)

// The creation intent belongs only to the managed resource. A project lookup
// returns current metadata and cannot reconstruct the original access intent.
type projectResourceModel struct {
	ID            types.String `tfsdk:"id"`
	Name          types.String `tfsdk:"name"`
	Description   types.String `tfsdk:"description"`
	CreatedAt     types.String `tfsdk:"created_at"`
	InitialAccess types.Object `tfsdk:"initial_access"`
}

func (m *projectResourceModel) setProject(p client.Project) {
	var metadata projectModel
	metadata.setProject(p)
	m.ID, m.Name, m.Description, m.CreatedAt = metadata.ID, metadata.Name, metadata.Description, metadata.CreatedAt
}

type initialProjectAccessModel struct {
	Mode      types.String `tfsdk:"mode"`
	RoleKeys  types.Set    `tfsdk:"role_keys"`
	UserUUIDs types.Set    `tfsdk:"user_uuids"`
}

func initialProjectAccessSchema() schema.SingleNestedAttribute {
	empty := setdefault.StaticValue(types.SetValueMust(types.StringType, []attr.Value{}))
	return schema.SingleNestedAttribute{
		Optional:            true,
		MarkdownDescription: "Creation-only access intent, committed atomically with the project. Requires project_access:write in addition to projects:write and a backend supporting atomic creation. Stored as creation history; refresh and metadata updates never reconcile the current policy. Omit on import. Removing this argument retains the policy. Use an imported opper_project_access resource for later policy management; changing this creation intent never replaces the project.",
		Attributes: map[string]schema.Attribute{
			"mode": schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.OneOf("organization", "restricted")}},
			"role_keys": schema.SetAttribute{Optional: true, Computed: true, ElementType: types.StringType, Default: empty,
				Validators:          []validator.Set{setvalidator.SizeAtMost(500), setvalidator.ValueStringsAre(stringvalidator.RegexMatches(regexp.MustCompile(`^(admin|developer|viewer|custom_[0-9a-f]{32})$`), "must identify a supported built-in or organization custom role"))},
				MarkdownDescription: "Initial role grants. Defaults to an empty set. May reference custom roles created in the same apply. Owners and Admins retain access implicitly."},
			"user_uuids": schema.SetAttribute{Optional: true, Computed: true, ElementType: types.StringType, Default: empty,
				Validators:          []validator.Set{setvalidator.SizeAtMost(500), setvalidator.ValueStringsAre(append(projectIDValidators(), stringvalidator.NoneOf("00000000-0000-0000-0000-000000000000"))...)},
				MarkdownDescription: "Initial existing-member grants. Defaults to an empty set. Grants do not add action permissions."},
		},
	}
}

func initialProjectAccessInput(ctx context.Context, value types.Object) (client.InitialProjectAccess, error) {
	invalid := errors.New("initial_access and all recipient values must be known before project creation")
	if value.IsNull() || value.IsUnknown() {
		return client.InitialProjectAccess{}, invalid
	}
	var data initialProjectAccessModel
	if value.As(ctx, &data, basetypes.ObjectAsOptions{}).HasError() || data.Mode.IsNull() || data.Mode.IsUnknown() || data.RoleKeys.IsNull() || data.RoleKeys.IsUnknown() || data.UserUUIDs.IsNull() || data.UserUUIDs.IsUnknown() {
		return client.InitialProjectAccess{}, invalid
	}
	roles, users := []string{}, []string{}
	if data.RoleKeys.ElementsAs(ctx, &roles, false).HasError() || data.UserUUIDs.ElementsAs(ctx, &users, false).HasError() {
		return client.InitialProjectAccess{}, invalid
	}
	return client.InitialProjectAccess{Mode: data.Mode.ValueString(), RoleKeys: roles, UserUUIDs: users}, nil
}

func (r *ProjectResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data projectResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() || data.InitialAccess.IsNull() || data.InitialAccess.IsUnknown() {
		return
	}
	var initial initialProjectAccessModel
	resp.Diagnostics.Append(data.InitialAccess.As(ctx, &initial, basetypes.ObjectAsOptions{})...)
	if resp.Diagnostics.HasError() {
		return
	}
	if initial.Mode.ValueString() == "organization" && ((!initial.RoleKeys.IsUnknown() && len(initial.RoleKeys.Elements()) > 0) || (!initial.UserUUIDs.IsUnknown() && len(initial.UserUUIDs.Elements()) > 0)) {
		resp.Diagnostics.AddAttributeError(path.Root("initial_access"), "Organization policy requires empty grants", "Use empty role_keys and user_uuids when the initial mode is organization.")
	}
}

func initialProjectAccessChange(ctx context.Context, next, previous types.Object, deferUnknown bool) error {
	if next.IsNull() || next.Equal(previous) {
		return nil
	}
	if previous.IsNull() {
		return errors.New("initial_access can only be supplied when creating a project. It cannot be added to an existing or imported project; import its policy into opper_project_access instead")
	}
	if deferUnknown {
		raw, err := next.ToTerraformValue(ctx)
		if err == nil && !raw.IsFullyKnown() {
			return nil
		}
	}
	return errors.New("initial_access records the original creation intent and cannot be changed. It never replaces the project or reapplies policy. Remove the argument to forget this history while retaining access, and import the policy into opper_project_access for ongoing management")
}

func (r *ProjectResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || req.State.Raw.IsNull() {
		return
	}
	var next, previous projectResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &next)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &previous)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := initialProjectAccessChange(ctx, next.InitialAccess, previous.InitialAccess, true); err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("initial_access"), "Initial project access is creation-only", err.Error())
	}
}
