package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

var (
	_                  resource.Resource                = (*DynamicRouteResource)(nil)
	_                  resource.ResourceWithConfigure   = (*DynamicRouteResource)(nil)
	_                  resource.ResourceWithImportState = (*DynamicRouteResource)(nil)
	dynamicRouteNameRE                                  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
)

type DynamicRouteResource struct{ client *client.Client }

type dynamicRouteModel struct {
	ID                  types.String         `tfsdk:"id"`
	Name                types.String         `tfsdk:"name"`
	DisplayName         types.String         `tfsdk:"display_name"`
	Description         types.String         `tfsdk:"description"`
	GraphJSON           jsontypes.Normalized `tfsdk:"graph_json"`
	EffectiveGraphJSON  jsontypes.Normalized `tfsdk:"effective_graph_json"`
	UUID                types.String         `tfsdk:"uuid"`
	InvocationModel     types.String         `tfsdk:"invocation_model"`
	DraftRevision       types.Int64          `tfsdk:"draft_revision"`
	ActiveVersionNumber types.Int64          `tfsdk:"active_version_number"`
	CreatedAt           types.String         `tfsdk:"created_at"`
}

// desiredGraph distinguishes an apply from refresh. The API normalizes graph
// defaults; a snapshot of that result lets refresh distinguish normalization
// from edits made outside Terraform without dropping desired configuration.
func (m *dynamicRouteModel) setDetail(d client.DynamicRouteDetail, desiredGraph *jsontypes.Normalized) {
	remote := jsontypes.NewNormalizedValue(string(d.Draft.Graph))
	if desiredGraph != nil {
		m.GraphJSON = *desiredGraph
	} else if m.EffectiveGraphJSON.IsNull() || m.EffectiveGraphJSON.IsUnknown() || !dynamicRouteJSONEqual(m.EffectiveGraphJSON.ValueString(), remote.ValueString()) {
		m.GraphJSON = remote
	}
	m.EffectiveGraphJSON = remote
	m.ID = types.StringValue(d.Route.Name)
	m.Name = types.StringValue(d.Route.Name)
	m.DisplayName = types.StringValue(d.Route.DisplayName)
	m.Description = types.StringValue(d.Route.Description)
	m.UUID = types.StringValue(d.Route.UUID)
	m.InvocationModel = types.StringValue(d.Route.InvocationModel)
	m.DraftRevision = types.Int64Value(d.Draft.Revision)
	m.ActiveVersionNumber = types.Int64Null()
	if d.ActiveVersion != nil {
		m.ActiveVersionNumber = types.Int64Value(d.ActiveVersion.VersionNumber)
	}
	m.CreatedAt = types.StringNull()
	if d.Route.CreatedAt != "" {
		m.CreatedAt = types.StringValue(d.Route.CreatedAt)
	}
}

func dynamicRouteJSONEqual(a, b string) bool {
	var av, bv any
	ad, bd := json.NewDecoder(strings.NewReader(a)), json.NewDecoder(strings.NewReader(b))
	ad.UseNumber()
	bd.UseNumber()
	if ad.Decode(&av) != nil || bd.Decode(&bv) != nil {
		return false
	}
	aa, _ := json.Marshal(av)
	bb, _ := json.Marshal(bv)
	return bytes.Equal(aa, bb)
}

func NewDynamicRouteResource() resource.Resource { return &DynamicRouteResource{} }

func (r *DynamicRouteResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dynamic_route"
}

func (r *DynamicRouteResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage an Opper dynamic route's draft and metadata. This resource never deploys a graph. Existing active versions are observed only; deleting the route also disables its active version.",
		Attributes: map[string]schema.Attribute{
			"id":                    schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, MarkdownDescription: "Route name; used for import."},
			"name":                  schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.RegexMatches(dynamicRouteNameRE, "must match ^[a-z0-9][a-z0-9._-]{0,127}$")}, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}, MarkdownDescription: "Route identifier, unique within the organization. Changing it replaces the route."},
			"display_name":          schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""), Validators: []validator.String{dynamicRouteLabelValidator{}}, MarkdownDescription: "Human label. Empty uses the route name in the platform. Does not deploy or increment the draft revision."},
			"description":           schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""), MarkdownDescription: "Draft description. Removing it clears the previous description."},
			"graph_json":            schema.StringAttribute{Required: true, CustomType: jsontypes.NormalizedType{}, Validators: []validator.String{dynamicRouteGraphValidator{}}, MarkdownDescription: "Desired draft graph as a JSON object containing elements. Formatting-only changes make no API writes. The server validates node and graph semantics."},
			"effective_graph_json":  schema.StringAttribute{Computed: true, CustomType: jsontypes.NormalizedType{}, MarkdownDescription: "Server-normalized draft graph, including applied defaults."},
			"uuid":                  schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, MarkdownDescription: "Stable route UUID."},
			"invocation_model":      schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, MarkdownDescription: "Invocation name dynamic/<name>. A draft-only route cannot serve calls until deployed outside Terraform."},
			"draft_revision":        schema.Int64Attribute{Computed: true, MarkdownDescription: "Current draft revision. Changes only when a draft is saved."},
			"active_version_number": schema.Int64Attribute{Computed: true, MarkdownDescription: "Active version, if any. Observational only; Terraform never deploys or undeploys it."},
			"created_at":            schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, MarkdownDescription: "Creation timestamp."},
		},
	}
}

