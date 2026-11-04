package provider

import (
	"context"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

var (
	_ datasource.DataSource                   = (*MemberDataSource)(nil)
	_ datasource.DataSourceWithConfigure      = (*MemberDataSource)(nil)
	_ datasource.DataSourceWithValidateConfig = (*MemberDataSource)(nil)
	_ datasource.DataSource                   = (*MembersDataSource)(nil)
	_ datasource.DataSourceWithConfigure      = (*MembersDataSource)(nil)
)

type MemberDataSource struct{ client *client.Client }
type MembersDataSource struct{ client *client.Client }

type memberDataSourceModel struct {
	ID     types.String `tfsdk:"id"`
	UUID   types.String `tfsdk:"uuid"`
	Email  types.String `tfsdk:"email"`
	Member types.Object `tfsdk:"member"`
}

type membersDataSourceModel struct {
	ID      types.String `tfsdk:"id"`
	Members types.List   `tfsdk:"members"`
}

func NewMemberDataSource() datasource.DataSource  { return &MemberDataSource{} }
func NewMembersDataSource() datasource.DataSource { return &MembersDataSource{} }

func (d *MemberDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_member"
}

func (d *MembersDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_members"
}

func memberObjectAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"uuid":       schema.StringAttribute{Computed: true, MarkdownDescription: "Member's user UUID."},
		"email":      schema.StringAttribute{Computed: true, MarkdownDescription: "Email returned by Opper, or an empty string when unavailable."},
		"name":       schema.StringAttribute{Computed: true, MarkdownDescription: "Display name, or an empty string when unavailable."},
		"role":       schema.StringAttribute{Computed: true, MarkdownDescription: "Effective built-in or custom role. Empty when the membership grants no role, such as a blocked member."},
		"is_owner":   schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether this member is the organization's owner."},
		"created_at": schema.StringAttribute{Computed: true, MarkdownDescription: "Opper account creation timestamp, when available. This is not the date the member joined the organization."},
	}
}

func memberObjectType() types.ObjectType {
	return types.ObjectType{AttrTypes: map[string]attr.Type{
		"uuid": types.StringType, "email": types.StringType, "name": types.StringType,
		"role": types.StringType, "is_owner": types.BoolType, "created_at": types.StringType,
	}}
}

func memberObjectValue(member client.Member) types.Object {
	return types.ObjectValueMust(memberObjectType().AttrTypes, map[string]attr.Value{
		"uuid": types.StringValue(member.UUID), "email": types.StringValue(member.Email),
		"name": types.StringValue(member.Name), "role": types.StringValue(member.Role),
		"is_owner": types.BoolValue(member.IsOwner), "created_at": types.StringPointerValue(member.CreatedAt),
	})
}

func (d *MemberDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Look up exactly one member in the management key's organization. Set exactly one of uuid or email; email matching is case-insensitive and exact. A missing or ambiguous member is an error. Requires members:read and performs no membership changes.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, MarkdownDescription: "UUID of the uniquely matched member."},
			"uuid": schema.StringAttribute{
				Optional: true, Validators: []validator.String{memberUUIDSelectorValidator{}},
				MarkdownDescription: "Exact canonical lowercase member UUID selector. Mutually exclusive with email; preserved as configured.",
			},
			"email": schema.StringAttribute{
				Optional: true, Validators: []validator.String{memberEmailSelectorValidator{}},
				MarkdownDescription: "Exact email selector, compared case-insensitively. Mutually exclusive with uuid; configured case is preserved. The observed email is available in member.email.",
			},
			"member": schema.SingleNestedAttribute{Computed: true, Attributes: memberObjectAttributes(), MarkdownDescription: "The uniquely matched member as returned by Opper."},
		},
	}
}

