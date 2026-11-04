package provider

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

var (
	_ resource.Resource                = (*ProjectWebhookResource)(nil)
	_ resource.ResourceWithConfigure   = (*ProjectWebhookResource)(nil)
	_ resource.ResourceWithImportState = (*ProjectWebhookResource)(nil)
)

// Keep ownership separate from the public URL attribute: refresh must expose
// remote drift without granting destroy permission over the replacement URL.
const projectWebhookOwnedURLKey = "project_webhook_owned_url"

type ProjectWebhookResource struct{ client *client.Client }

type projectWebhookModel struct {
	ID                       types.String `tfsdk:"id"`
	ProjectUUID              types.String `tfsdk:"project_uuid"`
	URL                      types.String `tfsdk:"url"`
	URLUpdatedAt             types.String `tfsdk:"url_updated_at"`
	SecretCreatedAt          types.String `tfsdk:"secret_created_at"`
	PreviousSecretValidUntil types.String `tfsdk:"previous_secret_valid_until"`
}

func NewProjectWebhookResource() resource.Resource { return &ProjectWebhookResource{} }

func (r *ProjectWebhookResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project_webhook"
}

func (r *ProjectWebhookResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage a project's default completion webhook URL. Requires apikeys:read and apikeys:write. Saving a URL enables webhook delivery for async jobs without a request-specific callback URL and can initialize the project's signing secret. This resource never retrieves or rotates that secret or sends a test event. Destroy clears the owned URL and preserves the project and signing secret. The API has no conditional update; the destroy ownership check is best effort.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				MarkdownDescription: "Project UUID, also used for import.",
			},
			"project_uuid": schema.StringAttribute{
				Required: true, Validators: projectIDValidators(), PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				MarkdownDescription: "Project UUID. Changing it replaces the webhook resource.",
			},
			"url": schema.StringAttribute{
				Required: true, Sensitive: true, Validators: []validator.String{projectWebhookURLValidator{}},
				MarkdownDescription: "Default HTTPS callback URL. Sensitive because its path or query may contain tokens; its value is retained in Terraform state. Opper validates public host/IP and DNS requirements.",
			},
			"url_updated_at":              schema.StringAttribute{Computed: true, MarkdownDescription: "When the default URL was last set or cleared, if available."},
			"secret_created_at":           schema.StringAttribute{Computed: true, MarkdownDescription: "When the current signing secret was created, if one exists. Contains no secret value."},
			"previous_secret_valid_until": schema.StringAttribute{Computed: true, MarkdownDescription: "End of the previous signing secret's rotation overlap, if available. This resource does not rotate secrets."},
		},
	}
}

func (r *ProjectWebhookResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected webhook configuration", "Expected an Opper API client.")
		return
	}
	r.client = c
}

func (m *projectWebhookModel) setWebhook(w client.ProjectWebhook) {
	m.ID = m.ProjectUUID
	m.URL = types.StringPointerValue(w.URL)
	m.URLUpdatedAt = types.StringPointerValue(w.URLUpdatedAt)
	m.SecretCreatedAt = types.StringPointerValue(w.SecretCreatedAt)
	m.PreviousSecretValidUntil = types.StringPointerValue(w.PreviousSecretValidUntil)
}

func (r *ProjectWebhookResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data projectWebhookModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	current, err := r.client.GetProjectWebhook(ctx, data.ProjectUUID.ValueString())
	if err != nil {
		resp.State.RemoveResource(ctx)
		resp.Diagnostics.AddError("Unable to inspect project webhook", err.Error())
		return
	}
	if current.URL != nil {
		resp.State.RemoveResource(ctx)
		resp.Diagnostics.AddError("Project webhook already configured", "This project already has a default webhook URL. Import its project UUID to manage the existing setting; creation never adopts or overwrites an existing URL, even when it matches configuration.")
		return
	}
	desired := data.URL.ValueString()
	updated, err := r.client.UpdateProjectWebhook(ctx, data.ProjectUUID.ValueString(), &desired)
	if err != nil {
		// The PATCH may have committed before its response was lost. Keeping a
		// failed Create as tainted state could let later cleanup clear a setting
		// whose outcome/ownership was never confirmed. The project UUID remains
		// available in configuration for explicit inspect/import recovery.
		resp.State.RemoveResource(ctx)
		resp.Diagnostics.AddError("Unable to create project webhook", err.Error()+" The URL may have been saved if the request reached Opper. Inspect the project's webhook overview and explicitly import its project UUID before retrying. No retry or cleanup clear was sent.")
		return
	}
	if updated.URL == nil || *updated.URL != desired {
		resp.State.RemoveResource(ctx)
		resp.Diagnostics.AddError("Project webhook changed during creation", "The returned setting does not match the requested URL. Inspect the project's webhook overview and import its UUID to reconcile the outcome; no replacement URL was adopted.")
		return
	}
	data.setWebhook(updated)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	owned, _ := json.Marshal(desired)
	resp.Diagnostics.Append(resp.Private.SetKey(ctx, projectWebhookOwnedURLKey, owned)...)
}