func (r *DynamicRouteResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected resource configuration", "Expected an Opper API client. Report this provider error to the maintainers.")
		return
	}
	r.client = c
}

func (r *DynamicRouteResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data dynamicRouteModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	d, err := r.client.CreateDynamicRoute(ctx, data.Name.ValueString(), data.DisplayName.ValueString(), data.Description.ValueString(), json.RawMessage(data.GraphJSON.ValueString()))
	if err != nil {
		resp.Diagnostics.AddError("Unable to create dynamic route", err.Error()+" If the request may have reached Opper, inspect the route and import its name before retrying. Creation does not support idempotency keys.")
		return
	}
	graph := data.GraphJSON
	data.setDetail(d, &graph)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *DynamicRouteResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data dynamicRouteModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	d, err := r.client.GetDynamicRoute(ctx, data.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read dynamic route", err.Error())
		return
	}
	data.setDetail(d, nil)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *DynamicRouteResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data, previous dynamicRouteModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &previous)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Decide the draft work from the refreshed state the user planned against.
	// A metadata response can include a newer, concurrently edited draft; it
	// must never turn a label-only plan into an unplanned draft overwrite.
	var description *string
	if !data.Description.Equal(previous.Description) {
		value := data.Description.ValueString()
		description = &value
	}
	var graph json.RawMessage
	if !dynamicRouteJSONEqual(data.GraphJSON.ValueString(), previous.GraphJSON.ValueString()) {
		graph = json.RawMessage(data.GraphJSON.ValueString())
	}
	// Metadata and draft writes are separate API operations. Persist each
	// successful step so a later refusal leaves its completed work recoverable.
	if !data.DisplayName.Equal(previous.DisplayName) {
		d, err := r.client.UpdateDynamicRouteDisplayName(ctx, data.ID.ValueString(), data.DisplayName.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Unable to update dynamic route label", err.Error())
			return
		}
		concurrentDraft := d.Route.Description != previous.Description.ValueString() || !dynamicRouteJSONEqual(string(d.Draft.Graph), previous.EffectiveGraphJSON.ValueString())
		previous.setDetail(d, nil)
		resp.Diagnostics.Append(resp.State.Set(ctx, &previous)...)
		if resp.Diagnostics.HasError() {
			return
		}
		if concurrentDraft {
			resp.Diagnostics.AddError("Concurrent dynamic route draft change", "The route draft changed after Terraform's refresh while its label was being updated. The completed label and observed draft are saved in state; no draft write was sent. Review a new plan before reconciling the draft.")
			return
		}
	}
	if description != nil || graph != nil {
		d, err := r.client.UpdateDynamicRouteDraft(ctx, data.ID.ValueString(), description, graph)
		if err != nil {
			resp.Diagnostics.AddError("Unable to update dynamic route draft", err.Error())
			return
		}
		desired := data.GraphJSON
		data.setDetail(d, &desired)
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		return
	}
	// Preserve refreshed observations rather than persisting unknown computed
	// plan values when only presentation metadata changed.
	previous.GraphJSON = data.GraphJSON
	resp.Diagnostics.Append(resp.State.Set(ctx, &previous)...)
}

func (r *DynamicRouteResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data dynamicRouteModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteDynamicRoute(ctx, data.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Unable to delete dynamic route", err.Error())
	}
}

func (r *DynamicRouteResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !dynamicRouteNameRE.MatchString(req.ID) {
		resp.Diagnostics.AddError("Invalid dynamic route import ID", "Import by the route's bare name, without the dynamic/ prefix.")
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

type dynamicRouteLabelValidator struct{}

func (dynamicRouteLabelValidator) Description(context.Context) string {
	return "Label must have at most 128 characters, no control characters, and no leading or trailing whitespace."
}
func (v dynamicRouteLabelValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (v dynamicRouteLabelValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	value := req.ConfigValue.ValueString()
	if value != strings.TrimSpace(value) || !utf8.ValidString(value) || utf8.RuneCountInString(value) > 128 || strings.ContainsFunc(value, unicode.IsControl) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid route display name", v.Description(ctx))
	}
}

type dynamicRouteGraphValidator struct{}

func (dynamicRouteGraphValidator) Description(context.Context) string {
	return "Graph must be a JSON object containing a nonempty elements array. Graph and element fields must match the published graph schema."
}
func (v dynamicRouteGraphValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (v dynamicRouteGraphValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	var graph struct {
		Elements []struct {
			ID         string                     `json:"id"`
			Type       string                     `json:"type"`
			Properties map[string]json.RawMessage `json:"properties,omitempty"`
			Outputs    map[string]struct {
				ElementID string `json:"elementId"`
			} `json:"outputs"`
		} `json:"elements"`
	}
	decoder := json.NewDecoder(strings.NewReader(req.ConfigValue.ValueString()))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&graph)
	var extra any
	if err != nil || len(graph.Elements) == 0 || decoder.Decode(&extra) != io.EOF {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid dynamic route graph", v.Description(ctx)+" See the Management API graph reference; node properties and connectivity are validated by Opper.")
	}
}
