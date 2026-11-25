package provider

import (
	"fmt"
	"regexp"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const projectModelListAddress = "opper_project_model_list.test"

// projectModelListTestConfig renders the resource. An empty routes or
// enforced leaves that attribute out.
func projectModelListTestConfig(baseURL, project, list, routes, enforced string) string {
	extra := ""
	if routes != "" {
		extra += "\n compat_model_list_dynamic_routes = " + routes
	}
	if enforced != "" {
		extra += "\n compat_model_list_enforced = " + enforced
	}
	return fmt.Sprintf(`
provider "opper" {
 management_api_key = "op-mak-local-test"
 base_url = %q
}
resource "opper_project_model_list" "test" {
 project_uuid = %q
 compat_model_list = %q%s
}
`, baseURL, project, list, extra)
}

func projectModelListProviderOnly(baseURL string) string {
	return fmt.Sprintf("provider \"opper\" {\nmanagement_api_key=\"op-mak-local-test\"\nbase_url=%q\n}\n", baseURL)
}

// checkProjectModelList checks the server's list for a project; an empty
// list means the project follows the organization.
func checkProjectModelList(api *projectModelListTestAPI, project, list string, routes, enforced bool) resource.TestCheckFunc {
	return func(*terraform.State) error {
		api.mu.Lock()
		defer api.mu.Unlock()
		e := api.lists[project]
		if list == "" {
			if e != nil {
				return fmt.Errorf("project %s still has its own list %+v", project, *e)
			}
			return nil
		}
		if e == nil || e.list != list || e.routes != routes || e.enforce != enforced {
			return fmt.Errorf("project %s list %+v, want %s/%v/%v", project, e, list, routes, enforced)
		}
		return nil
	}
}

func checkProjectModelListWrites(api *projectModelListTestAPI, puts, deletes int) resource.TestCheckFunc {
	return func(*terraform.State) error {
		api.mu.Lock()
		defer api.mu.Unlock()
		if api.puts != puts || api.deletes != deletes {
			return fmt.Errorf("expected %d PUTs/%d DELETEs, got %d/%d", puts, deletes, api.puts, api.deletes)
		}
		return nil
	}
}

// checkProjectModelListLastPut checks the field names of the latest PUT body:
// an omitted enforced setting must never be sent.
func checkProjectModelListLastPut(api *projectModelListTestAPI, fields ...string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		api.mu.Lock()
		defer api.mu.Unlock()
		if len(api.putBodies) == 0 {
			return fmt.Errorf("no PUT was sent")
		}
		body := api.putBodies[len(api.putBodies)-1]
		if len(body) != len(fields) {
			return fmt.Errorf("PUT body %v, want fields %v", body, fields)
		}
		for _, field := range fields {
			if _, ok := body[field]; !ok {
				return fmt.Errorf("PUT body %v lacks %s", body, field)
			}
		}
		return nil
	}
}

