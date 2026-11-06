package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func rolesCLIConfig(api *rolesTestAPI, label, grants, mode string, member bool) string {
	extra := ""
	if member {
		fallback := ""
		if mode == "assign_role" {
			fallback = "destroy_role = \"viewer\"\n destroy_role_change_token = data.opper_role.viewer.change_token"
		}
		extra = fmt.Sprintf(`
resource "opper_member_role" "test" {
 organization_uuid = %q
 member_uuid = %q
 role = opper_custom_role.test.name
 role_change_token = opper_custom_role.test.change_token
 destroy_behavior = %q
 %s
}
data "opper_member_role" "test" {
 organization_uuid = %q
 member_uuid = %q
 depends_on = [opper_member_role.test]
 }
`, rolesTestOrg, rolesTestMember, mode, fallback, rolesTestOrg, rolesTestMember)
	}
	return fmt.Sprintf(`provider "opper" {
 management_api_key = "op-mak-local-test"
base_url = %q
}
resource "opper_custom_role" "test" {
organization_uuid = %q
display_name = %q
permissions = %s
creation_idempotency_key = "role-fixture-create"
}
data "opper_role" "viewer" {
 organization_uuid = %q
name = "viewer"
 }
data "opper_roles" "all" {
 organization_uuid = %q
 }
%s`, api.server.URL, rolesTestOrg, label, grants, rolesTestOrg, rolesTestOrg, extra)
}
func rolesAttributes(t *testing.T, h *customModelCLI, kind string) map[string]any {
	t.Helper()
	b, e := os.ReadFile(filepath.Join(h.dir, "terraform.tfstate"))
	if e != nil {
		t.Fatal(e)
	}
	var v struct {
		Resources []struct {
			Mode, Type string
			Instances  []struct{ Attributes map[string]any }
		}
	}
	if json.Unmarshal(b, &v) != nil {
		t.Fatal("invalid terraform state")
	}
	for _, r := range v.Resources {
		if r.Mode == "managed" && r.Type == kind && len(r.Instances) > 0 {
			return r.Instances[0].Attributes
		}
	}
	t.Fatalf("missing %s", kind)
	return nil
}
func TestRolesResourceComposedLifecycle(t *testing.T) {
	api := newRolesTestAPI(t)
	h := newCustomModelCLI(t, rolesCLIConfig(api, "Operations", `["usage:read"]`, "assign_role", true))
	h.run("", "", "apply", "-auto-approve", "-input=false", "-no-color")
	role := rolesAttributes(t, h, "opper_custom_role")
	member := rolesAttributes(t, h, "opper_member_role")
	if member["role"] != role["name"] || member["operation_pending"] != false {
		t.Fatal("composed role assignment not confirmed")
	}
	h.run("", "", "plan", "-input=false", "-no-color", "-detailed-exitcode")
	h.write(rolesCLIConfig(api, "Operations renamed", `["projects:write","usage:read"]`, "assign_role", true))
	h.run("", "", "apply", "-auto-approve", "-input=false", "-no-color")
	h.run("", "", "plan", "-input=false", "-no-color", "-detailed-exitcode")
	after := rolesAttributes(t, h, "opper_member_role")
	if after["role_change_token"] != rolesAttributes(t, h, "opper_custom_role")["change_token"] {
		t.Fatal("same-role definition update was not pinned and confirmed")
	}
	h.run("", "", "destroy", "-auto-approve", "-input=false", "-no-color")
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.member.Role != "viewer" || len(api.roles) != 3 || api.assignments != 3 || api.creates != 1 || api.roleUpdates != 1 || api.deletes != 1 {
		t.Fatalf("unexpected workflow counts: roles%d member%s creates%d updates%d assignments%d deletes%d", len(api.roles), api.member.Role, api.creates, api.roleUpdates, api.assignments, api.deletes)
	}
}
func TestRolesResourceSavedPlanConflict(t *testing.T) {
	api := newRolesTestAPI(t)
	h := newCustomModelCLI(t, rolesCLIConfig(api, "First", `["usage:read"]`, "retain", false))
	h.run("", "", "apply", "-auto-approve", "-input=false", "-no-color")
	h.write(rolesCLIConfig(api, "Second", `["usage:read"]`, "retain", false))
	h.run("", "", "plan", "-input=false", "-no-color", "-out=saved.tfplan")
	id := rolesAttributes(t, h, "opper_custom_role")["name"].(string)
	api.mu.Lock()
	v := api.roles[id]
	v.ChangeToken = api.nextToken("orv1_")
	label := "External"
	v.DisplayName = &label
	api.roles[id] = v
	api.mu.Unlock()
	h.run("", `(?i)409|changed`, "apply", "-input=false", "-no-color", "saved.tfplan")
	h.run("", `(?i)changed|import`, "plan", "-input=false", "-no-color")
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.roleUpdates != 0 {
		t.Fatal("saved role plan overwrote external definition")
	}
}
func TestRolesResourceRetainDestroyDoesNotAssign(t *testing.T) {
	api := newRolesTestAPI(t)
	h := newCustomModelCLI(t, rolesCLIConfig(api, "Retained", `["usage:read"]`, "retain", true))
	h.run("", "", "apply", "-auto-approve", "-input=false", "-no-color")
	h.run("", "", "destroy", "-target=opper_member_role.test", "-auto-approve", "-input=false", "-no-color")
	api.mu.Lock()
	count := api.assignments
	api.mu.Unlock()
	if count != 1 {
		t.Fatal("retain destroy assigned a fallback")
	}
	h.run("", `(?i)409|reference`, "destroy", "-auto-approve", "-input=false", "-no-color")
}
func TestRolesResourceImportDispositionAndDrift(t *testing.T) {
	api := newRolesTestAPI(t)
	h := newCustomModelCLI(t, rolesCLIConfig(api, "Imported", `["usage:read"]`, "assign_role", true))
	h.run("", "", "apply", "-auto-approve", "-input=false", "-no-color")
	h.run("", "", "state", "rm", "opper_member_role.test")
	h.run("", "", "import", "-input=false", "-no-color", "opper_member_role.test", rolesTestOrg+"/"+rolesTestMember)
	if rolesAttributes(t, h, "opper_member_role")["destroy_behavior"] != nil {
		t.Fatal("import invented historical destroy disposition")
	}
	h.run("", "", "apply", "-auto-approve", "-input=false", "-no-color")
	api.mu.Lock()
	api.member.ChangeToken = api.nextToken("omrv1_")
	api.mu.Unlock()
	h.run("", `(?i)changed|review|import`, "destroy", "-auto-approve", "-input=false", "-no-color")
}
func TestRolesResourceProtectedAndDispositionRefusals(t *testing.T) {
	for _, guard := range []string{"owner", "directory", "blocked"} {
		t.Run(guard, func(t *testing.T) {
			api := newRolesTestAPI(t)
			api.mu.Lock()
			api.member.Owner = guard == "owner"
			api.member.DirectoryManaged = guard == "directory"
			api.member.Blocked = guard == "blocked"
			api.mu.Unlock()
			h := newCustomModelCLI(t, rolesCLIConfig(api, "Protected", `["usage:read"]`, "retain", true))
			h.run("", `(?i)protected`, "plan", "-input=false", "-no-color")
			api.mu.Lock()
			defer api.mu.Unlock()
			if api.creates != 0 || api.assignments != 0 {
				t.Fatal("planning a protected member mutated state")
			}
		})
	}
	api := newRolesTestAPI(t)
	config := strings.Replace(rolesCLIConfig(api, "Missing fallback", `["usage:read"]`, "assign_role", true), "destroy_role = \"viewer\"", "", 1)
	h := newCustomModelCLI(t, config)
	h.run("", `(?i)fallback`, "plan", "-input=false", "-no-color")
}

