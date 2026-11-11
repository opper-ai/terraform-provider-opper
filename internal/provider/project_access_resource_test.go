package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func projectAccessCLIConfig(api *projectAccessTestAPI, org, project, mode, roles, users string) string {
	return fmt.Sprintf(`provider "opper" {
 management_api_key = "op-mak-local-test"
 base_url = %q
}
resource "opper_project_access" "test" {
 organization_uuid = %q
 project_uuid = %q
 mode = %q
 role_keys = %s
 user_uuids = %s
}
`, api.server.URL, org, project, mode, roles, users)
}

func projectAccessAttributes(t *testing.T, h *customModelCLI) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(h.dir, "terraform.tfstate"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Resources []struct {
			Type      string
			Instances []struct{ Attributes map[string]any }
		}
	}
	if json.Unmarshal(b, &state) != nil {
		t.Fatal("invalid Terraform state")
	}
	for _, r := range state.Resources {
		if r.Type == "opper_project_access" && len(r.Instances) != 0 {
			return r.Instances[0].Attributes
		}
	}
	return nil
}

func assertProjectAccessWrites(t *testing.T, api *projectAccessTestAPI, wantPuts, wantWrites int) {
	t.Helper()
	puts, writes := api.counts()
	if puts != wantPuts || writes != wantWrites {
		t.Fatalf("PUT attempts/writes = %d/%d; want %d/%d", puts, writes, wantPuts, wantWrites)
	}
}

func TestProjectAccessResourceLifecycle(t *testing.T) {
	api := newProjectAccessTestAPI(t)
	first := projectAccessCLIConfig(api, projectAccessTestOrg, projectAccessTestProject, "restricted", `["viewer", "developer"]`, `[]`)
	h := newCustomModelCLI(t, first)
	h.run("", "", "apply", "-auto-approve", "-input=false", "-no-color")
	attrs := projectAccessAttributes(t, h)
	if attrs["id"] != projectAccessTestProject || attrs["mode"] != "restricted" || attrs["organization_uuid"] != projectAccessTestOrg {
		t.Fatalf("unexpected persisted policy: %#v", attrs)
	}
	assertProjectAccessWrites(t, api, 1, 1)
	out := h.run("", "", "plan", "-detailed-exitcode", "-input=false", "-no-color")
	if !strings.Contains(out, "No changes") {
		t.Fatal("unchanged policy did not produce an empty plan")
	}
	changed := projectAccessCLIConfig(api, projectAccessTestOrg, projectAccessTestProject, "restricted", `["viewer"]`, fmt.Sprintf(`[%q]`, projectAccessTestUser))
	h.write(changed)
	h.run("", "", "apply", "-auto-approve", "-input=false", "-no-color")
	assertProjectAccessWrites(t, api, 2, 2)
	h.run("", "", "state", "rm", "opper_project_access.test")
	h.run("", "", "import", "-input=false", "-no-color", "opper_project_access.test", projectAccessTestProject)
	h.run("", "", "plan", "-detailed-exitcode", "-input=false", "-no-color")
	assertProjectAccessWrites(t, api, 2, 2)
	api.mu.Lock()
	requests := len(api.requests)
	api.mu.Unlock()
	h.run("", "", "destroy", "-refresh=false", "-auto-approve", "-input=false", "-no-color")
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.requests) != requests || api.projects[projectAccessTestProject].Mode != "restricted" || api.projects[projectAccessTestProject].UserUUIDs[0] != projectAccessTestUser {
		t.Fatal("destroy contacted Opper or changed the retained policy")
	}
}