func TestProjectModelListLifecycle(t *testing.T) {
	requireTerraform(t)
	api := newProjectModelListTestAPI(t)
	url := api.server.URL
	p := projectModelListTestUUID
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: resource.ComposeTestCheckFunc(checkProjectModelList(api, p, "", false, false), checkProjectModelListWrites(api, 5, 1)), Steps: []resource.TestStep{
		// Routes default to true; enforced is left out and reported.
		{Config: projectModelListTestConfig(url, p, "recommended", "", ""), Check: resource.ComposeTestCheckFunc(
			resource.TestCheckResourceAttr(projectModelListAddress, "id", p),
			resource.TestCheckResourceAttr(projectModelListAddress, "compat_model_list", "recommended"),
			resource.TestCheckResourceAttr(projectModelListAddress, "compat_model_list_dynamic_routes", "true"),
			resource.TestCheckResourceAttr(projectModelListAddress, "compat_model_list_enforced", "false"),
			checkProjectModelList(api, p, "recommended", true, false),
			checkProjectModelListWrites(api, 1, 0),
			checkProjectModelListLastPut(api, "compat_model_list", "compat_model_list_dynamic_routes"),
		)},
		{Config: projectModelListTestConfig(url, p, "recommended", "", ""), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: checkProjectModelListWrites(api, 1, 0)},
		// Changing the list and routes is an in-place update.
		{Config: projectModelListTestConfig(url, p, "simplified", "false", ""), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(projectModelListAddress, plancheck.ResourceActionUpdate)}}, Check: resource.ComposeTestCheckFunc(
			checkProjectModelList(api, p, "simplified", false, false),
			checkProjectModelListWrites(api, 2, 0),
			checkProjectModelListLastPut(api, "compat_model_list", "compat_model_list_dynamic_routes"),
		)},
		// Turning enforcement on sends it.
		{Config: projectModelListTestConfig(url, p, "simplified", "false", "true"), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(projectModelListAddress, plancheck.ResourceActionUpdate)}}, Check: resource.ComposeTestCheckFunc(
			resource.TestCheckResourceAttr(projectModelListAddress, "compat_model_list_enforced", "true"),
			checkProjectModelList(api, p, "simplified", false, true),
			checkProjectModelListWrites(api, 3, 0),
			checkProjectModelListLastPut(api, "compat_model_list", "compat_model_list_dynamic_routes", "compat_model_list_enforced"),
		)},
		{ResourceName: projectModelListAddress, ImportState: true, ImportStateVerify: true},
		// Drift on a configured value is corrected.
		{PreConfig: func() { api.mu.Lock(); api.lists[p].list = "all"; api.mu.Unlock() }, Config: projectModelListTestConfig(url, p, "simplified", "false", "true"), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(projectModelListAddress, plancheck.ResourceActionUpdate)}}, Check: resource.ComposeTestCheckFunc(checkProjectModelList(api, p, "simplified", false, true), checkProjectModelListWrites(api, 4, 0))},
		// Leaving enforced out stops managing it: no plan, and a later list
		// change leaves it out of the body so the project keeps it.
		{Config: projectModelListTestConfig(url, p, "simplified", "false", ""), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: checkProjectModelListWrites(api, 4, 0)},
		{Config: projectModelListTestConfig(url, p, "none", "", ""), Check: resource.ComposeTestCheckFunc(
			resource.TestCheckResourceAttr(projectModelListAddress, "compat_model_list_enforced", "true"),
			checkProjectModelList(api, p, "none", true, true),
			checkProjectModelListWrites(api, 5, 0),
			checkProjectModelListLastPut(api, "compat_model_list", "compat_model_list_dynamic_routes"),
		)},
		// Removing the resource makes the project follow the organization.
		{Config: projectModelListProviderOnly(url), Check: resource.ComposeTestCheckFunc(checkProjectModelList(api, p, "", false, false), checkProjectModelListWrites(api, 5, 1))},
	}})
}

// A list cleared outside Terraform drops from state and is set again.
func TestProjectModelListClearedOutsideIsRecreated(t *testing.T) {
	requireTerraform(t)
	api := newProjectModelListTestAPI(t)
	config := projectModelListTestConfig(api.server.URL, projectModelListTestUUID, "recommended", "", "")
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkProjectModelListWrites(api, 2, 1), Steps: []resource.TestStep{
		{Config: config},
		{PreConfig: func() { api.mu.Lock(); delete(api.lists, projectModelListTestUUID); api.mu.Unlock() }, Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(projectModelListAddress, plancheck.ResourceActionCreate)}}, Check: resource.ComposeTestCheckFunc(checkProjectModelList(api, projectModelListTestUUID, "recommended", true, false), checkProjectModelListWrites(api, 2, 0))},
	}})
}

// Creation never takes over a list someone else set; import does.
func TestProjectModelListCreateRefusesExistingList(t *testing.T) {
	requireTerraform(t)
	api := newProjectModelListTestAPI(t)
	api.lists[projectModelListTestUUID] = &projectModelListEntry{list: "all", routes: false, enforce: true}
	config := projectModelListTestConfig(api.server.URL, projectModelListTestUUID, "all", "false", "")
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkProjectModelListWrites(api, 0, 1), Steps: []resource.TestStep{
		{Config: config, ExpectError: regexp.MustCompile(`(?s)already has its own model list.*Import`)},
		{Config: config, ResourceName: projectModelListAddress, ImportState: true, ImportStateId: projectModelListTestUUID, ImportStatePersist: true},
		{Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: resource.ComposeTestCheckFunc(
			resource.TestCheckResourceAttr(projectModelListAddress, "compat_model_list_enforced", "true"),
			checkProjectModelListWrites(api, 0, 0),
		)},
	}})
}

