package provider

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/hashicorp/go-uuid"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

var (
	_ resource.Resource                   = (*RuleResource)(nil)
	_ resource.ResourceWithConfigure      = (*RuleResource)(nil)
	_ resource.ResourceWithImportState    = (*RuleResource)(nil)
	_ resource.ResourceWithValidateConfig = (*RuleResource)(nil)
)

type RuleResource struct{ client *client.Client }

type ruleModel struct {
	ID                  types.String         `tfsdk:"id"`
	Kind                types.String         `tfsdk:"kind"`
	Name                types.String         `tfsdk:"name"`
	Enabled             types.Bool           `tfsdk:"enabled"`
	Scope               types.Object         `tfsdk:"scope"`
	SchemaVersion       types.Int64          `tfsdk:"schema_version"`
	ConfigJSON          jsontypes.Normalized `tfsdk:"config_json"`
	EffectiveConfigJSON jsontypes.Normalized `tfsdk:"effective_config_json"`
	CreatedAt           types.String         `tfsdk:"created_at"`
	UpdatedAt           types.String         `tfsdk:"updated_at"`
}

func NewRuleResource() resource.Resource { return &RuleResource{} }
func (r *RuleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_rule"
}
func (r *RuleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage a Control Plane rule. Kind and scope are immutable; changing either replaces the rule. Config is a JSON object using the Management API's rule vocabulary. Writes require a plan that supports the selected rule.",
		Attributes: map[string]schema.Attribute{
			"id":                    schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, MarkdownDescription: "Client-generated rule UUID, also used for import."},
			"kind":                  schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.OneOf("guard", "observe", "route", "comply")}, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}, MarkdownDescription: "Rule kind. Changing it replaces the rule."},
			"name":                  schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""), MarkdownDescription: "Display name. Defaults to an empty string; removing the argument clears it."},
			"enabled":               schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), MarkdownDescription: "Whether the rule is enforced. Defaults to false, matching the API."},
			"scope":                 ruleScopeSchema(),
			"schema_version":        schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(1), Validators: []validator.Int64{int64validator.AtLeast(1)}, MarkdownDescription: "Configuration schema version. Defaults to 1; the server validates supported versions. Version meanings are part of the API contract."},
			"config_json":           schema.StringAttribute{Required: true, CustomType: jsontypes.NormalizedType{}, Validators: []validator.String{ruleConfigValidator{}}, MarkdownDescription: "Desired rule configuration as a JSON object, normally jsonencode({...}). Formatting and object key order are ignored when comparing API responses."},
			"effective_config_json": schema.StringAttribute{Computed: true, CustomType: jsontypes.NormalizedType{}, MarkdownDescription: "Canonical configuration returned by Opper, including server defaults and generated guard handles. Used to detect remote drift without copying defaults into config_json."},
			"created_at":            schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, MarkdownDescription: "Creation timestamp, when returned by Opper."},
			"updated_at":            schema.StringAttribute{Computed: true, MarkdownDescription: "Last update timestamp, when returned by Opper."},
		},
	}
}

func (r *RuleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected resource configuration", "Expected an Opper API client.")
		return
	}
	r.client = c
}

func (r *RuleResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data ruleModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	validateRuleScope(ctx, data, &resp.Diagnostics)
}