func TestProjectAccessResourceSavedPlanRefusesConcurrentChanges(t *testing.T) {
	for _, scenario := range []struct {
		name             string
		initial, cascade bool
	}{{"adoption_after_policy_ABA", true, false}, {"update_after_policy_edit", false, false}, {"update_after_membership_cascade", false, true}} {
		t.Run(scenario.name, func(t *testing.T) {
			api := newProjectAccessTestAPI(t)
			users := `[]`
			if !scenario.initial {
				users = fmt.Sprintf(`[%q]`, projectAccessTestUser)
			}
			h := newCustomModelCLI(t, projectAccessCLIConfig(api, projectAccessTestOrg, projectAccessTestProject, "restricted", `["viewer"]`, users))
			if !scenario.initial {
				h.run("", "", "apply", "-auto-approve", "-input=false", "-no-color")
				h.write(projectAccessCLIConfig(api, projectAccessTestOrg, projectAccessTestProject, "restricted", `["developer"]`, `[]`))
			}
			h.run("", "", "plan", "-out=reviewed.tfplan", "-input=false", "-no-color")
			beforePuts, beforeWrites := api.counts()
			wantMode, wantRoles := "restricted", []string{"admin"}
			if scenario.initial {
				// A policy may return to Organization while the reviewed token
				// remains stale. This exercises the adoption validator itself,
				// rather than relying on the Restricted import guard.
				api.edit(projectAccessTestProject, "restricted", []string{"admin"}, []string{}, true)
				wantMode, wantRoles = "organization", []string{}
				api.edit(projectAccessTestProject, wantMode, wantRoles, []string{}, true)
			} else if scenario.cascade {
				// Removing the direct member grant changes the full token but
				// not the project revision or its existing role grant.
				wantRoles = []string{"viewer"}
				api.edit(projectAccessTestProject, wantMode, wantRoles, []string{}, false)
			} else {
				api.edit(projectAccessTestProject, wantMode, wantRoles, []string{}, true)
			}
			h.run("", `(?i)changed|conflict|stale|409|review`, "apply", "-input=false", "-no-color", "reviewed.tfplan")
			_, writes := api.counts()
			if writes != beforeWrites {
				t.Fatal("stale saved plan overwrote the concurrent policy")
			}
			puts, _ := api.counts()
			if puts > beforePuts+1 {
				t.Fatal("stale apply retried a mutation")
			}
			api.mu.Lock()
			p := api.projects[projectAccessTestProject]
			retained := p.Mode == wantMode && slices.Equal(p.RoleKeys, wantRoles) && len(p.UserUUIDs) == 0
			api.mu.Unlock()
			if !retained {
				t.Fatal("concurrent grants were not preserved")
			}
		})
	}
}

func TestProjectAccessResourceRequiresRestrictedImportAndKnownIdentity(t *testing.T) {
	api := newProjectAccessTestAPI(t)
	api.edit(projectAccessTestProject, "restricted", []string{"admin"}, []string{}, true)
	h := newCustomModelCLI(t, projectAccessCLIConfig(api, projectAccessTestOrg, projectAccessTestProject, "restricted", `["viewer"]`, `[]`))
	h.run("", `(?i)import`, "plan", "-input=false", "-no-color")
	assertProjectAccessWrites(t, api, 0, 0)
	h.run("", "", "import", "-input=false", "-no-color", "opper_project_access.test", projectAccessTestProject)
	h.run("", "", "apply", "-auto-approve", "-input=false", "-no-color")
	assertProjectAccessWrites(t, api, 1, 1)
	config := projectAccessCLIConfig(api, projectAccessTestOrg, projectAccessOtherProject, "restricted", `[]`, `[]`)
	config = strings.Replace(config, fmt.Sprintf("project_uuid = %q", projectAccessOtherProject), "project_uuid = opper_project.pending.id", 1)
	config += `resource "opper_project" "pending" { name = "not-created-during-plan" }`
	h2 := newCustomModelCLI(t, config)
	h2.run("", `(?i)known|existing|separate.*plan`, "plan", "-input=false", "-no-color")
	assertProjectAccessWrites(t, api, 1, 1)
}