func TestRolesResourceInterruptedUpdatesRecoverWithoutReplay(t *testing.T) {
	for _, kind := range []string{"role", "member"} {
		t.Run(kind, func(t *testing.T) {
			api := newRolesTestAPI(t)
			base := rolesCLIConfig(api, "Recovery", `["usage:read"]`, "assign_role", kind == "member")
			h := newCustomModelCLI(t, base)
			h.run("", "", "apply", "-auto-approve", "-input=false", "-no-color")
			next := rolesCLIConfig(api, "Recovery renamed", `["usage:read"]`, "assign_role", false)
			address := "opper_custom_role"
			if kind == "member" {
				next = strings.Replace(base, "role = opper_custom_role.test.name", `role = "viewer"`, 1)
				next = strings.Replace(next, "role_change_token = opper_custom_role.test.change_token", "role_change_token = data.opper_role.viewer.change_token", 1)
				address = "opper_member_role"
			}
			h.write(next)
			api.mu.Lock()
			api.dropRoleWrite = kind == "role"
			api.dropMemberWrite = kind == "member"
			api.mu.Unlock()
			h.run("", `(?i)recovery`, "apply", "-auto-approve", "-input=false", "-no-color")
			if rolesAttributes(t, h, address)["operation_pending"] != true {
				t.Fatal("uncertain operation identity was discarded")
			}
			api.mu.Lock()
			api.blockReceipts = false
			beforeRole, beforeMember := api.roleUpdates, api.assignments
			api.mu.Unlock()
			h.run("", "", "apply", "-auto-approve", "-input=false", "-no-color")
			if rolesAttributes(t, h, address)["operation_pending"] != false {
				t.Fatal("pure receipt recovery did not complete")
			}
			api.mu.Lock()
			if beforeRole != api.roleUpdates || beforeMember != api.assignments {
				t.Fatal("receipt recovery replayed a write")
			}
			api.mu.Unlock()
			h.run("", "", "destroy", "-auto-approve", "-input=false", "-no-color")
		})
	}
}

