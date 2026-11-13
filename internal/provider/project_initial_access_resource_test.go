package provider

import (
	"fmt"
	"reflect"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

const initialAccessTestUser = "22222222-2222-4222-8222-222222222222"

func projectInitialAccessTestConfig(baseURL, name, description, access string) string {
	initial := ""
	if access != "" {
		initial = "initial_access = " + access
	}
	return fmt.Sprintf(`
provider "opper" {
  management_api_key = "op-mak-local-test"
  base_url = %q
}
resource "opper_project" "test" {
  name = %q
  description = %q
  %s
}
`, baseURL, name, description, initial)
}

func TestProjectInitialAccessLifecycleAndCreationHistory(t *testing.T) {
	requireTerraform(t)
	api := newProjectTestAPI(t)
	const address = "opper_project.test"
	seed := fmt.Sprintf(`{mode = "restricted", role_keys = ["viewer"], user_uuids = [%q]}`, initialAccessTestUser)
	first := projectInitialAccessTestConfig(api.server.URL, "restricted", "initial", seed)
	updated := projectInitialAccessTestConfig(api.server.URL, "renamed", "updated", seed)
	removed := projectInitialAccessTestConfig(api.server.URL, "renamed", "updated", "")
	var id string
	check := func(creates, updates int, policy client.InitialProjectAccess) resource.TestCheckFunc {
		return func(s *terraform.State) error {
			api.mu.Lock()
			defer api.mu.Unlock()
			actualID := s.RootModule().Resources[address].Primary.ID
			if id == "" {
				id = actualID
			}
			if actualID != id || api.creates != creates || api.createRequests != creates || api.updates != updates || !reflect.DeepEqual(api.initialAccess[id], policy) {
				return fmt.Errorf("project identity/write/policy mismatch: id=%s creates=%d requests=%d updates=%d policy=%+v", actualID, api.creates, api.createRequests, api.updates, api.initialAccess[id])
			}
			if api.lastPatchFields["access"] {
				return fmt.Errorf("metadata update sent an access policy")
			}
			return nil
		}
	}
	initialPolicy := client.InitialProjectAccess{Mode: "restricted", RoleKeys: []string{"viewer"}, UserUUIDs: []string{initialAccessTestUser}}
	externalPolicy := client.InitialProjectAccess{Mode: "organization", RoleKeys: []string{}, UserUUIDs: []string{}}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true,
		Steps: []resource.TestStep{
			{Config: first, Check: resource.ComposeTestCheckFunc(check(1, 0, initialPolicy), resource.TestCheckResourceAttr(address, "initial_access.mode", "restricted"), resource.TestCheckResourceAttr(address, "initial_access.role_keys.#", "1"), resource.TestCheckResourceAttr(address, "initial_access.user_uuids.#", "1"))},
			{Config: first, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: check(1, 0, initialPolicy)},
			{PreConfig: func() { api.mu.Lock(); api.initialAccess[id] = externalPolicy; api.mu.Unlock() }, Config: first, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: resource.ComposeTestCheckFunc(check(1, 0, externalPolicy), resource.TestCheckResourceAttr(address, "initial_access.mode", "restricted"))},
			{Config: updated, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)}}, Check: resource.ComposeTestCheckFunc(check(1, 1, externalPolicy), resource.TestCheckResourceAttr(address, "description", "updated"), resource.TestCheckResourceAttr(address, "initial_access.mode", "restricted"))},
			{Config: removed, Check: resource.ComposeTestCheckFunc(check(1, 1, externalPolicy), resource.TestCheckNoResourceAttr(address, "initial_access.mode"))},
			{Config: removed, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: check(1, 1, externalPolicy)},
		},
		CheckDestroy: func(_ *terraform.State) error {
			api.mu.Lock()
			defer api.mu.Unlock()
			if len(api.projects) != 0 || api.deletes != 1 {
				return fmt.Errorf("expected project cleanup once, got projects=%d deletes=%d", len(api.projects), api.deletes)
			}
			return nil
		},
	})
}

func TestProjectInitialAccessRecipientsResolveAtApply(t *testing.T) {
	requireTerraform(t)
	api := newProjectTestAPI(t)
	config := projectInitialAccessTestConfig(api.server.URL, "unknown-recipients", "", `{
  mode = "restricted"
  role_keys = [terraform_data.recipients.output.role]
  user_uuids = [terraform_data.recipients.output.user]
}`) + fmt.Sprintf(`
resource "terraform_data" "recipients" {
  input = { role = "viewer", user = %q }
}
`, initialAccessTestUser)
	check := func(s *terraform.State) error {
		api.mu.Lock()
		defer api.mu.Unlock()
		id := s.RootModule().Resources["opper_project.test"].Primary.ID
		want := client.InitialProjectAccess{Mode: "restricted", RoleKeys: []string{"viewer"}, UserUUIDs: []string{initialAccessTestUser}}
		if api.createRequests != 1 || api.creates != 1 || !reflect.DeepEqual(api.initialAccess[id], want) {
			return fmt.Errorf("unknown recipients were not resolved into the single creation request: %+v", api.initialAccess[id])
		}
		return nil
	}
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, Steps: []resource.TestStep{
		{Config: config, Check: check},
		{Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: check},
	}})
}

