package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

// Role contains the complete public definition and its organization-bound CAS
// validator. Name is immutable; DisplayName is a human label.
type Role struct {
	OrganizationUUID     string   `json:"organization_uuid"`
	Name                 string   `json:"name"`
	DisplayName          *string  `json:"display_name"`
	Permissions          []string `json:"permissions"`
	EffectivePermissions []string `json:"effective_permissions"`
	Priority             int64    `json:"priority"`
	Custom               bool     `json:"custom"`
	ChangeToken          string   `json:"change_token"`
}
type RoleCatalog struct {
	Roles       []Role   `json:"roles"`
	Permissions []string `json:"permissions"`
}
type MemberRole struct {
	OrganizationUUID string  `json:"organization_uuid"`
	MemberUUID       string  `json:"member_uuid"`
	StoredRole       *string `json:"stored_role"`
	Role             string  `json:"role"`
	DirectoryManaged bool    `json:"directory_managed"`
	Owner            bool    `json:"owner"`
	Blocked          bool    `json:"blocked"`
	ChangeToken      string  `json:"change_token"`
}
type RoleCreateInput struct {
	DisplayName    string   `json:"display_name"`
	Permissions    []string `json:"permissions"`
	Priority       int64    `json:"priority"`
	IdempotencyKey string   `json:"idempotency_key"`
}
type RoleUpdateInput struct {
	DisplayName         string   `json:"display_name"`
	Permissions         []string `json:"permissions"`
	Priority            int64    `json:"priority"`
	ExpectedChangeToken string   `json:"expected_change_token"`
	IdempotencyKey      string   `json:"idempotency_key"`
}
type MemberRoleInput struct {
	Role                    string `json:"role"`
	ExpectedChangeToken     string `json:"expected_change_token"`
	ExpectedRoleChangeToken string `json:"expected_role_change_token"`
	IdempotencyKey          string `json:"idempotency_key"`
}
type RoleReceipt struct {
	IdempotencyKey            string `json:"idempotency_key"`
	ResourceID                string `json:"resource_id"`
	ResourceExists            bool   `json:"resource_exists"`
	ResultChangeToken         string `json:"result_change_token"`
	ResultMatchesCurrentState *bool  `json:"result_matches_current_state"`
}
type RoleResult struct {
	Role      Role
	Operation RoleReceipt
	Replayed  bool
}
type MemberRoleResult struct {
	MemberRole MemberRole
	Operation  RoleReceipt
	Replayed   bool
}

var roleNameRE = regexp.MustCompile(`^custom_[0-9a-f]{32}$`)
var roleTokenRE = regexp.MustCompile(`^orv1_[A-Za-z0-9_-]{43}$`)
var memberRoleTokenRE = regexp.MustCompile(`^omrv1_[A-Za-z0-9_-]{43}$`)
var permissionRE = regexp.MustCompile(`^[a-z][a-z0-9_]*(?::[a-z][a-z0-9_]*)+$`)
var errRoleResponse = errors.New("Opper Management API returned an invalid or incomplete role response")