func TestRolesResourceSavedMemberPlanAndFallbackConflicts(t *testing.T) {
	api := newRolesTestAPI(t)
	base := rolesCLIConfig(api, "Member saved plan", `["usage:read"]`, "assign_role", true)
	h := newCustomModelCLI(t, base)
	h.run("", "", "apply", "-auto-approve", "-input=false", "-no-color")
	next := strings.Replace(base, "role = opper_custom_role.test.name", `role = "viewer"`, 1)
	next = strings.Replace(next, "role_change_token = opper_custom_role.test.change_token", "role_change_token = data.opper_role.viewer.change_token", 1)
	h.write(next)
	h.run("", "", "plan", "-input=false", "-no-color", "-out=member.tfplan")
	api.mu.Lock()
	before := api.assignments
	api.member.ChangeToken = api.nextToken("omrv1_")
	api.mu.Unlock()
	h.run("", `(?i)409|changed`, "apply", "-input=false", "-no-color", "member.tfplan")
	api.mu.Lock()
	if api.assignments != before {
		t.Fatal("saved member plan overwrote a changed incarnation")
	}
	api.mu.Unlock()
	h.write(base)
	h.run("", "", "state", "rm", "opper_member_role.test")
	h.run("", "", "import", "-input=false", "-no-color", "opper_member_role.test", rolesTestOrg+"/"+rolesTestMember)
	h.run("", "", "apply", "-auto-approve", "-input=false", "-no-color")
	h.run("", "", "plan", "-destroy", "-input=false", "-no-color", "-out=destroy.tfplan")
	api.mu.Lock()
	v := api.roles["viewer"]
	v.ChangeToken = api.nextToken("orv1_")
	api.roles["viewer"] = v
	api.mu.Unlock()
	h.run("", `(?i)changed|review`, "apply", "-input=false", "-no-color", "destroy.tfplan")
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.assignments != before {
		t.Fatal("saved destroy substituted a fresh fallback definition")
	}
}

func TestRolesResourceInitialMemberSavedPlanPinsSource(t *testing.T) {
	api := newRolesTestAPI(t)
	h := newCustomModelCLI(t, rolesCLIConfig(api, "Initial saved", `["usage:read"]`, "retain", true))
	h.run("", "", "plan", "-input=false", "-no-color", "-out=initial.tfplan")
	api.mu.Lock()
	api.member.ChangeToken = api.nextToken("omrv1_")
	api.mu.Unlock()
	h.run("", `(?i)changed|review`, "apply", "-input=false", "-no-color", "initial.tfplan")
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.assignments != 0 {
		t.Fatal("initial saved plan refreshed a changed membership baseline")
	}
}

func TestRolesResourceUncertainCreateRequiresExplicitImport(t *testing.T) {
	for _, kind := range []string{"role", "member"} {
		t.Run(kind, func(t *testing.T) {
			api := newRolesTestAPI(t)
			api.mu.Lock()
			api.dropRoleWrite = kind == "role"
			api.dropMemberWrite = kind == "member"
			api.mu.Unlock()
			h := newCustomModelCLI(t, rolesCLIConfig(api, "Uncertain initial", `["usage:read"]`, "assign_role", kind == "member"))
			h.run("", `(?i)recovery|import`, "apply", "-auto-approve", "-input=false", "-no-color")
			resourceType := "opper_custom_role"
			if kind == "member" {
				resourceType = "opper_member_role"
			}
			if rolesAttributes(t, h, resourceType)["operation_pending"] != true {
				t.Fatal("uncertain creation lost its pending state")
			}
			api.mu.Lock()
			api.blockReceipts = false
			beforeCreate, beforeAssign := api.creates, api.assignments
			api.mu.Unlock()
			h.run("", `(?i)import`, "plan", "-input=false", "-no-color")
			api.mu.Lock()
			if api.creates != beforeCreate || api.assignments != beforeAssign {
				t.Fatal("pending initial operation was replayed")
			}
			api.mu.Unlock()
		})
	}
}

