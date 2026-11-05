package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

var (
	_ resource.Resource                = (*CustomModelResource)(nil)
	_ resource.ResourceWithConfigure   = (*CustomModelResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*CustomModelResource)(nil)
	_ resource.ResourceWithImportState = (*CustomModelResource)(nil)
)

type CustomModelResource struct{ client *client.Client }
type customModelResourceModel struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	Identifier        types.String `tfsdk:"identifier"`
	CredentialWO      types.String `tfsdk:"credential_wo"`
	CredentialVersion types.Int64  `tfsdk:"credential_version"`
	CreationKey       types.String `tfsdk:"creation_idempotency_key"`
	Provider          types.String `tfsdk:"model_provider"`
	Type              types.String `tfsdk:"type"`
	CredentialPresent types.Bool   `tfsdk:"credential_present"`
	CreatedAt         types.String `tfsdk:"created_at"`
	UpdatedAt         types.String `tfsdk:"updated_at"`
	ChangeToken       types.String `tfsdk:"change_token"`
	DefinitionChanged types.Bool   `tfsdk:"definition_changed"`
	OperationID       types.String `tfsdk:"operation_id"`
	OperationKind     types.String `tfsdk:"operation_kind"`
	OperationPending  types.Bool   `tfsdk:"operation_pending"`
}

// Private state is still Terraform state. Only metadata and opaque validators
// belong here: never credentials or hashes derived from their plaintext.
type customModelAck struct {
	Token, Name, Identifier string
	Version                 int64
}
type customModelIntent struct {
	Key, ExpectedToken, Name, Identifier string
	Version                              int64
	Rotate                               bool
}
type customModelPrivate struct {
	Ack    customModelAck
	Intent *customModelIntent
}
type customModelPrivateData interface {
	GetKey(context.Context, string) ([]byte, diag.Diagnostics)
	SetKey(context.Context, string, []byte) diag.Diagnostics
}

const customModelPrivateKey = "custom_model_lifecycle_v1"

