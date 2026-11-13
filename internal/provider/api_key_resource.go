package provider

import (
	"context"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

var (
	_ resource.Resource                = (*APIKeyResource)(nil)
	_ resource.ResourceWithConfigure   = (*APIKeyResource)(nil)
	_ resource.ResourceWithImportState = (*APIKeyResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*APIKeyResource)(nil)
)

type APIKeyResource struct{ client *client.Client }

type apiKeyModel struct {
	ID              types.String `tfsdk:"id"`
	ProjectID       types.String `tfsdk:"project_id"`
	Name            types.String `tfsdk:"name"`
	IdempotencyKey  types.String `tfsdk:"idempotency_key"`
	Rotation        types.String `tfsdk:"rotation"`
	Kind            types.String `tfsdk:"kind"`
	Prefix          types.String `tfsdk:"prefix"`
	CreatedAt       types.String `tfsdk:"created_at"`
	Secret          types.String `tfsdk:"secret"`
	SecretAvailable types.Bool   `tfsdk:"secret_available"`
	AllowedIPs      types.Set    `tfsdk:"allowed_ips"`
}

func NewAPIKeyResource() resource.Resource { return &APIKeyResource{} }

func (r *APIKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_api_key"
}

func (r *APIKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage a project-owned runtime API key. The secret is returned once and stored in Terraform state. Import recovers metadata only. Revoking the key immediately stops applications using it.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Computed: true, MarkdownDescription: "Runtime key's numeric ID.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"project_id":  schema.StringAttribute{Required: true, Validators: projectIDValidators(), PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}, MarkdownDescription: "Project UUID. Changing it replaces the key."},
			"name":        schema.StringAttribute{Required: true, Validators: []validator.String{projectNameValidator{}}, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}, MarkdownDescription: "Runtime key name. Renaming replaces the key; only the IP allowlist can be updated in place."},
			"allowed_ips": apiKeyAllowedIPsSchema(),
			"idempotency_key": schema.StringAttribute{
				Optional: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stringvalidator.RegexMatches(regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`), "must contain 1 to 128 letters, digits, dots, underscores, colons or hyphens")},
				MarkdownDescription: "Optional API creation receipt. Reuse only when recovering the same create operation. Change it for every replacement; a deleted receipt cannot create another key. Replays never return the secret again.",
			},
			"rotation":         schema.StringAttribute{Optional: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}, MarkdownDescription: "Change this value to rotate the key. If idempotency_key is configured, also change it for the replacement."},
			"kind":             schema.StringAttribute{Computed: true, MarkdownDescription: "Always project. These keys outlive the member who created them."},
			"prefix":           schema.StringAttribute{Computed: true, MarkdownDescription: "Display prefix, not an authentication credential."},
			"created_at":       schema.StringAttribute{Computed: true, MarkdownDescription: "Creation timestamp, when provided by Opper."},
			"secret":           schema.StringAttribute{Computed: true, Sensitive: true, MarkdownDescription: "Full runtime credential. Stored in Terraform state, unavailable after import or idempotency replay. Secure the state backend."},
			"secret_available": schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether this Terraform state holds the full credential. False after import."},
		},
	}
}

func (r *APIKeyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *APIKeyResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var config, previous, planned apiKeyModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &previous)...)
	resp.Diagnostics.Append(req.Plan.Get(ctx, &planned)...)
	if resp.Diagnostics.HasError() {
		return
	}
	replacement := !previous.ProjectID.Equal(planned.ProjectID) || !previous.Name.Equal(planned.Name) || !previous.Rotation.Equal(planned.Rotation)
	if replacement || !previous.IdempotencyKey.Equal(planned.IdempotencyKey) {
		if !previous.AllowedIPs.IsNull() && !previous.AllowedIPs.IsUnknown() && len(previous.AllowedIPs.Elements()) > 0 {
			if _, err := apiKeyAllowedIPsInput(ctx, config.AllowedIPs); err != nil {
				resp.Diagnostics.AddAttributeError(path.Root("allowed_ips"), "Configure the replacement key's allowlist", "The current key has IP restrictions. Explicitly configure a known allowed_ips list to retain or change them before replacing the key, or [] to intentionally remove restrictions. Terraform does not reliably carry omitted computed values into replacements. No key has been revoked or created.")
			}
		}
	}
	if previous.IdempotencyKey.IsNull() || previous.IdempotencyKey.ValueString() == "" {
		return
	}
	if replacement && (planned.IdempotencyKey.IsUnknown() || previous.IdempotencyKey.Equal(planned.IdempotencyKey)) {
		resp.Diagnostics.AddAttributeError(path.Root("idempotency_key"), "Change the idempotency key", "Changing the key's project, name or rotation requires a fresh creation receipt. Set a new, known idempotency_key or remove it before replacement. Reusing the old receipt could revoke the existing key before replacement creation fails.")
	}
}

func (m *apiKeyModel) setKey(k client.APIKey) {
	m.ID = types.StringValue(strconv.FormatInt(k.ID, 10))
	m.Name = types.StringValue(k.Name)
	m.Kind = types.StringValue(k.Kind)
	m.Prefix = types.StringValue(k.Prefix)
	m.CreatedAt = types.StringNull()
	if k.CreatedAt != "" {
		m.CreatedAt = types.StringValue(k.CreatedAt)
	}
	// List/refresh never receives the credential. Preserve what creation saved;
	// imported resources have no recoverable secret and must keep it null.
	if m.Secret.IsUnknown() {
		m.Secret = types.StringNull()
	}
	m.SecretAvailable = types.BoolValue(!m.Secret.IsNull() && m.Secret.ValueString() != "")
}

func (r *APIKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data apiKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var ips []string
	if data.AllowedIPs.IsUnknown() {
		var config apiKeyModel
		resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
		if resp.Diagnostics.HasError() {
			return
		}
		if !config.AllowedIPs.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root("allowed_ips"), "Unknown runtime-key allowlist", "allowed_ips and all its entries must be known before creating a runtime key.")
			return
		}
	} else if !data.AllowedIPs.IsNull() {
		var err error
		ips, err = apiKeyAllowedIPsInput(ctx, data.AllowedIPs)
		if err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("allowed_ips"), "Invalid runtime-key allowlist", err.Error())
			return
		}
		if ips == nil {
			ips = []string{}
		}
	}
	var k client.APIKey
	var err error
	if ips == nil {
		k, err = r.client.CreateAPIKey(ctx, data.ProjectID.ValueString(), data.Name.ValueString(), data.IdempotencyKey.ValueString())
	} else {
		k, err = r.client.CreateAPIKeyWithAllowedIPs(ctx, data.ProjectID.ValueString(), data.Name.ValueString(), data.IdempotencyKey.ValueString(), ips)
	}
	if err != nil && k.ID <= 0 {
		resp.Diagnostics.AddError("Unable to create runtime API key", err.Error()+" If the request may have succeeded, inspect the project's key metadata before retrying. Import the exact key to manage it, or revoke it explicitly before creating a replacement. The secret is not retrievable after creation.")
		return
	}
	data.Secret = types.StringNull()
	if k.Key != "" {
		data.Secret = types.StringValue(k.Key)
	}
	data.setKey(k)
	if err != nil {
		// An incompatible create response can still identify a minted key. Keep
		// its credential and ID for explicit recovery without claiming protection.
		data.AllowedIPs = types.SetNull(types.StringType)
		if k.AllowedIPs != nil {
			_ = data.setAllowedIPs(ctx, k.AllowedIPs)
		}
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		resp.Diagnostics.AddError("Runtime API key allowlist was not confirmed", err.Error()+" The returned key identity and any one-time secret are saved in state. Inspect its restrictions before use. Import the exact key to adopt it, or review an explicit replacement with a fresh idempotency_key if one was configured; the failed creation may be tainted. No retry, replacement or automatic revocation was attempted.")
		return
	}
	if policyErr := data.setAllowedIPs(ctx, k.AllowedIPs); policyErr != nil {
		data.AllowedIPs = types.SetNull(types.StringType)
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		resp.Diagnostics.AddError("Unable to confirm runtime-key allowlist", policyErr.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if k.Key == "" {
		resp.Diagnostics.AddError("Runtime API key secret unavailable", "Opper returned key metadata without the one-time secret. This can happen when replaying a creation receipt. Its identity is saved in Terraform state. Review the key and set a new idempotency_key before replacing it; a replay cannot recover the secret.")
	}
}

func (r *APIKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data apiKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := strconv.ParseInt(data.ID.ValueString(), 10, 32)
	if err != nil || id <= 0 {
		resp.Diagnostics.AddError("Invalid runtime API key ID", "Expected a positive numeric key ID.")
		return
	}
	keys, err := r.client.ListAPIKeys(ctx, data.ProjectID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read runtime API keys", err.Error())
		return
	}
	for _, k := range keys {
		if k.ID != id {
			continue
		}
		if k.Kind != "project" {
			resp.Diagnostics.AddError("Unsupported runtime API key ownership", "This resource manages project-owned keys only. Personal keys cannot be imported or adopted.")
			return
		}
		data.setKey(k)
		if err := data.setAllowedIPs(ctx, k.AllowedIPs); err != nil {
			resp.Diagnostics.AddError("Unable to read runtime-key allowlist", err.Error())
			return
		}
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		return
	}
	resp.State.RemoveResource(ctx)
}

func (r *APIKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var planned, previous apiKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &planned)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &previous)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !previous.ProjectID.Equal(planned.ProjectID) || !previous.Name.Equal(planned.Name) || !previous.Rotation.Equal(planned.Rotation) || !previous.IdempotencyKey.Equal(planned.IdempotencyKey) {
		resp.Diagnostics.AddError("Unsupported runtime-key update", "Changing the project, name, rotation or creation receipt must replace the key. Only allowed_ips can change in place.")
		return
	}
	ips, err := apiKeyAllowedIPsInput(ctx, planned.AllowedIPs)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("allowed_ips"), "Invalid runtime-key allowlist", err.Error())
		return
	}
	id, err := strconv.ParseInt(previous.ID.ValueString(), 10, 32)
	if err != nil || id <= 0 {
		resp.Diagnostics.AddError("Invalid runtime API key ID", "Expected a positive numeric key ID.")
		return
	}
	data := previous // Preserve the one-time secret, including null after import.
	data.AllowedIPs = planned.AllowedIPs
	old, oldErr := apiKeyAllowedIPsInput(ctx, previous.AllowedIPs)
	if oldErr == nil && slices.Equal(old, ips) {
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		return
	}
	k, err := r.client.UpdateAPIKeyAllowedIPs(ctx, previous.ProjectID.ValueString(), id, ips)
	if err != nil {
		resp.Diagnostics.AddError("Unable to update runtime-key allowlist", err.Error()+" The existing key identity and secret are retained. No replacement or retry was attempted.")
		return
	}
	if k.Name != previous.Name.ValueString() || k.Prefix != previous.Prefix.ValueString() {
		resp.Diagnostics.AddError("Unexpected runtime API key response", "Opper returned different key metadata after an allowlist update. The existing key identity and secret are retained; inspect the current policy before retrying.")
		return
	}
	data.setKey(k)
	if err := data.setAllowedIPs(ctx, k.AllowedIPs); err != nil {
		resp.Diagnostics.AddError("Unable to confirm runtime-key allowlist", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *APIKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data apiKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := strconv.ParseInt(data.ID.ValueString(), 10, 32)
	if err != nil || id <= 0 {
		resp.Diagnostics.AddError("Invalid runtime API key ID", "Expected a positive numeric key ID.")
		return
	}
	if err := r.client.DeleteAPIKey(ctx, data.ProjectID.ValueString(), id); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Unable to revoke runtime API key", err.Error())
	}
}

func (r *APIKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 2 || !uuidPattern.MatchString(parts[0]) {
		resp.Diagnostics.AddError("Invalid API key import ID", "Use <project_uuid>/<key_id>.")
		return
	}
	id, err := strconv.ParseInt(parts[1], 10, 32)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != parts[1] {
		resp.Diagnostics.AddError("Invalid API key import ID", "Use <project_uuid>/<positive_numeric_key_id>.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("project_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}