func TestProjectModelListImportValidation(t *testing.T) {
	requireTerraform(t)
	for _, tc := range []struct{ id, want string }{
		{"not-a-uuid", `canonical lowercase project UUID`},
		{"AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA", `canonical lowercase project UUID`},
		{projectModelListTestUUID, `(?s)no model list of its own.*Create the resource`},
	} {
		t.Run(tc.id, func(t *testing.T) {
			api := newProjectModelListTestAPI(t)
			resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, Steps: []resource.TestStep{{Config: projectModelListTestConfig(api.server.URL, projectModelListTestUUID, "all", "", ""), ResourceName: projectModelListAddress, ImportState: true, ImportStateId: tc.id, ExpectError: regexp.MustCompile(tc.want)}}})
			if err := checkProjectModelListWrites(api, 0, 0)(nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProjectModelListValidation(t *testing.T) {
	requireTerraform(t)
	for _, tc := range []struct{ project, list, routes, want string }{
		{"not-a-uuid", "all", "", `UUID`},
		{projectModelListTestUUID, "ALL", "", `one of`},
		{projectModelListTestUUID, "", "", `one of`},
		{projectModelListTestUUID, "none", "false", `(?s)Model list would be empty.*compat_model_list_dynamic_routes\s+must\s+be\s+true`},
	} {
		t.Run(tc.project+tc.list+tc.routes, func(t *testing.T) {
			api := newProjectModelListTestAPI(t)
			resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, Steps: []resource.TestStep{{Config: projectModelListTestConfig(api.server.URL, tc.project, tc.list, tc.routes, ""), PlanOnly: true, ExpectError: regexp.MustCompile(tc.want)}}})
			if err := checkProjectModelListWrites(api, 0, 0)(nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// While the organization enforces its list, a wider project list is refused
// and the provider says why.
func TestProjectModelListWiderThanEnforcedOrganization(t *testing.T) {
	requireTerraform(t)
	api := newProjectModelListTestAPI(t)
	api.orgList, api.orgRoutes, api.orgEnforced = "simplified", false, true
	url := api.server.URL
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkProjectModelListWrites(api, 3, 1), Steps: []resource.TestStep{
		{Config: projectModelListTestConfig(url, projectModelListTestUUID, "all", "false", ""), ExpectError: regexp.MustCompile(`(?s)400.*organization enforces its own list`)},
		{Config: projectModelListTestConfig(url, projectModelListTestUUID, "recommended", "", ""), ExpectError: regexp.MustCompile(`(?s)400.*dynamic\s+routes`)},
		{Config: projectModelListTestConfig(url, projectModelListTestUUID, "recommended", "false", ""), Check: checkProjectModelList(api, projectModelListTestUUID, "recommended", false, false)},
	}})
}

func TestProjectModelListMissingProject(t *testing.T) {
	requireTerraform(t)
	api := newProjectModelListTestAPI(t)
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, Steps: []resource.TestStep{
		{Config: projectModelListTestConfig(api.server.URL, projectModelListMissingUUID, "all", "", ""), ExpectError: regexp.MustCompile(`(?s)404.*missing,\s+deleted\s+or\s+in\s+another\s+organization`)},
	}})
}

// Destroying the list of a project that was deleted meanwhile succeeds.
func TestProjectModelListDeleteAfterProjectDeleted(t *testing.T) {
	requireTerraform(t)
	api := newProjectModelListTestAPI(t)
	config := projectModelListTestConfig(api.server.URL, projectModelListTestUUID, "all", "", "")
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkProjectModelListWrites(api, 1, 1), Steps: []resource.TestStep{
		{Config: config},
		// The list is still reported (as if deletion had not propagated),
		// but DELETE answers 404.
		{PreConfig: func() { api.mu.Lock(); delete(api.projects, projectModelListTestUUID); api.mu.Unlock() }, Config: projectModelListProviderOnly(api.server.URL)},
	}})
}

func TestProjectModelListPreservesStateOnReadFailure(t *testing.T) {
	requireTerraform(t)
	for _, code := range []int{403, 500} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			api := newProjectModelListTestAPI(t)
			config := projectModelListTestConfig(api.server.URL, projectModelListTestUUID, "all", "", "")
			resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkProjectModelListWrites(api, 1, 1), Steps: []resource.TestStep{
				{Config: config},
				{PreConfig: func() { api.mu.Lock(); api.getFailure = code; api.mu.Unlock() }, Config: config, PlanOnly: true, ExpectError: regexp.MustCompile(strconv.Itoa(code))},
				{PreConfig: func() { api.mu.Lock(); api.getFailure = 0; api.mu.Unlock() }, Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: checkProjectModelListWrites(api, 1, 0)},
			}})
		})
	}
}

