package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

type RoleDataSource struct {
	client *client.Client
	list   bool
}

func NewRoleDataSource() datasource.DataSource  { return &RoleDataSource{} }
func NewRolesDataSource() datasource.DataSource { return &RoleDataSource{list: true} }
func (d *RoleDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_role"
	if d.list {
		resp.TypeName += "s"
	}
}
func (d *RoleDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	var ok bool
	d.client, ok = req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Invalid role lookup configuration", "Expected an Opper API client.")
	}
}

type roleLookupModel struct {
	ID                   types.String `tfsdk:"id"`
	OrganizationUUID     types.String `tfsdk:"organization_uuid"`
	Name                 types.String `tfsdk:"name"`
	DisplayName          types.String `tfsdk:"display_name"`
	Permissions          types.Set    `tfsdk:"permissions"`
	EffectivePermissions types.Set    `tfsdk:"effective_permissions"`
	Priority             types.Int64  `tfsdk:"priority"`
	Custom               types.Bool   `tfsdk:"custom"`
	ChangeToken          types.String `tfsdk:"change_token"`
}
type rolesLookupModel struct {
	ID               types.String `tfsdk:"id"`
	OrganizationUUID types.String `tfsdk:"organization_uuid"`
	Roles            types.List   `tfsdk:"roles"`
	Permissions      types.Set    `tfsdk:"permissions"`
}