func loadCustomModelPrivate(ctx context.Context, p customModelPrivateData) (customModelPrivate, error) {
	var v customModelPrivate
	raw, d := p.GetKey(ctx, customModelPrivateKey)
	if d.HasError() {
		return v, errors.New("unable to read custom-model recovery state")
	}
	if len(raw) > 0 && json.Unmarshal(raw, &v) != nil {
		return v, errors.New("invalid custom-model recovery state; inspect and import the exact UUID")
	}
	return v, nil
}
func saveCustomModelPrivate(ctx context.Context, p customModelPrivateData, v customModelPrivate) diag.Diagnostics {
	raw, err := json.Marshal(v)
	if err != nil {
		return diag.Diagnostics{diag.NewErrorDiagnostic("Unable to save recovery state", "Custom-model recovery metadata could not be encoded.")}
	}
	return p.SetKey(ctx, customModelPrivateKey, raw)
}
func NewCustomModelResource() resource.Resource { return &CustomModelResource{} }
func (r *CustomModelResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_custom_model"
}
func (r *CustomModelResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage a default-endpoint OpenAI BYOK definition without provider calls. Requires Terraform 1.11+ and custom_models:read/write/update/delete. Credentials are write-only; rotation requires an increased credential_version. Unexplained definition changes require review and explicit UUID import before further writes.",
		Attributes: map[string]schema.Attribute{
			"id":                       schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, MarkdownDescription: "Immutable model UUID, or a pending-create marker while an uncertain create has no recovered UUID."},
			"name":                     schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.LengthBetween(1, 255), stringvalidator.RegexMatches(regexp.MustCompile(`\S`), "must not be blank")}},
			"identifier":               schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.LengthBetween(1, 255), stringvalidator.RegexMatches(regexp.MustCompile(`^openai/[A-Za-z0-9][A-Za-z0-9._:-]*$`), "must be a supported openai/model identifier")}},
			"credential_wo":            schema.StringAttribute{Optional: true, Sensitive: true, WriteOnly: true, MarkdownDescription: "Provider credential, read only from configuration and never persisted in plan/state. Required for creation and an increased credential_version; ignored when that version is unchanged."},
			"credential_version":       schema.Int64Attribute{Optional: true, Computed: true, Validators: []validator.Int64{int64validator.AtLeast(0)}, PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}, MarkdownDescription: "Terraform's acknowledged rotation command sequence, not an observed remote credential version. Create with a positive value; increase it to rotate. Omission preserves the previous sequence; import starts at zero."},
			"creation_idempotency_key": schema.StringAttribute{Optional: true, Validators: []validator.String{stringvalidator.RegexMatches(regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`), "must be a bounded operation key")}, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}, MarkdownDescription: "Required, known operation key for creation. Use a fresh key for every replacement/recreation; a completed key never creates another model. Imported definitions can omit it."},
			"model_provider":           schema.StringAttribute{Computed: true},
			"type":                     schema.StringAttribute{Computed: true},
			"credential_present":       schema.BoolAttribute{Computed: true},
			"created_at":               schema.StringAttribute{Computed: true},
			"updated_at":               schema.StringAttribute{Computed: true},
			"change_token":             schema.StringAttribute{Computed: true, MarkdownDescription: "Opaque observed definition validator. Does not authorize a write or describe credential validity."},
			"definition_changed":       schema.BoolAttribute{Computed: true, MarkdownDescription: "Complete remote state differs from this resource's confirmed baseline. Inspect and import before unexplained changes can be managed."},
			"operation_id":             schema.StringAttribute{Computed: true, MarkdownDescription: "Original creation key or deterministic update receipt identity; not a credential."},
			"operation_kind":           schema.StringAttribute{Computed: true},
			"operation_pending":        schema.BoolAttribute{Computed: true, MarkdownDescription: "An uncertain operation requires pure receipt recovery or explicit import. Further writes and deletion are blocked."},
		}}
}
func (r *CustomModelResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected custom-model configuration", "Expected an Opper API client.")
		return
	}
	r.client = c
}
func (m *customModelResourceModel) observe(s client.CustomModelState) {
	x := s.Model
	m.ID = types.StringValue(x.ID)
	m.Name = types.StringValue(x.Name)
	m.Identifier = types.StringValue(x.Identifier)
	m.Provider = types.StringPointerValue(x.Provider)
	m.Type = types.StringPointerValue(x.Type)
	m.CredentialPresent = types.BoolValue(x.CredentialPresent)
	m.CreatedAt = types.StringValue(x.CreatedAt)
	m.UpdatedAt = types.StringValue(x.UpdatedAt)
	m.ChangeToken = types.StringValue(s.ChangeToken)
	m.CredentialWO = types.StringNull()
}
func (m *customModelResourceModel) initialize() {
	m.CredentialWO = types.StringNull()
	m.Provider = types.StringNull()
	m.Type = types.StringNull()
	m.CredentialPresent = types.BoolValue(false)
	m.CreatedAt = types.StringNull()
	m.UpdatedAt = types.StringNull()
	m.ChangeToken = types.StringNull()
	m.DefinitionChanged = types.BoolValue(false)
	m.OperationPending = types.BoolValue(false)
	m.OperationID = types.StringNull()
	m.OperationKind = types.StringNull()
}
func (m *customModelResourceModel) pending(kind, key string) {
	m.OperationKind = types.StringValue(kind)
	m.OperationID = types.StringValue(key)
	m.OperationPending = types.BoolValue(true)
	m.CredentialWO = types.StringNull()
}
func acknowledgeCustomModel(m *customModelResourceModel, p *customModelPrivate, s client.CustomModelState, v int64) {
	m.observe(s)
	m.CredentialVersion = types.Int64Value(v)
	m.DefinitionChanged = types.BoolValue(false)
	m.OperationPending = types.BoolValue(false)
	p.Ack = customModelAck{Token: s.ChangeToken, Name: s.Model.Name, Identifier: s.Model.Identifier, Version: v}
	p.Intent = nil
}
func customModelOperation(id string, a customModelAck, name, identifier string, version int64) customModelIntent {
	// No credential material enters the intent key. The explicit version is the
	// command identity even if an ephemeral value changes between plan and apply.
	raw, _ := json.Marshal(struct {
		Contract, ID, Token, Name, Identifier string
		Version                               int64
	}{"opper-terraform-custom-model-update-v1", id, a.Token, name, identifier, version})
	h := sha256.Sum256(raw)
	return customModelIntent{Key: "tf-update-" + hex.EncodeToString(h[:]), ExpectedToken: a.Token, Name: name, Identifier: identifier, Version: version, Rotate: version > a.Version}
}
func customModelReceiptMatches(x client.CustomModelUpdateReceipt, id string) bool {
	return x.CustomModelUUID == id && x.ResourceExists && x.ResultMatchesCurrentState != nil && *x.ResultMatchesCurrentState
}
func (r *CustomModelResource) confirmUpdate(ctx context.Context, id string, i customModelIntent) (client.CustomModelState, error) {
	x, err := r.client.GetCustomModelUpdateOperation(ctx, i.Key)
	if err != nil {
		return client.CustomModelState{}, err
	}
	if !customModelReceiptMatches(x, id) {
		return client.CustomModelState{}, errors.New("the original update completed but its result is absent, changed, or cannot be compared; inspect and import before further writes")
	}
	s, err := r.client.GetCustomModelState(ctx, id)
	if err != nil {
		return s, err
	}
	if s.Model.Name != i.Name || s.Model.Identifier != i.Identifier || !s.Model.CredentialPresent {
		return s, errors.New("the recovered definition differs from the original update intent")
	}
	// The original result must still match after reading its state. Never take a
	// newer token as authorization to overwrite a changed or rotated definition.
	x, err = r.client.GetCustomModelUpdateOperation(ctx, i.Key)
	if err != nil {
		return s, err
	}
	if !customModelReceiptMatches(x, id) {
		return s, errors.New("the definition changed while confirming the original update")
	}
	return s, nil
}
func customModelMissingBaseline() error {
	return errors.New("confirmed custom-model baseline is unavailable; inspect and explicitly import the exact UUID")
}
func (r *CustomModelResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() {
		if req.Plan.Raw.IsNull() {
			return
		}
		var m customModelResourceModel
		resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
		if resp.Diagnostics.HasError() {
			return
		}
		if m.CreationKey.IsNull() || m.CreationKey.IsUnknown() || m.CreationKey.ValueString() == "" {
			resp.Diagnostics.AddAttributeError(path.Root("creation_idempotency_key"), "Creation key required", "Configure a fresh known creation_idempotency_key before creating a model.")
			return
		}
		if m.CredentialVersion.IsUnknown() || m.CredentialVersion.IsNull() || m.CredentialVersion.ValueInt64() < 1 {
			resp.Diagnostics.AddAttributeError(path.Root("credential_version"), "Initial credential version required", "Creation requires an explicit positive credential_version.")
			return
		}
		var cred types.String
		resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("credential_wo"), &cred)...)
		if cred.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root("credential_wo"), "Initial credential required", "Creation requires credential_wo. Use an ephemeral variable to keep the source value out of saved configuration artifacts.")
		}
		if !resp.Diagnostics.HasError() && r.client != nil {
			if _, err := r.client.GetCustomModelCreationState(ctx, m.CreationKey.ValueString()); err == nil {
				resp.Diagnostics.AddAttributeError(path.Root("creation_idempotency_key"), "Creation key already completed", "Inspect and import the original UUID, or configure a fresh creation key before replacement. A completed key cannot create another definition.")
			} else if !client.IsNotFound(err) {
				resp.Diagnostics.AddError("Unable to inspect creation receipt", err.Error())
			}
		}
		return
	}
	var previous customModelResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &previous)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := loadCustomModelPrivate(ctx, req.Private)
	if err != nil {
		resp.Diagnostics.AddError("Invalid custom-model recovery state", err.Error())
		return
	}
	if previous.OperationPending.ValueBool() {
		resp.Diagnostics.AddError("Custom-model operation needs recovery", "Recover its original receipt or inspect and import the exact UUID before planning further mutations.")
		return
	}
	if req.Plan.Raw.IsNull() {
		if previous.DefinitionChanged.ValueBool() {
			resp.Diagnostics.AddError("Custom-model definition changed", "Review the changed definition and explicitly import its UUID before deletion.")
			return
		}
		if p.Ack.Token == "" {
			resp.Diagnostics.AddError("Missing custom-model baseline", customModelMissingBaseline().Error())
		}
		return
	}
	var m customModelResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var configuredVersion types.Int64
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("credential_version"), &configuredVersion)...)
	if configuredVersion.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("credential_version"), "Known credential version required", "The rotation command sequence must be known during planning. Omit it to preserve the acknowledged sequence.")
		return
	}
	if m.CredentialVersion.IsUnknown() || m.CredentialVersion.IsNull() {
		m.CredentialVersion = previous.CredentialVersion
	}
	if m.CredentialVersion.ValueInt64() < previous.CredentialVersion.ValueInt64() {
		resp.Diagnostics.AddAttributeError(path.Root("credential_version"), "Credential version cannot decrease", "Keep the acknowledged command sequence or increase it with credential_wo.")
		return
	}
	if p.Ack.Token == "" {
		resp.Diagnostics.AddError("Missing custom-model baseline", customModelMissingBaseline().Error())
		return
	}
	if m.Name.IsUnknown() || m.Identifier.IsUnknown() {
		m.OperationID = types.StringUnknown()
		m.ChangeToken = types.StringUnknown()
		resp.Diagnostics.Append(resp.Plan.Set(ctx, &m)...)
		return
	}
	intent := customModelOperation(previous.ID.ValueString(), p.Ack, m.Name.ValueString(), m.Identifier.ValueString(), m.CredentialVersion.ValueInt64())
	intended := intent.Name != p.Ack.Name || intent.Identifier != p.Ack.Identifier || intent.Rotate
	observedDiff := !m.Name.Equal(previous.Name) || !m.Identifier.Equal(previous.Identifier)
	if !intended {
		if observedDiff && previous.DefinitionChanged.ValueBool() {
			resp.Diagnostics.AddError("Custom-model definition changed", "The complete definition changed outside a confirmed Terraform operation. Inspect it and explicitly import the UUID before reconciliation.")
			return
		}
		m.OperationID = previous.OperationID
		m.OperationKind = previous.OperationKind
		m.OperationPending = types.BoolValue(false)
		m.Provider = previous.Provider
		m.Type = previous.Type
		m.CredentialPresent = previous.CredentialPresent
		m.CreatedAt = previous.CreatedAt
		m.UpdatedAt = previous.UpdatedAt
		m.ChangeToken = previous.ChangeToken
		m.DefinitionChanged = previous.DefinitionChanged
		resp.Diagnostics.Append(resp.Plan.Set(ctx, &m)...)
		return
	}
	recoverOnly := false
	if previous.DefinitionChanged.ValueBool() {
		x, e := r.client.GetCustomModelUpdateOperation(ctx, intent.Key)
		if e != nil || !customModelReceiptMatches(x, previous.ID.ValueString()) {
			resp.Diagnostics.AddError("Custom-model definition changed", "The observed definition cannot be attributed to this exact pending intent. Inspect it and explicitly import the UUID; no fresh token or new rotation is used.")
			return
		}
		recoverOnly = true
	}
	if intent.Rotate && !recoverOnly {
		var cred types.String
		resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("credential_wo"), &cred)...)
		if cred.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root("credential_wo"), "Rotation credential required", "Increasing credential_version requires credential_wo.")
			return
		}
	}
	p.Intent = &intent
	resp.Diagnostics.Append(saveCustomModelPrivate(ctx, resp.Private, p)...)
	m.OperationID = types.StringUnknown()
	m.OperationKind = types.StringValue("update")
	m.OperationPending = types.BoolValue(false)
	m.ChangeToken = types.StringUnknown()
	m.DefinitionChanged = types.BoolUnknown()
	m.UpdatedAt = types.StringUnknown()
	m.Provider = previous.Provider
	m.Type = previous.Type
	m.CredentialPresent = previous.CredentialPresent
	m.CreatedAt = previous.CreatedAt
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &m)...)
}
func (r *CustomModelResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m customModelResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var credential types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("credential_wo"), &credential)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if credential.IsNull() || credential.IsUnknown() || strings.TrimSpace(credential.ValueString()) == "" || m.CreationKey.IsNull() || m.CreationKey.IsUnknown() || m.CredentialVersion.IsUnknown() || m.CredentialVersion.ValueInt64() < 1 {
		resp.Diagnostics.AddError("Incomplete custom-model create intent", "Creation requires a known creation key, positive credential_version and nonblank credential_wo.")
		return
	}
	version := m.CredentialVersion.ValueInt64()
	m.initialize()
	key := m.CreationKey.ValueString()
	// Pure lookup first protects hard-crash recovery and prevents adoption of a
	// different historical intent under a caller-reused creation key.
	receipt, err := r.client.GetCustomModelCreationState(ctx, key)
	if err == nil {
		r.retainCreate(ctx, &m, receipt.CustomModelUUID, key)
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
		resp.Diagnostics.AddError("Creation key already completed", "The original UUID is retained for review. Inspect and explicitly import it; use a fresh key for a different creation intent.")
		return
	}
	if !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Unable to inspect creation receipt", err.Error())
		return
	}
	result, err := r.client.CreateCustomModelWithState(ctx, m.Name.ValueString(), m.Identifier.ValueString(), credential.ValueString(), key)
	if err != nil {
		if !deploymentUncertain(err) {
			resp.Diagnostics.AddError("Unable to create custom model", err.Error())
			return
		}
		id := "pending-create:" + key
		if receipt, e := r.client.GetCustomModelCreationState(ctx, key); e == nil {
			id = receipt.CustomModelUUID
		}
		r.retainCreate(ctx, &m, id, key)
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
		resp.Diagnostics.AddError("Custom-model creation needs recovery", "The original operation identity is retained. Recover its receipt and explicitly import the confirmed UUID. No create is repeated.")
		return
	}
	m.ID = types.StringValue(result.Model.ID)
	if result.Replayed {
		r.retainCreate(ctx, &m, result.Model.ID, key)
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
		resp.Diagnostics.AddError("Existing custom-model creation recovered", "Inspect and explicitly import the original UUID. A historical creation receipt does not prove its original credential remains current.")
		return
	}
	s, err := r.client.GetCustomModelState(ctx, result.Model.ID)
	if err != nil || s.ChangeToken != result.Operation.InitialChangeToken || !reflect.DeepEqual(s.Model, result.Model) {
		r.retainCreate(ctx, &m, result.Model.ID, key)
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
		resp.Diagnostics.AddError("Unable to confirm created definition", "Creation completed but its current definition could not be confirmed. Inspect and explicitly import the original UUID.")
		return
	}
	p := customModelPrivate{}
	acknowledgeCustomModel(&m, &p, s, version)
	m.OperationID = types.StringValue(key)
	m.OperationKind = types.StringValue("create")
	resp.Diagnostics.Append(saveCustomModelPrivate(ctx, resp.Private, p)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *CustomModelResource) retainCreate(ctx context.Context, m *customModelResourceModel, id, key string) {
	m.ID = types.StringValue(id)
	m.CredentialVersion = types.Int64Value(0)
	m.pending("create", key)
	if !strings.HasPrefix(id, "pending-create:") {
		if s, err := r.client.GetCustomModelState(ctx, id); err == nil {
			m.observe(s)
		}
	}
}
func (r *CustomModelResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m customModelResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := loadCustomModelPrivate(ctx, req.Private)
	if err != nil {
		resp.Diagnostics.AddError("Invalid custom-model recovery state", err.Error())
		return
	}
	if m.OperationPending.ValueBool() {
		if m.OperationKind.ValueString() == "create" {
			x, e := r.client.GetCustomModelCreationState(ctx, m.OperationID.ValueString())
			if e != nil {
				resp.Diagnostics.AddError("Creation recovery remains unresolved", e.Error()+" No mutation or state removal was performed.")
				return
			}
			r.retainCreate(ctx, &m, x.CustomModelUUID, m.OperationID.ValueString())
			resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
			resp.Diagnostics.AddWarning("Explicit creation import required", "The original UUID was recovered. Inspect it, remove only this pending resource address from state, and import the exact UUID before another apply.")
			return
		}
		if p.Intent == nil {
			resp.Diagnostics.AddError("Missing pending update intent", customModelMissingBaseline().Error())
			return
		}
		s, e := r.confirmUpdate(ctx, m.ID.ValueString(), *p.Intent)
		if e != nil {
			resp.Diagnostics.AddError("Custom-model update needs recovery", e.Error()+" No mutation was repeated.")
			return
		}
		version := p.Intent.Version
		acknowledgeCustomModel(&m, &p, s, version)
		resp.Diagnostics.Append(saveCustomModelPrivate(ctx, resp.Private, p)...)
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
		return
	}
	s, err := r.client.GetCustomModelState(ctx, m.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read custom model", err.Error())
		return
	}
	m.observe(s)
	m.DefinitionChanged = types.BoolValue(p.Ack.Token == "" || s.ChangeToken != p.Ack.Token)
	if m.DefinitionChanged.ValueBool() {
		resp.Diagnostics.AddWarning("Custom-model definition changed", "An outside edit or interrupted operation changed the complete definition. This does not identify a credential change. Recover the exact pending intent or inspect and import the UUID before further writes.")
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *CustomModelResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m, previous customModelResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &previous)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// A rejected apply must never persist the planned rotation version as if it
	// had been acknowledged. Only a confirmed receipt replaces this prior state.
	resp.Diagnostics.Append(resp.State.Set(ctx, &previous)...)
	p, err := loadCustomModelPrivate(ctx, req.Private)
	if err != nil {
		resp.Diagnostics.AddError("Invalid custom-model recovery state", err.Error())
		return
	}
	if previous.OperationPending.ValueBool() || p.Ack.Token == "" {
		resp.Diagnostics.AddError("Custom-model operation needs recovery", customModelMissingBaseline().Error())
		return
	}
	intent := customModelOperation(previous.ID.ValueString(), p.Ack, m.Name.ValueString(), m.Identifier.ValueString(), m.CredentialVersion.ValueInt64())
	plannedIntent := p.Intent
	p.Intent = nil
	resp.Diagnostics.Append(saveCustomModelPrivate(ctx, resp.Private, p)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plannedIntent != nil && *plannedIntent != intent {
		resp.Diagnostics.AddError("Custom-model plan changed", "The original update intent no longer matches the saved plan.")
		return
	}
	p.Intent = &intent
	x, lookupErr := r.client.GetCustomModelUpdateOperation(ctx, intent.Key)
	if lookupErr != nil && !client.IsNotFound(lookupErr) {
		resp.Diagnostics.Append(resp.State.Set(ctx, &previous)...)
		resp.Diagnostics.AddError("Unable to inspect update receipt", lookupErr.Error())
		return
	}
	if lookupErr != nil {
		if previous.DefinitionChanged.ValueBool() {
			resp.Diagnostics.Append(resp.State.Set(ctx, &previous)...)
			resp.Diagnostics.AddError("Custom-model definition changed", "Inspect and explicitly import before changing this definition.")
			return
		}
		input := client.CustomModelUpdateInput{ExpectedChangeToken: intent.ExpectedToken, IdempotencyKey: intent.Key}
		if intent.Name != p.Ack.Name {
			v := intent.Name
			input.Name = &v
		}
		if intent.Identifier != p.Ack.Identifier {
			v := intent.Identifier
			input.Identifier = &v
		}
		if intent.Rotate {
			var cred types.String
			resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("credential_wo"), &cred)...)
			if resp.Diagnostics.HasError() {
				return
			}
			if cred.IsNull() || cred.IsUnknown() || strings.TrimSpace(cred.ValueString()) == "" {
				resp.Diagnostics.AddError("Rotation credential required", "The configured credential_wo must be nonblank and known at apply.")
				return
			}
			v := cred.ValueString()
			input.Credential = &v
		}
		if input.Name == nil && input.Identifier == nil && input.Credential == nil {
			m.OperationID = previous.OperationID
			m.OperationKind = previous.OperationKind
			m.OperationPending = types.BoolValue(false)
			m.observe(client.CustomModelState{Model: client.CustomModel{ID: previous.ID.ValueString(), Name: previous.Name.ValueString(), Identifier: previous.Identifier.ValueString(), Provider: previous.Provider.ValueStringPointer(), Type: previous.Type.ValueStringPointer(), CredentialPresent: previous.CredentialPresent.ValueBool(), CreatedAt: previous.CreatedAt.ValueString(), UpdatedAt: previous.UpdatedAt.ValueString()}, ChangeToken: previous.ChangeToken.ValueString()})
			m.DefinitionChanged = previous.DefinitionChanged
			resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
			return
		}
		result, e := r.client.UpdateCustomModel(ctx, previous.ID.ValueString(), input)
		if e != nil && !deploymentUncertain(e) {
			p.Intent = nil
			resp.Diagnostics.Append(saveCustomModelPrivate(ctx, resp.Private, p)...)
			resp.Diagnostics.Append(resp.State.Set(ctx, &previous)...)
			resp.Diagnostics.AddError("Unable to update custom model", e.Error()+" The provider does not refresh a token or reapply a rotation after a conflict.")
			return
		}
		if e != nil {
			if recovered, re := r.client.GetCustomModelUpdateOperation(ctx, intent.Key); re == nil {
				x = recovered
			} else {
				previous.pending("update", intent.Key)
				resp.Diagnostics.Append(saveCustomModelPrivate(ctx, resp.Private, p)...)
				resp.Diagnostics.Append(resp.State.Set(ctx, &previous)...)
				resp.Diagnostics.AddError("Custom-model update needs recovery", "The original receipt identity and prior credential version are retained. No PATCH is repeated.")
				return
			}
		} else {
			x = result.Operation
		}
	}
	s, err := r.confirmUpdate(ctx, previous.ID.ValueString(), intent)
	if !customModelReceiptMatches(x, previous.ID.ValueString()) || err != nil {
		previous.pending("update", intent.Key)
		resp.Diagnostics.Append(saveCustomModelPrivate(ctx, resp.Private, p)...)
		resp.Diagnostics.Append(resp.State.Set(ctx, &previous)...)
		resp.Diagnostics.AddError("Custom-model result needs review", "The historical update result is changed, absent, unknown, or could not be confirmed. The previous credential version is retained; inspect and import before further mutation.")
		return
	}
	acknowledgeCustomModel(&m, &p, s, intent.Version)
	m.OperationID = types.StringValue(intent.Key)
	m.OperationKind = types.StringValue("update")
	resp.Diagnostics.Append(saveCustomModelPrivate(ctx, resp.Private, p)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
func (r *CustomModelResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var m customModelResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if m.OperationPending.ValueBool() || m.DefinitionChanged.ValueBool() || strings.HasPrefix(m.ID.ValueString(), "pending-create:") {
		resp.Diagnostics.AddError("Custom-model deletion needs review", "Resolve the original operation or inspect and explicitly import before deletion.")
		return
	}
	p, err := loadCustomModelPrivate(ctx, req.Private)
	if err != nil || p.Ack.Token == "" {
		resp.Diagnostics.AddError("Missing custom-model baseline", customModelMissingBaseline().Error())
		return
	}
	if err = r.client.DeleteCustomModel(ctx, m.ID.ValueString(), p.Ack.Token); err != nil {
		resp.Diagnostics.AddError("Unable to delete reviewed custom model", err.Error()+" The original token is retained; no fresh state is read to force deletion.")
	}
}
func (r *CustomModelResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	s, err := r.client.GetCustomModelState(ctx, req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Unable to import custom model", err.Error())
		return
	}
	m := customModelResourceModel{CreationKey: types.StringNull()}
	m.initialize()
	p := customModelPrivate{}
	acknowledgeCustomModel(&m, &p, s, 0)
	resp.Diagnostics.Append(saveCustomModelPrivate(ctx, resp.Private, p)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// An operation key contains no credential, credential hash or caller-supplied
// opaque strings in diagnostics.
func (customModelIntent) String() string { return "custom-model intent (credential-free metadata)" }

var _ = fmt.Stringer(customModelIntent{})