func TestProjectModelListDeleteFailureKeepsState(t *testing.T) {
	requireTerraform(t)
	api := newProjectModelListTestAPI(t)
	config := projectModelListTestConfig(api.server.URL, projectModelListTestUUID, "all", "", "")
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkProjectModelListWrites(api, 1, 2), Steps: []resource.TestStep{
		{Config: config},
		{PreConfig: func() { api.mu.Lock(); api.deleteFailure = 403; api.mu.Unlock() }, Config: projectModelListProviderOnly(api.server.URL), ExpectError: regexp.MustCompile(`403`)},
		{PreConfig: func() { api.mu.Lock(); api.deleteFailure = 0; api.mu.Unlock() }, Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: checkProjectModelList(api, projectModelListTestUUID, "all", true, false)},
	}})
}

func TestProjectModelListUncertainWriteRecovery(t *testing.T) {
	requireTerraform(t)
	api := newProjectModelListTestAPI(t)
	api.putResponse = "malformed_saved"
	config := projectModelListTestConfig(api.server.URL, projectModelListTestUUID, "all", "", "")
	updated := projectModelListTestConfig(api.server.URL, projectModelListTestUUID, "simplified", "", "true")
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkProjectModelListWrites(api, 2, 1), Steps: []resource.TestStep{
		{Config: config, Check: checkProjectModelListWrites(api, 1, 0)},
		{Config: updated, Check: resource.ComposeTestCheckFunc(checkProjectModelList(api, projectModelListTestUUID, "simplified", true, true), checkProjectModelListWrites(api, 2, 0))},
		{Config: updated, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: checkProjectModelListWrites(api, 2, 0)},
	}})
}

func TestProjectModelListUnexpectedWriteOutcome(t *testing.T) {
	requireTerraform(t)
	// A response naming another project is not covered here: the recovery
	// read then finds this project's desired list and accepts it.
	for _, outcome := range []string{"different_value"} {
		t.Run(outcome, func(t *testing.T) {
			api := newProjectModelListTestAPI(t)
			config := projectModelListTestConfig(api.server.URL, projectModelListTestUUID, "all", "", "")
			updated := projectModelListTestConfig(api.server.URL, projectModelListTestUUID, "simplified", "", "")
			resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkProjectModelListWrites(api, 3, 1), Steps: []resource.TestStep{
				{Config: config},
				{PreConfig: func() { api.mu.Lock(); api.putResponse = outcome; api.mu.Unlock() }, Config: updated, ExpectError: regexp.MustCompile(`(?i)different project`)},
				{PreConfig: func() {
					api.mu.Lock()
					defer api.mu.Unlock()
					if api.puts != 2 {
						t.Fatal("unexpected write outcome caused an automatic PUT retry")
					}
					api.putResponse = ""
				}, Config: updated, Check: checkProjectModelList(api, projectModelListTestUUID, "simplified", true, false)},
			}})
		})
	}
}

// Changing project_uuid moves the list: the old project follows the
// organization again.
func TestProjectModelListProjectChangeReplaces(t *testing.T) {
	requireTerraform(t)
	api := newProjectModelListTestAPI(t)
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkProjectModelListWrites(api, 2, 2), Steps: []resource.TestStep{
		{Config: projectModelListTestConfig(api.server.URL, projectModelListTestUUID, "all", "", "")},
		{Config: projectModelListTestConfig(api.server.URL, projectModelListOtherUUID, "all", "", ""), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(projectModelListAddress, plancheck.ResourceActionDestroyBeforeCreate)}}, Check: resource.ComposeTestCheckFunc(
			checkProjectModelList(api, projectModelListTestUUID, "", false, false),
			checkProjectModelList(api, projectModelListOtherUUID, "all", true, false),
		)},
	}})
}

// A PUT response naming another project is not trusted; one read confirms
// this project's list instead, and the PUT is not retried.
func TestProjectModelListResponseForOtherProjectIsVerified(t *testing.T) {
	requireTerraform(t)
	api := newProjectModelListTestAPI(t)
	api.putResponse = "different_uuid"
	config := projectModelListTestConfig(api.server.URL, projectModelListTestUUID, "all", "", "")
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkProjectModelListWrites(api, 1, 1), Steps: []resource.TestStep{
		{Config: config, Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr(projectModelListAddress, "id", projectModelListTestUUID), checkProjectModelListWrites(api, 1, 0))},
	}})
}