func TestRolesResourceFallbackRecoveryDoesNotReplay(t *testing.T) {
	api := newRolesTestAPI(t)
	h := newCustomModelCLI(t, rolesCLIConfig(api, "Fallback recovery", `["usage:read"]`, "assign_role", true))
	h.run("", "", "apply", "-auto-approve", "-input=false", "-no-color")
	api.mu.Lock()
	api.dropMemberWrite = true
	api.mu.Unlock()
	h.run("", `(?i)recovery`, "destroy", "-auto-approve", "-input=false", "-no-color")
	api.mu.Lock()
	api.blockReceipts = false
	before := api.assignments
	api.mu.Unlock()
	h.run("", "", "destroy", "-auto-approve", "-input=false", "-no-color")
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.assignments != before || api.member.Role != "viewer" || len(api.roles) != 3 {
		t.Fatal("fallback pure recovery replayed an assignment or failed cleanup")
	}
}

func TestRolesResourceReplacementReadFailureAndIdentityGuards(t *testing.T) {
	api := newRolesTestAPI(t)
	base := rolesCLIConfig(api, "Guarded", `["usage:read"]`, "retain", false)
	h := newCustomModelCLI(t, base)
	h.run("", "", "apply", "-auto-approve", "-input=false", "-no-color")
	h.run("", `(?i)already completed|fresh key`, "plan", "-replace=opper_custom_role.test", "-input=false", "-no-color")
	id := rolesAttributes(t, h, "opper_custom_role")["name"]
	api.mu.Lock()
	api.readFailure = 403
	api.mu.Unlock()
	h.run("", `(?i)403`, "plan", "-input=false", "-no-color")
	if rolesAttributes(t, h, "opper_custom_role")["name"] != id {
		t.Fatal("refused read discarded role state")
	}
	api.mu.Lock()
	api.readFailure = 0
	api.mu.Unlock()
	h.run("", "", "state", "rm", "opper_custom_role.test")
	h.run("", `(?i)built.in|invalid.*import`, "import", "-input=false", "-no-color", "opper_custom_role.test", "viewer")
	h.run("", "", "import", "-input=false", "-no-color", "opper_custom_role.test", id.(string))
	wrong := strings.ReplaceAll(base, rolesTestOrg, "33333333-3333-4333-8333-333333333333")
	h.write(wrong)
	h.run("", `(?i)organization|mismatch`, "plan", "-input=false", "-no-color")
}

func TestRolesResourceHardCrashUsesOriginalReceiptOnly(t *testing.T) {
	for _, kind := range []string{"role", "member"} {
		t.Run(kind, func(t *testing.T) {
			api := newRolesTestAPI(t)
			base := rolesCLIConfig(api, "Before crash", `["usage:read"]`, "assign_role", kind == "member")
			h := newCustomModelCLI(t, base)
			h.run("", "", "apply", "-auto-approve", "-input=false", "-no-color")
			path := filepath.Join(h.dir, "terraform.tfstate")
			old, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			next := rolesCLIConfig(api, "After crash", `["usage:read"]`, "assign_role", false)
			if kind == "member" {
				next = strings.Replace(base, "role = opper_custom_role.test.name", `role = "viewer"`, 1)
				next = strings.Replace(next, "role_change_token = opper_custom_role.test.change_token", "role_change_token = data.opper_role.viewer.change_token", 1)
			}
			h.write(next)
			h.run("", "", "apply", "-auto-approve", "-input=false", "-no-color")
			api.mu.Lock()
			beforeRole, beforeMember := api.roleUpdates, api.assignments
			api.mu.Unlock()
			if e = os.WriteFile(path, old, 0600); e != nil {
				t.Fatal(e)
			}
			h.run("", "", "apply", "-auto-approve", "-input=false", "-no-color")
			api.mu.Lock()
			if api.roleUpdates != beforeRole || api.assignments != beforeMember {
				t.Fatal("hard-crash recovery repeated a mutation")
			}
			if kind == "role" {
				id := fmt.Sprintf("custom_%032x", 1)
				v := api.roles[id]
				v.ChangeToken = api.nextToken("orv1_")
				api.roles[id] = v
			} else {
				api.member.ChangeToken = api.nextToken("omrv1_")
			}
			api.mu.Unlock()
			if e = os.WriteFile(path, old, 0600); e != nil {
				t.Fatal(e)
			}
			h.run("", `(?i)changed|import`, "apply", "-auto-approve", "-input=false", "-no-color")
			api.mu.Lock()
			defer api.mu.Unlock()
			if api.roleUpdates != beforeRole || api.assignments != beforeMember {
				t.Fatal("superseded historical receipt overwrote later state")
			}
		})
	}
}