func (r *ProjectWebhookResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data projectWebhookModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	current, err := r.client.GetProjectWebhook(ctx, data.ProjectUUID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read project webhook", err.Error())
		return
	}
	if current.URL == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	data.setWebhook(current)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	// Provider-private last-applied ownership is preserved unchanged by Read.
}

func (r *ProjectWebhookResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data, previous projectWebhookModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &previous)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if data.URL.Equal(previous.URL) {
		data.URLUpdatedAt, data.SecretCreatedAt, data.PreviousSecretValidUntil = previous.URLUpdatedAt, previous.SecretCreatedAt, previous.PreviousSecretValidUntil
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		return
	}
	desired := data.URL.ValueString()
	updated, err := r.client.UpdateProjectWebhook(ctx, data.ProjectUUID.ValueString(), &desired)
	if err != nil {
		resp.Diagnostics.Append(resp.State.Set(ctx, &previous)...)
		resp.Diagnostics.AddError("Unable to update project webhook", err.Error()+" If the outcome is uncertain, inspect the overview and explicitly reimport its project UUID before further changes. Ownership of an unconfirmed replacement URL is not adopted automatically.")
		return
	}
	if updated.URL == nil || *updated.URL != desired {
		resp.Diagnostics.Append(resp.State.Set(ctx, &previous)...)
		resp.Diagnostics.AddError("Project webhook changed during update", "The returned setting does not match the requested URL. State ownership is preserved; inspect and import the current setting to reconcile it.")
		return
	}
	data.setWebhook(updated)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	owned, _ := json.Marshal(desired)
	resp.Diagnostics.Append(resp.Private.SetKey(ctx, projectWebhookOwnedURLKey, owned)...)
}

func (r *ProjectWebhookResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data projectWebhookModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	current, err := r.client.GetProjectWebhook(ctx, data.ProjectUUID.ValueString())
	if client.IsNotFound(err) || err == nil && current.URL == nil {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to inspect project webhook before clearing", err.Error())
		return
	}
	encoded, diags := req.Private.GetKey(ctx, projectWebhookOwnedURLKey)
	resp.Diagnostics.Append(diags...)
	var owned string
	if resp.Diagnostics.HasError() || len(encoded) == 0 || json.Unmarshal(encoded, &owned) != nil || owned == "" {
		resp.Diagnostics.AddError("Project webhook ownership unavailable", "The last successfully written or imported URL is unavailable. Inspect the setting and explicitly import the project UUID before clearing it.")
		return
	}
	if *current.URL != owned {
		resp.Diagnostics.AddError("Project webhook changed outside Terraform", "The current webhook URL differs from the last URL this resource successfully wrote or explicitly imported. Destroy refuses to clear another setting. Reconcile a reviewed plan or explicitly import the project's current webhook first.")
		return
	}
	updated, err := r.client.UpdateProjectWebhook(ctx, data.ProjectUUID.ValueString(), nil)
	if client.IsNotFound(err) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to clear project webhook", err.Error())
		return
	}
	if updated.URL != nil {
		resp.Diagnostics.AddError("Project webhook changed while clearing", "The API still reports a configured webhook. State is preserved; inspect the current setting before retrying. The API does not provide atomic compare-and-swap for this setting.")
	}
}

func (r *ProjectWebhookResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !uuidPattern.MatchString(req.ID) {
		resp.Diagnostics.AddError("Invalid project webhook import ID", "Import the webhook using its lowercase project UUID.")
		return
	}
	current, err := r.client.GetProjectWebhook(ctx, req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Unable to inspect imported project webhook", err.Error())
		return
	}
	if current.URL == nil {
		resp.Diagnostics.AddError("Project webhook is not configured", "The project has no default webhook URL to import. Configure the resource to create one instead.")
		return
	}
	data := projectWebhookModel{ProjectUUID: types.StringValue(req.ID)}
	data.setWebhook(current)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	owned, _ := json.Marshal(*current.URL)
	resp.Diagnostics.Append(resp.Private.SetKey(ctx, projectWebhookOwnedURLKey, owned)...)
}

type projectWebhookURLValidator struct{}

func (projectWebhookURLValidator) Description(context.Context) string {
	return "URL must be a nonempty HTTPS URL of at most 2048 bytes, with a hostname, port 443 or no port, and no credentials, fragment, whitespace, control characters or backslashes. Opper validates public addressing and DNS."
}
func (v projectWebhookURLValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (v projectWebhookURLValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if !validProjectWebhookURLShape(req.ConfigValue.ValueString()) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid project webhook URL", v.Description(ctx))
	}
}

// This is deliberately DNS-free. Public host/IP resolution and connect-time
// SSRF enforcement belong to Opper's authoritative callback URL validation.
func validProjectWebhookURLShape(raw string) bool {
	if raw == "" || len(raw) > 2048 || strings.Contains(raw, "#") {
		return false
	}
	for _, r := range raw {
		if r <= 0x20 || r == 0x7f || r == '\\' {
			return false
		}
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Opaque == "" && u.Hostname() != "" && u.User == nil && (u.Port() == "" || u.Port() == "443")
}