func (d *MembersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Read the management key organization's members in deterministic UUID order. Requires members:read. No invitation, removal or role changes are performed.",
		Attributes: map[string]schema.Attribute{
			"id":      schema.StringAttribute{Computed: true, MarkdownDescription: "Constant members identifier, scoped by the provider's organization credential."},
			"members": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: memberObjectAttributes()}, MarkdownDescription: "All organization members, sorted by UUID. Empty when there are none."},
		},
	}
}

func (d *MemberDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected member data source configuration", "Expected an Opper API client.")
		return
	}
	d.client = c
}

func (d *MembersDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected members data source configuration", "Expected an Opper API client.")
		return
	}
	d.client = c
}

func validateMemberSelectors(data memberDataSourceModel, diags *diag.Diagnostics) {
	// An unknown optional selector can still resolve to null. Defer the
	// relationship check until both selectors are known, then recheck in Read.
	if data.UUID.IsUnknown() || data.Email.IsUnknown() {
		return
	}
	if data.UUID.IsNull() == data.Email.IsNull() {
		diags.AddError("Exactly one member selector required", "Configure exactly one of uuid or email to identify a member in this provider's organization.")
	}
}

func (d *MemberDataSource) ValidateConfig(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	var data memberDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if !resp.Diagnostics.HasError() {
		validateMemberSelectors(data, &resp.Diagnostics)
	}
}

func (d *MemberDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data memberDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	validateMemberSelectors(data, &resp.Diagnostics)
	if data.UUID.IsUnknown() || data.Email.IsUnknown() {
		resp.Diagnostics.AddError("Member selector is unknown", "The member selector must be known before it can be read.")
	}
	if resp.Diagnostics.HasError() {
		return
	}
	members, err := d.client.ListMembers(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read organization members", err.Error())
		return
	}
	var match client.Member
	count := 0
	for _, member := range members {
		selected := !data.UUID.IsNull() && member.UUID == data.UUID.ValueString()
		if !data.Email.IsNull() {
			selected = strings.EqualFold(member.Email, data.Email.ValueString())
		}
		if selected {
			match = member
			count++
		}
	}
	if count == 0 {
		resp.Diagnostics.AddError("Member not found", "No member in this provider's organization matches the configured selector.")
		return
	}
	if count != 1 {
		resp.Diagnostics.AddError("Ambiguous member selector", "The configured selector matches multiple members. Use a unique member UUID; no member was selected.")
		return
	}
	data.ID = types.StringValue(match.UUID)
	data.Member = memberObjectValue(match)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (d *MembersDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data membersDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	members, err := d.client.ListMembers(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read organization members", err.Error())
		return
	}
	sort.Slice(members, func(i, j int) bool { return members[i].UUID < members[j].UUID })
	values := make([]attr.Value, 0, len(members))
	for _, member := range members {
		values = append(values, memberObjectValue(member))
	}
	data.ID = types.StringValue("members")
	var diags diag.Diagnostics
	data.Members, diags = types.ListValue(memberObjectType(), values)
	resp.Diagnostics.Append(diags...)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	}
}

type memberEmailSelectorValidator struct{}

// The shared RegexMatches validator includes the invalid value in its
// diagnostic. Keep member selectors generic even if a user puts an email in
// the UUID field accidentally.
type memberUUIDSelectorValidator struct{}

func (memberUUIDSelectorValidator) Description(context.Context) string {
	return "Member UUID selector must be a canonical lowercase UUID."
}
func (v memberUUIDSelectorValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (v memberUUIDSelectorValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if !uuidPattern.MatchString(req.ConfigValue.ValueString()) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid member UUID selector", v.Description(ctx))
	}
}

func (memberEmailSelectorValidator) Description(context.Context) string {
	return "Email selector must be nonempty with no leading or trailing whitespace."
}
func (v memberEmailSelectorValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (v memberEmailSelectorValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	value := req.ConfigValue.ValueString()
	if value == "" || value != strings.TrimSpace(value) {
		resp.Diagnostics.AddAttributeError(path.Root("email"), "Invalid member email selector", v.Description(ctx))
	}
}