func TestRolesResourceWrongOrganizationReplacementNeverDeletesOriginal(t *testing.T) {
	api := newRolesTestAPI(t)
	// Exclude guarded data sources: the resource itself must refuse its unsafe
	// replacement before Terraform performs the destructive half.
	base := strings.Split(rolesCLIConfig(api, "Original organization", `["usage:read"]`, "retain", false), `data "opper_role"`)[0]
	h := newCustomModelCLI(t, base)
	h.run("", "", "apply", "-auto-approve", "-input=false", "-no-color")
	id := rolesAttributes(t, h, "opper_custom_role")["name"].(string)
	wrong := strings.ReplaceAll(base, rolesTestOrg, "33333333-3333-4333-8333-333333333333")
	wrong = strings.Replace(wrong, "role-fixture-create", "fresh-wrong-org-key", 1)
	h.write(wrong)
	h.run("", `(?i)organization|mismatch`, "apply", "-auto-approve", "-input=false", "-no-color")
	api.mu.Lock()
	_, exists := api.roles[id]
	creates, deletes := api.creates, api.deletes
	api.mu.Unlock()
	if !exists || creates != 1 || deletes != 0 {
		t.Fatalf("wrong-org replacement deleted/recreated original: exists%v creates%d deletes%d", exists, creates, deletes)
	}
	if rolesAttributes(t, h, "opper_custom_role")["name"] != id {
		t.Fatal("wrong-org replacement discarded the original state")
	}
	h.write(base)
	h.run("", "", "plan", "-input=false", "-no-color", "-detailed-exitcode")
	h.run("", "", "destroy", "-auto-approve", "-input=false", "-no-color")
}

func memberOnlyConfig(api *rolesTestAPI, org, member string) string {
	api.mu.Lock()
	admin, viewer := api.roles["admin"].ChangeToken, api.roles["viewer"].ChangeToken
	api.mu.Unlock()
	return fmt.Sprintf(`provider "opper" {
 management_api_key = "op-mak-local-test"
 base_url = %q
}
resource "opper_member_role" "test" {
 organization_uuid = %q
 member_uuid = %q
 role = "admin"
 role_change_token = %q
 destroy_behavior = "assign_role"
 destroy_role = "viewer"
 destroy_role_change_token = %q
}
`, api.server.URL, org, member, admin, viewer)
}

func TestRolesResourceWrongOrganizationMemberReplacementNeverAssignsFallback(t *testing.T) {
	api := newRolesTestAPI(t)
	h := newCustomModelCLI(t, memberOnlyConfig(api, rolesTestOrg, rolesTestMember))
	h.run("", "", "apply", "-auto-approve", "-input=false", "-no-color")
	h.write(memberOnlyConfig(api, "33333333-3333-4333-8333-333333333333", rolesTestMember))
	h.run("", `(?i)organization|mismatch`, "apply", "-auto-approve", "-input=false", "-no-color")
	api.mu.Lock()
	role, count := api.member.Role, api.assignments
	api.mu.Unlock()
	if role != "admin" || count != 1 {
		t.Fatalf("invalid replacement assigned oldmember fallback: role%s assignments%d", role, count)
	}
	if rolesAttributes(t, h, "opper_member_role")["organization_uuid"] != rolesTestOrg {
		t.Fatal("invalid replacement discarded original membership state")
	}
	h.write(memberOnlyConfig(api, rolesTestOrg, rolesTestMember))
	h.run("", "", "destroy", "-auto-approve", "-input=false", "-no-color")
}