func (r *RuleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data ruleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := uuid.GenerateUUID()
	if err != nil {
		resp.Diagnostics.AddError("Unable to generate rule UUID", "Could not obtain secure randomness for the rule identity.")
		return
	}
	data.ID = types.StringValue(id)
	scope, diags := ruleScopeToWire(ctx, data.Scope)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	var name *string
	if data.Name.ValueString() != "" {
		value := data.Name.ValueString()
		name = &value
	}
	rule, err := r.client.CreateRule(ctx, client.Rule{ID: id, Kind: data.Kind.ValueString(), Name: name, Enabled: data.Enabled.ValueBool(), Scope: scope, SchemaVersion: data.SchemaVersion.ValueInt64(), Config: json.RawMessage(data.ConfigJSON.ValueString())})
	if err != nil {
		var apiErr *client.APIError
		uncertain := !errors.As(err, &apiErr) || apiErr.StatusCode >= 500
		if uncertain {
			// A read of this exact client-generated UUID can recover a committed
			// create whose response was lost. Never repeat the mutation blindly.
			if ctx.Err() == nil {
				if recovered, recoveryErr := r.client.GetRule(ctx, id); recoveryErr == nil {
					data.setRule(recovered, false)
					resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
					return
				}
			}
			data.CreatedAt = types.StringNull()
			data.UpdatedAt = types.StringNull()
			data.EffectiveConfigJSON = jsontypes.NewNormalizedNull()
			resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
			resp.Diagnostics.AddError("Unable to create rule", err.Error()+" The rule UUID is retained in state for reconciliation. Terraform marks failed creates as tainted; inspect the next plan and confirm the remote rule before accepting replacement or removing the taint.")
		} else {
			resp.Diagnostics.AddError("Unable to create rule", err.Error())
		}
		return
	}
	data.setRule(rule, false)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *RuleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data ruleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	rule, err := r.client.GetRule(ctx, data.ID.ValueString())
	if errors.Is(err, client.ErrRuleNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read rule", err.Error())
		return
	}
	remote := jsontypes.NewNormalizedValue(string(rule.Config))
	preserveDesired := !data.EffectiveConfigJSON.IsNull() && !data.EffectiveConfigJSON.IsUnknown() && sameRuleJSON(ctx, data.EffectiveConfigJSON, remote)
	data.setRule(rule, !preserveDesired)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *RuleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data, previous ruleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &previous)...)
	if resp.Diagnostics.HasError() {
		return
	}
	patch := client.RulePatch{}
	if !data.Name.Equal(previous.Name) {
		value := data.Name.ValueString()
		patch.Name = &value
	}
	if !data.Enabled.Equal(previous.Enabled) {
		value := data.Enabled.ValueBool()
		patch.Enabled = &value
	}
	if !data.SchemaVersion.Equal(previous.SchemaVersion) {
		value := data.SchemaVersion.ValueInt64()
		patch.SchemaVersion = &value
	}
	if !sameRuleJSON(ctx, data.ConfigJSON, previous.ConfigJSON) {
		patch.Config = json.RawMessage(data.ConfigJSON.ValueString())
	}
	if patch.Name == nil && patch.Enabled == nil && patch.SchemaVersion == nil && len(patch.Config) == 0 {
		data.EffectiveConfigJSON = previous.EffectiveConfigJSON
		data.CreatedAt = previous.CreatedAt
		data.UpdatedAt = previous.UpdatedAt
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		return
	}
	rule, err := r.client.UpdateRule(ctx, data.ID.ValueString(), patch)
	if err != nil {
		resp.Diagnostics.AddError("Unable to update rule", err.Error())
		return
	}
	if len(patch.Config) == 0 && !sameRuleJSON(ctx, previous.EffectiveConfigJSON, jsontypes.NewNormalizedValue(string(rule.Config))) {
		// We did not write config. Accepting a newly observed canonical snapshot
		// while keeping the old desired value would hide this concurrent change.
		data.setRule(rule, true)
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		resp.Diagnostics.AddError("Rule configuration changed during update", "Opper returned configuration that differs from the last refresh although this update did not write config. The metadata update completed and the observed policy is saved in state. Review the next plan to reconcile the policy before continuing.")
		return
	}
	data.setRule(rule, false)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *RuleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data ruleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteRule(ctx, data.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Unable to delete rule", err.Error())
	}
}

func (r *RuleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !uuidPattern.MatchString(req.ID) {
		resp.Diagnostics.AddError("Invalid rule import ID", "Import a rule using its UUID.")
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (m *ruleModel) setRule(rule client.Rule, replaceDesired bool) {
	m.ID = types.StringValue(rule.ID)
	m.Kind = types.StringValue(rule.Kind)
	m.Name = types.StringValue("")
	if rule.Name != nil {
		m.Name = types.StringValue(*rule.Name)
	}
	m.Enabled = types.BoolValue(rule.Enabled)
	m.SchemaVersion = types.Int64Value(rule.SchemaVersion)
	m.Scope = ruleScopeValue(rule.Scope)
	m.EffectiveConfigJSON = jsontypes.NewNormalizedValue(string(rule.Config))
	if replaceDesired || m.ConfigJSON.IsNull() || m.ConfigJSON.IsUnknown() {
		m.ConfigJSON = m.EffectiveConfigJSON
	}
	m.CreatedAt = types.StringNull()
	if rule.CreatedAt != "" {
		m.CreatedAt = types.StringValue(rule.CreatedAt)
	}
	m.UpdatedAt = types.StringNull()
	if rule.UpdatedAt != "" {
		m.UpdatedAt = types.StringValue(rule.UpdatedAt)
	}
}

func sameRuleJSON(ctx context.Context, a, b jsontypes.Normalized) bool {
	if a.IsNull() || a.IsUnknown() || b.IsNull() || b.IsUnknown() {
		return false
	}
	same, diags := a.StringSemanticEquals(ctx, b)
	return same && !diags.HasError()
}

type ruleConfigValidator struct{}

func (ruleConfigValidator) Description(context.Context) string {
	return "Configuration must be a JSON object."
}
func (v ruleConfigValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (v ruleConfigValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(req.ConfigValue.ValueString()), &object) != nil || object == nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid rule configuration", "config_json must contain a JSON object.")
	}
}
