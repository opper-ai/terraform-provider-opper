package provider

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

type ruleScopeModel struct {
	Type          types.String `tfsdk:"type"`
	ProjectUUIDs  types.Set    `tfsdk:"project_uuids"`
	FunctionUUIDs types.Set    `tfsdk:"function_uuids"`
	RoleNames     types.Set    `tfsdk:"role_names"`
	MemberUUIDs   types.Set    `tfsdk:"member_uuids"`
}

func ruleScopeSchema() schema.SingleNestedAttribute {
	targetSet := func(uuid bool, description string) schema.SetAttribute {
		validators := []validator.String{stringvalidator.LengthAtLeast(1)}
		if uuid {
			validators = projectIDValidators()
		}
		return schema.SetAttribute{Optional: true, Computed: true, ElementType: types.StringType, Default: setdefault.StaticValue(types.SetValueMust(types.StringType, []attr.Value{})), Validators: []validator.Set{setvalidator.ValueStringsAre(validators...)}, MarkdownDescription: description}
	}
	return schema.SingleNestedAttribute{
		Required: true, PlanModifiers: []planmodifier.Object{objectplanmodifier.RequiresReplace()},
		MarkdownDescription: "Immutable rule scope. Exactly the target set matching type must be nonempty; org takes no targets. Replacing scope can create an enforcement gap unless create_before_destroy is configured and overlapping policies are valid.",
		Attributes: map[string]schema.Attribute{
			"type":           schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.OneOf("org", "projects", "functions", "roles", "members")}, MarkdownDescription: "Scope type."},
			"project_uuids":  targetSet(true, "Project UUIDs for projects scope."),
			"function_uuids": targetSet(true, "Function UUIDs for functions scope; supported rule kinds are validated by Opper."),
			"role_names":     targetSet(false, "Role names for comply budget rules with roles scope."),
			"member_uuids":   targetSet(true, "Member UUIDs for comply budget rules with members scope."),
		},
	}
}

func ruleScopeTypes() map[string]attr.Type {
	return map[string]attr.Type{"type": types.StringType, "project_uuids": types.SetType{ElemType: types.StringType}, "function_uuids": types.SetType{ElemType: types.StringType}, "role_names": types.SetType{ElemType: types.StringType}, "member_uuids": types.SetType{ElemType: types.StringType}}
}
func ruleScopeValue(scope client.RuleScope) types.Object {
	set := func(values []string) types.Set {
		out := make([]attr.Value, 0, len(values))
		for _, value := range values {
			out = append(out, types.StringValue(value))
		}
		return types.SetValueMust(types.StringType, out)
	}
	return types.ObjectValueMust(ruleScopeTypes(), map[string]attr.Value{"type": types.StringValue(scope.Type), "project_uuids": set(scope.ProjectUUIDs), "function_uuids": set(scope.FunctionUUIDs), "role_names": set(scope.RoleNames), "member_uuids": set(scope.MemberUUIDs)})
}
func ruleScopeToWire(ctx context.Context, value types.Object) (client.RuleScope, diag.Diagnostics) {
	var scope ruleScopeModel
	diags := value.As(ctx, &scope, basetypes.ObjectAsOptions{})
	out := client.RuleScope{Type: scope.Type.ValueString()}
	for _, pair := range []struct {
		set types.Set
		dst *[]string
	}{{scope.ProjectUUIDs, &out.ProjectUUIDs}, {scope.FunctionUUIDs, &out.FunctionUUIDs}, {scope.RoleNames, &out.RoleNames}, {scope.MemberUUIDs, &out.MemberUUIDs}} {
		diags.Append(pair.set.ElementsAs(ctx, pair.dst, false)...)
		sort.Strings(*pair.dst)
	}
	return out, diags
}
func validateRuleScope(ctx context.Context, data ruleModel, diags *diag.Diagnostics) {
	if data.Scope.IsNull() || data.Scope.IsUnknown() {
		return
	}
	var scope ruleScopeModel
	diags.Append(data.Scope.As(ctx, &scope, basetypes.ObjectAsOptions{})...)
	if diags.HasError() || scope.Type.IsUnknown() {
		return
	}
	sets := map[string]types.Set{"projects": scope.ProjectUUIDs, "functions": scope.FunctionUUIDs, "roles": scope.RoleNames, "members": scope.MemberUUIDs}
	scopeType := scope.Type.ValueString()
	for targetType, set := range sets {
		if set.IsUnknown() {
			continue
		}
		count := len(set.Elements())
		if targetType == scopeType && count == 0 {
			diags.AddAttributeError(path.Root("scope"), "Missing scope targets", "The target set matching scope.type must contain at least one target.")
		}
		if targetType != scopeType && count > 0 {
			diags.AddAttributeError(path.Root("scope"), "Unexpected scope targets", "Only the target set matching scope.type may contain targets; org scope has none.")
		}
	}
	if (scopeType == "roles" || scopeType == "members") && !data.Kind.IsUnknown() && !data.ConfigJSON.IsUnknown() {
		var config struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(data.ConfigJSON.ValueString()), &config) == nil && (data.Kind.ValueString() != "comply" || config.Type != "budget") {
			diags.AddAttributeError(path.Root("scope"), "Unsupported member scope", "roles and members scopes are only available for comply budget rules.")
		}
	}
}
