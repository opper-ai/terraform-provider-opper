package provider

import (
	"context"
	"regexp"
	"strings"

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
	_           resource.Resource                = (*ProjectResource)(nil)
	_           resource.ResourceWithConfigure   = (*ProjectResource)(nil)
	_           resource.ResourceWithImportState = (*ProjectResource)(nil)
	uuidPattern                                  = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

type ProjectResource struct{ client *client.Client }

type projectModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	CreatedAt   types.String `tfsdk:"created_at"`
}

func (m *projectModel) setProject(p client.Project) {
	m.ID = types.StringValue(p.UUID)
	m.Name = types.StringValue(p.Name)
	m.Description = types.StringValue(p.Description)
	m.CreatedAt = types.StringNull()
	if p.CreatedAt != "" {
		m.CreatedAt = types.StringValue(p.CreatedAt)
	}
}

func NewProjectResource() resource.Resource { return &ProjectResource{} }

func (r *ProjectResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project"
}

func (r *ProjectResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage an Opper project. Deleting a project also permanently revokes all of its runtime keys. Retention is configured through rules rather than projects.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true, MarkdownDescription: "Stable project UUID. Used for import.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required: true, MarkdownDescription: "Project name, unique within the organization. Leading and trailing whitespace are not allowed.",
				Validators: []validator.String{projectNameValidator{}},
			},
			"description": schema.StringAttribute{
				Optional: true, Computed: true, Default: stringdefault.StaticString(""),
				MarkdownDescription: "Project description. Defaults to an empty string. Removing this argument clears the remote description.",
			},
			"created_at": schema.StringAttribute{
				Computed: true, MarkdownDescription: "Creation timestamp, when provided by Opper.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"initial_access": initialProjectAccessSchema(),
		},
	}
}

func (r *ProjectResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *ProjectResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data projectResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var p client.Project
	var err error
	if data.InitialAccess.IsNull() {
		p, err = r.client.CreateProject(ctx, data.Name.ValueString(), data.Description.ValueString())
	} else {
		access, inputErr := initialProjectAccessInput(ctx, data.InitialAccess)
		if inputErr != nil {
			resp.Diagnostics.AddAttributeError(path.Root("initial_access"), "Invalid initial project access", inputErr.Error())
			return
		}
		p, err = r.client.CreateProjectWithAccess(ctx, data.Name.ValueString(), data.Description.ValueString(), access)
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to create project", err.Error()+" If the request may have reached Opper, check for the project and import its UUID before retrying. Project creation does not support idempotency keys.")
		return
	}
	data.setProject(p)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ProjectResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data projectResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := r.client.GetProject(ctx, data.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Unable to read project", err.Error())
		return
	}
	data.setProject(p)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ProjectResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data, previous projectResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &previous)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := initialProjectAccessChange(ctx, data.InitialAccess, previous.InitialAccess, false); err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("initial_access"), "Initial project access is creation-only", err.Error())
		return
	}
	// PATCH only changed fields. In particular, Opper's protected default
	// project refuses any name field, even when its value is unchanged.
	var name, description *string
	if !data.Name.Equal(previous.Name) {
		value := data.Name.ValueString()
		name = &value
	}
	if !data.Description.Equal(previous.Description) {
		value := data.Description.ValueString()
		description = &value
	}
	if name == nil && description == nil {
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		return
	}
	p, err := r.client.UpdateProject(ctx, data.ID.ValueString(), name, description)
	if err != nil {
		resp.Diagnostics.AddError("Unable to update project", err.Error())
		return
	}
	data.setProject(p)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ProjectResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data projectResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteProject(ctx, data.ID.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Unable to delete project", err.Error())
	}
}

func (r *ProjectResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !uuidPattern.MatchString(req.ID) {
		resp.Diagnostics.AddError("Invalid project import ID", "Import a project using its UUID, for example 01234567-89ab-cdef-0123-456789abcdef.")
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

type projectNameValidator struct{}

func (projectNameValidator) Description(context.Context) string {
	return "Name must be nonempty and have no leading or trailing whitespace."
}

func (v projectNameValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v projectNameValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	name := req.ConfigValue.ValueString()
	if name == "" || name != strings.TrimSpace(name) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid project name", v.Description(ctx))
	}
}

func projectIDValidators() []validator.String {
	return []validator.String{stringvalidator.RegexMatches(uuidPattern, "must be a canonical lowercase UUID")}
}
