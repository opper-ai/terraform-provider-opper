package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

const rolesPrivateKey = "organization_roles_v1"

type roleIntent struct {
	Kind, Key, ResourceID, ExpectedToken, RoleToken, Role string
	Definition                                            *client.RoleCreateInput
}
type rolePrivate struct {
	AckToken string
	Intent   *roleIntent
}

func loadRolePrivate(ctx context.Context, p customModelPrivateData) (rolePrivate, error) {
	var v rolePrivate
	b, d := p.GetKey(ctx, rolesPrivateKey)
	if d.HasError() || len(b) > 0 && json.Unmarshal(b, &v) != nil {
		return v, errors.New("invalid role recovery metadata; inspect and import the exact identity")
	}
	return v, nil
}
func saveRolePrivate(ctx context.Context, p customModelPrivateData, v rolePrivate) diag.Diagnostics {
	b, e := json.Marshal(v)
	if e != nil {
		return diag.Diagnostics{diag.NewErrorDiagnostic("Unable to save role recovery metadata", "Nonsecret recovery metadata could not be encoded.")}
	}
	return p.SetKey(ctx, rolesPrivateKey, b)
}
func roleOperationKey(kind, id, token string, intent any) string {
	b, _ := json.Marshal(struct {
		Kind, ID, Token string
		Intent          any
	}{kind, id, token, intent})
	h := sha256.Sum256(b)
	return "tf-role-" + hex.EncodeToString(h[:])
}
func roleReceiptMatches(r client.RoleReceipt, id string) bool {
	return r.ResourceID == id && r.ResourceExists && r.ResultMatchesCurrentState != nil && *r.ResultMatchesCurrentState
}
func roleUncertain(e error) bool { return !client.IsLocalValidationError(e) && deploymentUncertain(e) }
func roleOrg(got, want string) error {
	if got != want {
		return errors.New("the credential's organization differs from the configured organization; no role or membership is adopted or modified")
	}
	return nil
}
func confirmRole(ctx context.Context, c *client.Client, org, id, kind, key string, expected *client.RoleCreateInput) (client.Role, error) {
	var v client.Role
	r, e := c.GetRoleOperation(ctx, kind, key)
	if e != nil {
		return v, e
	}
	if !roleReceiptMatches(r, id) {
		return v, errors.New("original role result is absent, changed or unknown")
	}
	v, e = c.GetRole(ctx, id)
	if e != nil {
		return v, e
	}
	if e = roleOrg(v.OrganizationUUID, org); e != nil {
		return v, e
	}
	if r.ResultChangeToken != v.ChangeToken || expected != nil && (v.DisplayName == nil || *v.DisplayName != expected.DisplayName || v.Priority != expected.Priority || !slices.Equal(v.Permissions, expected.Permissions)) {
		return v, errors.New("current role is not the original operation result")
	}
	r, e = c.GetRoleOperation(ctx, kind, key)
	if e != nil || !roleReceiptMatches(r, id) || r.ResultChangeToken != v.ChangeToken {
		return v, errors.New("role changed while confirming the original result")
	}
	return v, nil
}
func confirmMemberRole(ctx context.Context, c *client.Client, org, id, key, desiredRole string) (client.MemberRole, error) {
	var v client.MemberRole
	r, e := c.GetMemberRoleOperation(ctx, key)
	if e != nil {
		return v, e
	}
	if !roleReceiptMatches(r, id) {
		return v, errors.New("original assignment result is absent, changed or unknown")
	}
	v, e = c.GetMemberRole(ctx, id)
	if e != nil {
		return v, e
	}
	if e = roleOrg(v.OrganizationUUID, org); e != nil {
		return v, e
	}
	if r.ResultChangeToken != v.ChangeToken || v.Role != desiredRole || v.Owner || v.Blocked || v.DirectoryManaged {
		return v, errors.New("current membership is not the original operation result")
	}
	r, e = c.GetMemberRoleOperation(ctx, key)
	if e != nil || !roleReceiptMatches(r, id) || r.ResultChangeToken != v.ChangeToken {
		return v, errors.New("membership changed while confirming the original result")
	}
	return v, nil
}
func roleRecoveryError() error {
	return fmt.Errorf("the confirmed role or membership baseline changed; inspect and explicitly import before further mutation; no fresh token or mutation retry is used")
}