func roleLocal(message string) error { return &localValidationError{message: message} }
func assignableRole(name string) bool {
	return name == "admin" || name == "developer" || name == "viewer" || roleNameRE.MatchString(name)
}
func roleDefinitionValid(name string, grants []string, priority int64) bool {
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 64 || strings.TrimSpace(name) == "" || len(grants) < 1 || len(grants) > 50 || priority < 1 || priority > 299 {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "owner", "admin", "developer", "viewer", "member":
		return false
	}
	for _, p := range grants {
		if len(p) > 128 || !permissionRE.MatchString(p) {
			return false
		}
	}
	return true
}
func roleEnvelope(raw []byte) (map[string]json.RawMessage, error) {
	v, e := customModelObject(raw, "meta", "data")
	if e != nil {
		return nil, errRoleResponse
	}
	return v, nil
}
func roleStrings(raw json.RawMessage, max int) ([]string, error) {
	var v []string
	if string(raw) == "null" || json.Unmarshal(raw, &v) != nil || len(v) > max {
		return nil, errRoleResponse
	}
	seen := map[string]bool{}
	for _, s := range v {
		if len(s) > 128 || !permissionRE.MatchString(s) || seen[s] {
			return nil, errRoleResponse
		}
		seen[s] = true
	}
	if !sort.StringsAreSorted(v) {
		return nil, errRoleResponse
	}
	return v, nil
}
func decodeRole(raw json.RawMessage) (Role, error) {
	var v Role
	o, e := customModelObject(raw, "organization_uuid", "name", "display_name", "permissions", "effective_permissions", "priority", "custom", "change_token")
	if e != nil || json.Unmarshal(raw, &v) != nil {
		return v, errRoleResponse
	}
	if !validCustomModelID(v.OrganizationUUID) || !assignableRole(v.Name) || !roleTokenRE.MatchString(v.ChangeToken) || v.Custom != roleNameRE.MatchString(v.Name) {
		return v, errRoleResponse
	}
	if v.Permissions, e = roleStrings(o["permissions"], 50); e != nil {
		return v, e
	}
	if v.EffectivePermissions, e = roleStrings(o["effective_permissions"], 100); e != nil {
		return v, e
	}
	// A snapshot describes stored state, including legacy definitions that do
	// not satisfy today's write rules. Nullable labels, empty declared grants
	// and older priority values must remain readable without normalizing them.
	var priority *int64
	if json.Unmarshal(o["priority"], &priority) != nil || priority == nil {
		return v, errRoleResponse
	}
	v.Priority = *priority
	if !v.Custom && v.Priority != map[string]int64{"admin": 300, "developer": 200, "viewer": 100}[v.Name] {
		return v, errRoleResponse
	}
	if _, e = customModelBool(o["custom"]); e != nil {
		return v, errRoleResponse
	}
	return v, nil
}
func decodeMemberRole(raw json.RawMessage) (MemberRole, error) {
	var v MemberRole
	o, e := customModelObject(raw, "organization_uuid", "member_uuid", "stored_role", "role", "directory_managed", "owner", "blocked", "change_token")
	if e != nil || json.Unmarshal(raw, &v) != nil {
		return v, errRoleResponse
	}
	if !validCustomModelID(v.OrganizationUUID) || !validCustomModelID(v.MemberUUID) || !memberRoleTokenRE.MatchString(v.ChangeToken) || v.Role != "" && v.Role != "owner" && !assignableRole(v.Role) {
		return v, errRoleResponse
	}
	if _, e = customModelString(o["role"]); e != nil {
		return v, errRoleResponse
	}
	if v.StoredRole != nil && *v.StoredRole != "member" && *v.StoredRole != "owner" && !assignableRole(*v.StoredRole) {
		return v, errRoleResponse
	}
	for _, f := range []string{"directory_managed", "owner", "blocked"} {
		if _, e = customModelBool(o[f]); e != nil {
			return v, errRoleResponse
		}
	}
	return v, nil
}
func decodeRoleReceipt(raw json.RawMessage, key string, member bool) (RoleReceipt, error) {
	var v RoleReceipt
	o, e := customModelObject(raw, "idempotency_key", "resource_id", "resource_exists", "result_change_token", "result_matches_current_state")
	if e != nil || json.Unmarshal(raw, &v) != nil {
		return v, errRoleResponse
	}
	valid := roleNameRE.MatchString(v.ResourceID) && roleTokenRE.MatchString(v.ResultChangeToken)
	if member {
		valid = validCustomModelID(v.ResourceID) && memberRoleTokenRE.MatchString(v.ResultChangeToken)
	}
	if v.IdempotencyKey != key || !valid {
		return v, errRoleResponse
	}
	if _, e = customModelBool(o["resource_exists"]); e != nil {
		return v, errRoleResponse
	}
	if string(o["result_matches_current_state"]) != "null" {
		b, e := customModelBool(o["result_matches_current_state"])
		if e != nil {
			return v, errRoleResponse
		}
		v.ResultMatchesCurrentState = &b
	}
	if !v.ResourceExists && (v.ResultMatchesCurrentState == nil || *v.ResultMatchesCurrentState) {
		return v, errRoleResponse
	}
	return v, nil
}