func TestProjectAccessResourceDriftCanBeReviewedAndOrganizationAccessExplicitlyRestored(t *testing.T) {
	api := newProjectAccessTestAPI(t)
	h := newCustomModelCLI(t, projectAccessCLIConfig(api, projectAccessTestOrg, projectAccessTestProject, "restricted", `["viewer"]`, `[]`))
	h.run("", "", "apply", "-auto-approve", "-input=false", "-no-color")
	api.edit(projectAccessTestProject, "restricted", []string{"developer"}, []string{}, true)
	h.run("", "", "plan", "-out=reviewed.tfplan", "-input=false", "-no-color")
	h.run("", "", "apply", "-input=false", "-no-color", "reviewed.tfplan")
	assertProjectAccessWrites(t, api, 2, 2)
	h.write(projectAccessCLIConfig(api, projectAccessTestOrg, projectAccessTestProject, "organization", `[]`, `[]`))
	h.run("", "", "apply", "-auto-approve", "-input=false", "-no-color")
	assertProjectAccessWrites(t, api, 3, 3)
	if attrs := projectAccessAttributes(t, h); attrs["mode"] != "organization" {
		t.Fatal("explicit Organization update was not persisted")
	}
}

func TestProjectAccessResourceIdentityChangeAndReadFailurePreserveState(t *testing.T) {
	api := newProjectAccessTestAPI(t)
	config := projectAccessCLIConfig(api, projectAccessTestOrg, projectAccessTestProject, "restricted", `["viewer"]`, `[]`)
	h := newCustomModelCLI(t, config)
	h.run("", "", "apply", "-auto-approve", "-input=false", "-no-color")
	for _, code := range []int{403, 404, 503} {
		api.mu.Lock()
		api.readFailure = code
		api.mu.Unlock()
		h.run("", fmt.Sprint(code), "plan", "-input=false", "-no-color")
		if attrs := projectAccessAttributes(t, h); attrs["id"] != projectAccessTestProject || attrs["mode"] != "restricted" {
			t.Fatal("failed read discarded policy state")
		}
	}
	api.mu.Lock()
	api.readFailure = 0
	api.projects[projectAccessTestProject].OrganizationUUID = projectAccessOtherOrg
	api.mu.Unlock()
	h.run("", `(?i)organization|identity`, "plan", "-input=false", "-no-color")
	if projectAccessAttributes(t, h)["organization_uuid"] != projectAccessTestOrg {
		t.Fatal("foreign-organization response replaced state")
	}
	api.mu.Lock()
	api.projects[projectAccessTestProject].OrganizationUUID = projectAccessTestOrg
	api.mu.Unlock()
	api.edit(projectAccessOtherProject, "restricted", []string{"admin"}, []string{}, true)
	h.write(projectAccessCLIConfig(api, projectAccessTestOrg, projectAccessOtherProject, "restricted", `["viewer"]`, `[]`))
	h.run("", `(?i)import`, "plan", "-input=false", "-no-color")
	assertProjectAccessWrites(t, api, 1, 1)
	if projectAccessAttributes(t, h)["id"] != projectAccessTestProject {
		t.Fatal("invalid replacement forgot the original policy")
	}
}

func TestProjectAccessResourceSavedPlanCannotFollowCredentialOrganizationChange(t *testing.T) {
	api := newProjectAccessTestAPI(t)
	h := newCustomModelCLI(t, projectAccessCLIConfig(api, projectAccessTestOrg, projectAccessTestProject, "restricted", `["viewer"]`, `[]`))
	h.run("", "", "plan", "-out=reviewed.tfplan", "-input=false", "-no-color")
	api.mu.Lock()
	api.projects[projectAccessTestProject].OrganizationUUID = projectAccessOtherOrg
	api.mu.Unlock()
	h.run("", `(?i)organization|identity`, "apply", "-input=false", "-no-color", "reviewed.tfplan")
	assertProjectAccessWrites(t, api, 0, 0)
	if projectAccessAttributes(t, h) != nil {
		t.Fatal("foreign organization was adopted through a saved plan")
	}
}