func roleLookupAttrs() map[string]schema.Attribute {
	return map[string]schema.Attribute{"organization_uuid": schema.StringAttribute{Computed: true}, "name": schema.StringAttribute{Computed: true}, "display_name": schema.StringAttribute{Computed: true}, "permissions": schema.SetAttribute{Computed: true, ElementType: types.StringType}, "effective_permissions": schema.SetAttribute{Computed: true, ElementType: types.StringType}, "priority": schema.Int64Attribute{Computed: true}, "custom": schema.BoolAttribute{Computed: true}, "change_token": schema.StringAttribute{Computed: true}}
}
func roleLookupType() types.ObjectType {
	return types.ObjectType{AttrTypes: map[string]attr.Type{"organization_uuid": types.StringType, "name": types.StringType, "display_name": types.StringType, "permissions": types.SetType{ElemType: types.StringType}, "effective_permissions": types.SetType{ElemType: types.StringType}, "priority": types.Int64Type, "custom": types.BoolType, "change_token": types.StringType}}
}
func roleObject(ctx context.Context, v client.Role) types.Object {
	permissions, _ := types.SetValueFrom(ctx, types.StringType, v.Permissions)
	effective, _ := types.SetValueFrom(ctx, types.StringType, v.EffectivePermissions)
	return types.ObjectValueMust(roleLookupType().AttrTypes, map[string]attr.Value{"organization_uuid": types.StringValue(v.OrganizationUUID), "name": types.StringValue(v.Name), "display_name": types.StringPointerValue(v.DisplayName), "permissions": permissions, "effective_permissions": effective, "priority": types.Int64Value(v.Priority), "custom": types.BoolValue(v.Custom), "change_token": types.StringValue(v.ChangeToken)})
}
func (d *RoleDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := roleLookupAttrs()
	attrs["id"] = schema.StringAttribute{Computed: true}
	attrs["organization_uuid"] = schema.StringAttribute{Required: true, Validators: projectIDValidators()}
	attrs["name"] = schema.StringAttribute{Required: true, MarkdownDescription: "Exact built-in admin/developer/viewer or custom_<hex> immutable name. Owner is not an assignable catalog role."}
	if d.list {
		attrs = map[string]schema.Attribute{"id": schema.StringAttribute{Computed: true}, "organization_uuid": schema.StringAttribute{Required: true, Validators: projectIDValidators()}, "roles": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: roleLookupAttrs()}}, "permissions": schema.SetAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Admin grant ceiling accepted for custom roles."}}
	}
	resp.Schema = schema.Schema{MarkdownDescription: "Read role definitions with organization identity verification and reviewed change tokens. Requires roles:read; never creates or modifies a role.", Attributes: attrs}
}
func (d *RoleDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.list {
		var m rolesLookupModel
		resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
		if resp.Diagnostics.HasError() {
			return
		}
		v, e := d.client.GetRoleCatalog(ctx)
		if e == nil && len(v.Roles) > 0 {
			e = roleOrg(v.Roles[0].OrganizationUUID, m.OrganizationUUID.ValueString())
		}
		if e != nil || len(v.Roles) == 0 {
			detail := "Role catalog must identify the configured organization."
			if e != nil {
				detail = e.Error()
			}
			resp.Diagnostics.AddError("Unable to read role catalog", detail)
			return
		}
		values := make([]attr.Value, 0, len(v.Roles))
		for _, role := range v.Roles {
			values = append(values, roleObject(ctx, role))
		}
		m.Roles, _ = types.ListValue(roleLookupType(), values)
		m.Permissions, _ = types.SetValueFrom(ctx, types.StringType, v.Permissions)
		m.ID = m.OrganizationUUID
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
		return
	}
	var m roleLookupModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	v, e := d.client.GetRole(ctx, m.Name.ValueString())
	if e == nil {
		e = roleOrg(v.OrganizationUUID, m.OrganizationUUID.ValueString())
	}
	if e != nil {
		resp.Diagnostics.AddError("Unable to read role", e.Error())
		return
	}
	m.ID = types.StringValue(v.OrganizationUUID + "/" + v.Name)
	m.DisplayName = types.StringPointerValue(v.DisplayName)
	m.Permissions, _ = types.SetValueFrom(ctx, types.StringType, v.Permissions)
	m.EffectivePermissions, _ = types.SetValueFrom(ctx, types.StringType, v.EffectivePermissions)
	m.Priority = types.Int64Value(v.Priority)
	m.Custom = types.BoolValue(v.Custom)
	m.ChangeToken = types.StringValue(v.ChangeToken)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

type MemberRoleDataSource struct{ client *client.Client }
type memberRoleLookupModel struct {
	ID               types.String `tfsdk:"id"`
	OrganizationUUID types.String `tfsdk:"organization_uuid"`
	MemberUUID       types.String `tfsdk:"member_uuid"`
	Role             types.String `tfsdk:"role"`
	StoredRole       types.String `tfsdk:"stored_role"`
	DirectoryManaged types.Bool   `tfsdk:"directory_managed"`
	Owner            types.Bool   `tfsdk:"owner"`
	Blocked          types.Bool   `tfsdk:"blocked"`
	ChangeToken      types.String `tfsdk:"change_token"`
}

func NewMemberRoleDataSource() datasource.DataSource { return &MemberRoleDataSource{} }
func (d *MemberRoleDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_member_role"
}
func (d *MemberRoleDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	var ok bool
	d.client, ok = req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Invalid member role lookup configuration", "Expected an Opper API client.")
	}
}
func (d *MemberRoleDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{MarkdownDescription: "Read an existing membership's role and admission flags without changing it. Requires member_roles:read.", Attributes: map[string]schema.Attribute{"id": schema.StringAttribute{Computed: true}, "organization_uuid": schema.StringAttribute{Required: true, Validators: projectIDValidators()}, "member_uuid": schema.StringAttribute{Required: true, Validators: projectIDValidators()}, "role": schema.StringAttribute{Computed: true}, "stored_role": schema.StringAttribute{Computed: true}, "directory_managed": schema.BoolAttribute{Computed: true}, "owner": schema.BoolAttribute{Computed: true}, "blocked": schema.BoolAttribute{Computed: true}, "change_token": schema.StringAttribute{Computed: true}}}
}
func (d *MemberRoleDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var m memberRoleLookupModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	v, e := d.client.GetMemberRole(ctx, m.MemberUUID.ValueString())
	if e == nil {
		e = roleOrg(v.OrganizationUUID, m.OrganizationUUID.ValueString())
	}
	if e != nil {
		resp.Diagnostics.AddError("Unable to read member role", e.Error())
		return
	}
	m.ID = types.StringValue(v.OrganizationUUID + "/" + v.MemberUUID)
	m.Role = types.StringValue(v.Role)
	m.StoredRole = types.StringPointerValue(v.StoredRole)
	m.DirectoryManaged = types.BoolValue(v.DirectoryManaged)
	m.Owner = types.BoolValue(v.Owner)
	m.Blocked = types.BoolValue(v.Blocked)
	m.ChangeToken = types.StringValue(v.ChangeToken)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}