// The shared bounded request implementation never retries a mutation and marks
// only errors occurring before HTTP as local validation failures.
func (c *Client) roleRequest(ctx context.Context, method, path string, body any, codes ...int) (map[string]json.RawMessage, int, error) {
	raw, code, e := c.customModelRequest(ctx, method, path, body, codes...)
	if e != nil {
		return nil, code, e
	}
	v, e := roleEnvelope(raw)
	return v, code, e
}
func (c *Client) GetRoleCatalog(ctx context.Context) (RoleCatalog, error) {
	var out RoleCatalog
	env, _, e := c.roleRequest(ctx, http.MethodGet, "/management/v1/roles", nil, 200)
	if e != nil {
		return out, e
	}
	if _, e = customModelObject(env["meta"]); e != nil {
		return out, errRoleResponse
	}
	o, e := customModelObject(env["data"], "roles", "permissions")
	if e != nil {
		return out, errRoleResponse
	}
	if out.Permissions, e = roleStrings(o["permissions"], 100); e != nil {
		return out, e
	}
	var rows []json.RawMessage
	if string(o["roles"]) == "null" || json.Unmarshal(o["roles"], &rows) != nil || len(rows) > 10000 {
		return out, errRoleResponse
	}
	out.Roles = []Role{}
	seen := map[string]bool{}
	org := ""
	for _, raw := range rows {
		v, e := decodeRole(raw)
		if e != nil || seen[v.Name] || org != "" && org != v.OrganizationUUID {
			return out, errRoleResponse
		}
		org = v.OrganizationUUID
		seen[v.Name] = true
		out.Roles = append(out.Roles, v)
	}
	sort.Slice(out.Roles, func(i, j int) bool { return out.Roles[i].Name < out.Roles[j].Name })
	return out, nil
}
func (c *Client) GetRole(ctx context.Context, name string) (Role, error) {
	if !assignableRole(name) {
		return Role{}, roleLocal("invalid role name")
	}
	env, _, e := c.roleRequest(ctx, http.MethodGet, "/management/v1/roles/"+name, nil, 200)
	if e != nil {
		return Role{}, e
	}
	if _, e = customModelObject(env["meta"]); e != nil {
		return Role{}, errRoleResponse
	}
	v, e := decodeRole(env["data"])
	if e == nil && v.Name != name {
		e = errRoleResponse
	}
	return v, e
}
func (c *Client) CreateRole(ctx context.Context, input RoleCreateInput) (RoleResult, error) {
	if !roleDefinitionValid(input.DisplayName, input.Permissions, input.Priority) || !customModelKeyRE.MatchString(input.IdempotencyKey) {
		return RoleResult{}, roleLocal("invalid custom role creation intent")
	}
	env, code, e := c.roleRequest(ctx, http.MethodPost, "/management/v1/roles", input, 201, 200)
	if e != nil {
		return RoleResult{}, e
	}
	result, e := decodeRoleResult(env, input.IdempotencyKey, "", code == 200)
	if e == nil && (result.Replayed != (code == 200) || !result.Replayed && !roleResultDefinitionMatches(result.Role, input.DisplayName, input.Permissions, input.Priority)) {
		e = errRoleResponse
	}
	return result, e
}
func (c *Client) UpdateRole(ctx context.Context, name string, input RoleUpdateInput) (RoleResult, error) {
	if !roleNameRE.MatchString(name) || !roleDefinitionValid(input.DisplayName, input.Permissions, input.Priority) || !roleTokenRE.MatchString(input.ExpectedChangeToken) || !customModelKeyRE.MatchString(input.IdempotencyKey) {
		return RoleResult{}, roleLocal("invalid custom role update intent")
	}
	env, _, e := c.roleRequest(ctx, http.MethodPut, "/management/v1/roles/"+name, input, 200)
	if e != nil {
		return RoleResult{}, e
	}
	result, e := decodeRoleResult(env, input.IdempotencyKey, name, false)
	if e == nil && !result.Replayed && !roleResultDefinitionMatches(result.Role, input.DisplayName, input.Permissions, input.Priority) {
		e = errRoleResponse
	}
	return result, e
}