func TestProjectAccessResourceCannotReplaceBoundIdentity(t *testing.T) {
	api := newProjectAccessTestAPI(t)
	h := newCustomModelCLI(t, projectAccessCLIConfig(api, projectAccessTestOrg, projectAccessTestProject, "restricted", `["viewer"]`, `[]`))
	h.run("", "", "apply", "-auto-approve", "-input=false", "-no-color")
	// B is an Organization policy, so this cannot rely on the separate
	// Restricted-import guard. Replacing the address must be refused while A
	// remains owned, before Terraform can schedule a retain-only destroy of A.
	h.write(projectAccessCLIConfig(api, projectAccessTestOrg, projectAccessOtherProject, "restricted", `["developer"]`, `[]`))
	h.run("", `(?i)identity cannot change|separate resource address`, "plan", "-out=retarget.tfplan", "-input=false", "-no-color")
	if attrs := projectAccessAttributes(t, h); attrs["id"] != projectAccessTestProject || attrs["organization_uuid"] != projectAccessTestOrg || attrs["mode"] != "restricted" {
		t.Fatal("rejected retargeting discarded the original owned policy")
	}
	// Terraform may retain an errored plan artifact. It must be unapplyable,
	// even if B's identity or availability changes after the rejected plan.
	api.mu.Lock()
	api.projects[projectAccessOtherProject].OrganizationUUID = projectAccessOtherOrg
	api.readFailure = 503
	beforeRequests := len(api.requests)
	api.mu.Unlock()
	h.run("", `(?i)cannot apply|errored|failed to load|no such file|could not.*plan`, "apply", "-input=false", "-no-color", "retarget.tfplan")
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.requests) != beforeRequests || api.puts != 1 || api.writes != 1 {
		t.Fatal("rejected retargeting contacted or modified the destination")
	}
	if a, b := api.projects[projectAccessTestProject], api.projects[projectAccessOtherProject]; a.Mode != "restricted" || !slices.Equal(a.RoleKeys, []string{"viewer"}) || b.Mode != "organization" {
		t.Fatal("identity change altered either retained remote policy")
	}
}

func TestProjectAccessResourceUncertainResponseNeverRetriesOrClaimsSuccess(t *testing.T) {
	for _, update := range []bool{false, true} {
		for _, malformed := range []bool{false, true} {
			t.Run(fmt.Sprintf("update=%v/malformed=%v", update, malformed), func(t *testing.T) {
				api := newProjectAccessTestAPI(t)
				h := newCustomModelCLI(t, projectAccessCLIConfig(api, projectAccessTestOrg, projectAccessTestProject, "restricted", `["viewer"]`, `[]`))
				if update {
					h.run("", "", "apply", "-auto-approve", "-input=false", "-no-color")
					h.write(projectAccessCLIConfig(api, projectAccessTestOrg, projectAccessTestProject, "restricted", `["developer"]`, `[]`))
				}
				api.mu.Lock()
				api.failAfterWrite, api.malformedAfterWrite = !malformed, malformed
				api.mu.Unlock()
				before, _ := api.counts()
				h.run("", `(?i)uncertain|inspect|review|503|invalid.*response`, "apply", "-auto-approve", "-input=false", "-no-color")
				puts, _ := api.counts()
				if puts != before+1 {
					t.Fatal("uncertain mutation was retried")
				}
				attrs := projectAccessAttributes(t, h)
				if !update && attrs != nil {
					t.Fatal("uncertain initial write claimed Terraform ownership")
				}
				if update && (attrs["mode"] != "restricted" || attrs["role_keys"].([]any)[0] != "viewer") {
					t.Fatal("uncertain update did not preserve previous state")
				}
				if !update {
					h.run("", `(?i)import`, "plan", "-input=false", "-no-color")
				}
			})
		}
	}
}

func TestProjectAccessResourceDisabledWritesAndInvalidPolicyDoNotChangeRemote(t *testing.T) {
	api := newProjectAccessTestAPI(t)
	h := newCustomModelCLI(t, projectAccessCLIConfig(api, projectAccessTestOrg, projectAccessTestProject, "restricted", `["viewer"]`, `[]`))
	api.mu.Lock()
	api.putFailure = 403
	api.mu.Unlock()
	h.run("", `403`, "apply", "-auto-approve", "-input=false", "-no-color")
	assertProjectAccessWrites(t, api, 1, 0)
	h.write(projectAccessCLIConfig(api, projectAccessTestOrg, projectAccessTestProject, "organization", `["viewer"]`, `[]`))
	h.run("", `(?i)organization|empty|recipient`, "plan", "-input=false", "-no-color")
	assertProjectAccessWrites(t, api, 1, 0)
}