func TestProjectInitialAccessCannotBeEditedOrAddedAfterCreation(t *testing.T) {
	requireTerraform(t)
	for _, seeded := range []bool{false, true} {
		t.Run(fmt.Sprintf("seeded_%t", seeded), func(t *testing.T) {
			api := newProjectTestAPI(t)
			initial := ""
			if seeded {
				initial = `{ mode = "restricted" }`
			}
			first := projectInitialAccessTestConfig(api.server.URL, "immutable-seed", "", initial)
			changed := projectInitialAccessTestConfig(api.server.URL, "would-change-metadata", "", `{ mode = "restricted", role_keys = ["viewer"] }`)
			resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, Steps: []resource.TestStep{
				{Config: first},
				{Config: changed, PlanOnly: true, ExpectError: regexp.MustCompile(`(?i)(initial.access|creation.only|create.time)`)},
				{Config: first, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: func(s *terraform.State) error {
					api.mu.Lock()
					defer api.mu.Unlock()
					id := s.RootModule().Resources["opper_project.test"].Primary.ID
					if api.creates != 1 || api.createRequests != 1 || api.updates != 0 || api.projects[id].Name != "immutable-seed" {
						return fmt.Errorf("seed refusal mutated/replaced the existing project")
					}
					return nil
				}},
			}})
		})
	}
}

func TestProjectInitialAccessImportDoesNotInventCreationHistory(t *testing.T) {
	requireTerraform(t)
	api := newProjectTestAPI(t)
	const id = "33333333-3333-4333-8333-333333333333"
	// Model a project created elsewhere. Import starts without Terraform
	// ownership, so the metadata response cannot invent an initial snapshot.
	api.projects[id] = client.Project{UUID: id, Name: "imported-project", Description: "", CreatedAt: "2026-11-03T09:00:00Z"}
	api.initialAccess[id] = client.InitialProjectAccess{Mode: "restricted", RoleKeys: []string{}, UserUUIDs: []string{}}
	seeded := projectInitialAccessTestConfig(api.server.URL, "imported-project", "", `{mode = "restricted"}`)
	unseeded := projectInitialAccessTestConfig(api.server.URL, "imported-project", "", "")
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, Steps: []resource.TestStep{
		{Config: unseeded, ResourceName: "opper_project.test", ImportState: true, ImportStatePersist: true, ImportStateId: id},
		{Config: unseeded, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: resource.TestCheckNoResourceAttr("opper_project.test", "initial_access.mode")},
		{Config: seeded, PlanOnly: true, ExpectError: regexp.MustCompile(`(?i)(initial.access|creation.only|create.time)`)},
		{Config: unseeded, Check: func(_ *terraform.State) error {
			api.mu.Lock()
			defer api.mu.Unlock()
			if api.createRequests != 0 || api.updates != 0 || api.initialAccess[id].Mode != "restricted" {
				return fmt.Errorf("import changed the current policy or recreated the project")
			}
			return nil
		}},
	}})
}

func TestProjectInitialAccessDenialDoesNotFallback(t *testing.T) {
	requireTerraform(t)
	api := newProjectTestAPI(t)
	api.createFailure = 403
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), Steps: []resource.TestStep{
		{Config: projectInitialAccessTestConfig(api.server.URL, "must-stay-absent", "", `{mode = "restricted"}`), ExpectError: regexp.MustCompile(`403`)},
	}})
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.createRequests != 1 || api.creates != 0 || len(api.projects) != 0 {
		t.Fatalf("denied atomic creation retried/fell back or created a project: requests=%d creates=%d projects=%d", api.createRequests, api.creates, len(api.projects))
	}
}

func TestProjectInitialAccessOrganizationRejectsRecipients(t *testing.T) {
	requireTerraform(t)
	api := newProjectTestAPI(t)
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), Steps: []resource.TestStep{
		{Config: projectInitialAccessTestConfig(api.server.URL, "invalid-organization", "", `{mode = "organization", role_keys = ["viewer"]}`), PlanOnly: true, ExpectError: regexp.MustCompile(`(?i)(organization|initial.access|empty)`)},
	}})
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.createRequests != 0 || len(api.projects) != 0 {
		t.Fatal("invalid Organization grant reached project creation")
	}
}