func roleResultDefinitionMatches(role Role, label string, permissions []string, priority int64) bool {
	grants := append([]string{}, permissions...)
	sort.Strings(grants)
	grants = slices.Compact(grants)
	return role.DisplayName != nil && *role.DisplayName == strings.TrimSpace(label) && role.Priority == priority && slices.Equal(role.Permissions, grants)
}
func decodeRoleResult(env map[string]json.RawMessage, key, name string, createReplay bool) (RoleResult, error) {
	var out RoleResult
	meta, e := customModelObject(env["meta"], "operation", "idempotency_replayed")
	if e != nil {
		return out, errRoleResponse
	}
	if out.Replayed, e = customModelBool(meta["idempotency_replayed"]); e != nil {
		return out, errRoleResponse
	}
	if out.Operation, e = decodeRoleReceipt(meta["operation"], key, false); e != nil {
		return out, e
	}
	if out.Role, e = decodeRole(env["data"]); e != nil {
		return out, e
	}
	if out.Role.Name != out.Operation.ResourceID || name != "" && out.Role.Name != name || !out.Role.Custom || !out.Operation.ResourceExists || !out.Replayed && out.Role.ChangeToken != out.Operation.ResultChangeToken || createReplay && !out.Replayed {
		return out, errRoleResponse
	}
	return out, nil
}
func (c *Client) DeleteRole(ctx context.Context, name, token string) error {
	if !roleNameRE.MatchString(name) || !roleTokenRE.MatchString(token) {
		return roleLocal("invalid custom role deletion intent")
	}
	_, _, e := c.customModelRequest(ctx, http.MethodDelete, "/management/v1/roles/"+name, map[string]string{"expected_change_token": token}, 204)
	return e
}
func (c *Client) GetRoleOperation(ctx context.Context, kind, key string) (RoleReceipt, error) {
	if kind != "create" && kind != "update" || !customModelKeyRE.MatchString(key) {
		return RoleReceipt{}, roleLocal("invalid role operation identity")
	}
	env, _, e := c.roleRequest(ctx, http.MethodGet, "/management/v1/role-operations/"+kind+"/"+key, nil, 200)
	if e != nil {
		return RoleReceipt{}, e
	}
	if _, e = customModelObject(env["meta"]); e != nil {
		return RoleReceipt{}, errRoleResponse
	}
	return decodeRoleReceipt(env["data"], key, false)
}
func (c *Client) GetMemberRole(ctx context.Context, id string) (MemberRole, error) {
	if !validCustomModelID(id) {
		return MemberRole{}, roleLocal("invalid member UUID")
	}
	env, _, e := c.roleRequest(ctx, http.MethodGet, "/management/v1/members/"+id+"/role", nil, 200)
	if e != nil {
		return MemberRole{}, e
	}
	if _, e = customModelObject(env["meta"]); e != nil {
		return MemberRole{}, errRoleResponse
	}
	v, e := decodeMemberRole(env["data"])
	if e == nil && v.MemberUUID != id {
		e = errRoleResponse
	}
	return v, e
}
func (c *Client) SetMemberRole(ctx context.Context, id string, input MemberRoleInput) (MemberRoleResult, error) {
	var out MemberRoleResult
	if !validCustomModelID(id) || !assignableRole(input.Role) || !memberRoleTokenRE.MatchString(input.ExpectedChangeToken) || !roleTokenRE.MatchString(input.ExpectedRoleChangeToken) || !customModelKeyRE.MatchString(input.IdempotencyKey) {
		return out, roleLocal("invalid member role assignment intent")
	}
	env, _, e := c.roleRequest(ctx, http.MethodPut, "/management/v1/members/"+id+"/role", input, 200)
	if e != nil {
		return out, e
	}
	meta, e := customModelObject(env["meta"], "operation", "idempotency_replayed")
	if e != nil {
		return out, errRoleResponse
	}
	if out.Replayed, e = customModelBool(meta["idempotency_replayed"]); e != nil {
		return out, errRoleResponse
	}
	if out.Operation, e = decodeRoleReceipt(meta["operation"], input.IdempotencyKey, true); e != nil {
		return out, e
	}
	if out.MemberRole, e = decodeMemberRole(env["data"]); e != nil {
		return out, e
	}
	if out.MemberRole.MemberUUID != id || out.Operation.ResourceID != id || !out.Operation.ResourceExists || !out.Replayed && (out.MemberRole.ChangeToken != out.Operation.ResultChangeToken || out.MemberRole.Role != input.Role || out.MemberRole.Owner || out.MemberRole.Blocked || out.MemberRole.DirectoryManaged) {
		return out, errRoleResponse
	}
	return out, nil
}
func (c *Client) GetMemberRoleOperation(ctx context.Context, key string) (RoleReceipt, error) {
	if !customModelKeyRE.MatchString(key) {
		return RoleReceipt{}, roleLocal("invalid member role operation identity")
	}
	env, _, e := c.roleRequest(ctx, http.MethodGet, "/management/v1/member-role-operations/"+key, nil, 200)
	if e != nil {
		return RoleReceipt{}, e
	}
	if _, e = customModelObject(env["meta"]); e != nil {
		return RoleReceipt{}, errRoleResponse
	}
	return decodeRoleReceipt(env["data"], key, true)
}
