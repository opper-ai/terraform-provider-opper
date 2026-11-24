package provider

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

func organizationModelListTestConfig(baseURL, uuid, value string) string {
	return fmt.Sprintf(`
provider "opper" {
 management_api_key = "op-mak-local-test"
 base_url = %q
}
resource "opper_organization_model_list" "test" {
 organization_uuid = %q
 compat_model_list = %q
}
data "opper_organization_model_list" "current" {}
`, baseURL, uuid, value)
}
func checkOrganizationModelListWrites(api *organizationModelListTestAPI, want int, value string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		api.mu.Lock()
		defer api.mu.Unlock()
		if api.puts != want || api.value != value {
			return fmt.Errorf("expected %d PUTs and value %s, got %d/%s", want, value, api.puts, api.value)
		}
		return nil
	}
}
func TestOrganizationModelListLifecycle(t *testing.T) {
	requireTerraform(t)
	api := newOrganizationModelListTestAPI(t)
	first := organizationModelListTestConfig(api.server.URL, organizationModelListTestUUID, "all")
	changed := organizationModelListTestConfig(api.server.URL, organizationModelListTestUUID, "simplified")
	const address = "opper_organization_model_list.test"
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true,
		CheckDestroy: checkOrganizationModelListWrites(api, 3, "simplified"),
		Steps: []resource.TestStep{
			{Config: first, Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr(address, "id", organizationModelListTestUUID), resource.TestCheckResourceAttr(address, "compat_model_list", "all"), checkOrganizationModelListWrites(api, 1, "all"))},
			{Config: first, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: checkOrganizationModelListWrites(api, 1, "all")},
			{Config: changed, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)}}, Check: checkOrganizationModelListWrites(api, 2, "simplified")},
			{ResourceName: address, ImportState: true, ImportStateVerify: true},
			{PreConfig: func() { api.mu.Lock(); api.value = "recommended"; api.mu.Unlock() }, Config: changed, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)}}, Check: checkOrganizationModelListWrites(api, 3, "simplified")},
			{Config: changed, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr("data.opper_organization_model_list.current", "organization_uuid", organizationModelListTestUUID), resource.TestCheckResourceAttr("data.opper_organization_model_list.current", "compat_model_list", "simplified"))},
			// Removing the resource relinquishes management instead of resetting the
			// preference, deleting the organization, or calling any DELETE endpoint.
			{Config: fmt.Sprintf("provider \"opper\" {\nmanagement_api_key=\"op-mak-local-test\"\nbase_url=%q\n}\n", api.server.URL), Check: checkOrganizationModelListWrites(api, 3, "simplified")},
		},
	})
}
func TestOrganizationModelListAdoptsExistingValue(t *testing.T) {
	requireTerraform(t)
	api := newOrganizationModelListTestAPI(t)
	config := organizationModelListTestConfig(api.server.URL, organizationModelListTestUUID, "recommended")
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkOrganizationModelListWrites(api, 0, "recommended"), Steps: []resource.TestStep{{Config: config, Check: checkOrganizationModelListWrites(api, 0, "recommended")}, {Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}}}})
}
func TestOrganizationModelListRejectsUUIDMismatchBeforeWrite(t *testing.T) {
	requireTerraform(t)
	api := newOrganizationModelListTestAPI(t)
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, Steps: []resource.TestStep{{Config: organizationModelListTestConfig(api.server.URL, organizationModelListOtherUUID, "all"), ExpectError: regexp.MustCompile(`(?i)organization.*match|different organization|organization.*credential`)}}})
	if err := checkOrganizationModelListWrites(api, 0, "recommended")(nil); err != nil {
		t.Fatal(err)
	}
}
func TestOrganizationModelListPreservesStateOnReadFailure(t *testing.T) {
	requireTerraform(t)
	for _, code := range []int{403, 404, 500} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			api := newOrganizationModelListTestAPI(t)
			config := organizationModelListTestConfig(api.server.URL, organizationModelListTestUUID, "all")
			resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkOrganizationModelListWrites(api, 1, "all"), Steps: []resource.TestStep{
				{Config: config},
				{PreConfig: func() { api.mu.Lock(); api.getFailure = code; api.mu.Unlock() }, Config: config, PlanOnly: true, ExpectError: regexp.MustCompile(strconv.Itoa(code))},
				{PreConfig: func() { api.mu.Lock(); api.getFailure = 0; api.mu.Unlock() }, Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr("opper_organization_model_list.test", "id", organizationModelListTestUUID), checkOrganizationModelListWrites(api, 1, "all"))},
			}})
		})
	}
}
func TestOrganizationModelListCredentialChangePreservesState(t *testing.T) {
	requireTerraform(t)
	api := newOrganizationModelListTestAPI(t)
	config := organizationModelListTestConfig(api.server.URL, organizationModelListTestUUID, "all")
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkOrganizationModelListWrites(api, 1, "all"), Steps: []resource.TestStep{
		{Config: config},
		{PreConfig: func() { api.mu.Lock(); api.uuid = organizationModelListOtherUUID; api.mu.Unlock() }, Config: config, PlanOnly: true, ExpectError: regexp.MustCompile(`(?i)organization.*match|different organization|organization.*credential`)},
		{PreConfig: func() { api.mu.Lock(); api.uuid = organizationModelListTestUUID; api.mu.Unlock() }, Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: resource.TestCheckResourceAttr("opper_organization_model_list.test", "organization_uuid", organizationModelListTestUUID)},
	}})
}
func TestOrganizationModelListUpdateRefusalPreservesState(t *testing.T) {
	requireTerraform(t)
	api := newOrganizationModelListTestAPI(t)
	config := organizationModelListTestConfig(api.server.URL, organizationModelListTestUUID, "all")
	updated := organizationModelListTestConfig(api.server.URL, organizationModelListTestUUID, "simplified")
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkOrganizationModelListWrites(api, 2, "all"), Steps: []resource.TestStep{
		{Config: config},
		{PreConfig: func() { api.mu.Lock(); api.putFailure = 403; api.mu.Unlock() }, Config: updated, ExpectError: regexp.MustCompile(`403`)},
		{PreConfig: func() { api.mu.Lock(); api.putFailure = 0; api.mu.Unlock() }, Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: checkOrganizationModelListWrites(api, 2, "all")},
	}})
}
func TestOrganizationModelListUncertainWriteRecovery(t *testing.T) {
	requireTerraform(t)
	api := newOrganizationModelListTestAPI(t)
	api.putResponse = "malformed_saved"
	config := organizationModelListTestConfig(api.server.URL, organizationModelListTestUUID, "all")
	updated := organizationModelListTestConfig(api.server.URL, organizationModelListTestUUID, "simplified")
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkOrganizationModelListWrites(api, 2, "simplified"), Steps: []resource.TestStep{
		{Config: config, Check: checkOrganizationModelListWrites(api, 1, "all")},
		{Config: updated, Check: checkOrganizationModelListWrites(api, 2, "simplified")},
		{Config: updated, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: checkOrganizationModelListWrites(api, 2, "simplified")},
	}})
}
func TestOrganizationModelListUnexpectedWriteOutcome(t *testing.T) {
	requireTerraform(t)
	for _, outcome := range []string{"malformed_other", "different_value", "different_uuid"} {
		t.Run(outcome, func(t *testing.T) {
			api := newOrganizationModelListTestAPI(t)
			config := organizationModelListTestConfig(api.server.URL, organizationModelListTestUUID, "all")
			updated := organizationModelListTestConfig(api.server.URL, organizationModelListTestUUID, "simplified")
			resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkOrganizationModelListWrites(api, 2, "all"), Steps: []resource.TestStep{
				{Config: config},
				{PreConfig: func() { api.mu.Lock(); api.putResponse = outcome; api.mu.Unlock() }, Config: updated, ExpectError: regexp.MustCompile(`(?i)organization|outcome|model.list|preference`)},
				{PreConfig: func() {
					api.mu.Lock()
					defer api.mu.Unlock()
					if api.puts != 2 {
						t.Fatal("unexpected write outcome caused an automatic PUT retry")
					}
					api.value = "all"
					api.putResponse = ""
				}, Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: resource.TestCheckResourceAttr("opper_organization_model_list.test", "compat_model_list", "all")},
			}})
		})
	}
}
func TestOrganizationModelListImportValidation(t *testing.T) {
	requireTerraform(t)
	for _, id := range []string{"not-a-uuid", "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA", organizationModelListOtherUUID} {
		t.Run(id, func(t *testing.T) {
			api := newOrganizationModelListTestAPI(t)
			resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, Steps: []resource.TestStep{{Config: organizationModelListTestConfig(api.server.URL, organizationModelListTestUUID, "all"), ResourceName: "opper_organization_model_list.test", ImportState: true, ImportStateId: id, ExpectError: regexp.MustCompile(`(?i)UUID|organization`)}}})
			if err := checkOrganizationModelListWrites(api, 0, "recommended")(nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestOrganizationModelListValidation(t *testing.T) {
	requireTerraform(t)
	for _, tc := range []struct{ uuid, value string }{{"not-a-uuid", "all"}, {"AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA", "all"}, {organizationModelListTestUUID, "ALL"}, {organizationModelListTestUUID, ""}, {organizationModelListTestUUID, "unknown"}, {organizationModelListTestUUID, "NONE"}} {
		t.Run(tc.uuid+tc.value, func(t *testing.T) {
			api := newOrganizationModelListTestAPI(t)
			resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, Steps: []resource.TestStep{{Config: organizationModelListTestConfig(api.server.URL, tc.uuid, tc.value), ExpectError: regexp.MustCompile(`(?i)UUID|compat_model_list|one of`)}}})
			if err := checkOrganizationModelListWrites(api, 0, "recommended")(nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestOrganizationModelListDeferredUUID(t *testing.T) {
	requireTerraform(t)
	api := newOrganizationModelListTestAPI(t)
	config := fmt.Sprintf(`
provider "opper" {
 management_api_key="op-mak-local-test"
 base_url=%q
}
data "opper_organization_model_list" "current" {}
resource "terraform_data" "organization" { input=data.opper_organization_model_list.current.organization_uuid }
resource "opper_organization_model_list" "test" {
 organization_uuid=terraform_data.organization.output
 compat_model_list="all"
}
`, api.server.URL)
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkOrganizationModelListWrites(api, 1, "all"), Steps: []resource.TestStep{{Config: config, Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr("opper_organization_model_list.test", "id", organizationModelListTestUUID), checkOrganizationModelListWrites(api, 1, "all"))}, {Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}}}})
}

func TestAccOrganizationModelListResource(t *testing.T) {
	if os.Getenv("TF_ACC") != "1" {
		t.Skip("set TF_ACC=1 to run live Opper acceptance tests")
	}
	if os.Getenv("OPPER_TEST_DISPOSABLE_ORG") != "1" {
		t.Skip("set OPPER_TEST_DISPOSABLE_ORG=1 only for an isolated disposable test organization")
	}
	token := os.Getenv("OPPER_MANAGEMENT_API_KEY")
	if strings.TrimSpace(token) == "" {
		t.Fatal("OPPER_MANAGEMENT_API_KEY is required for live acceptance tests")
	}
	requireTerraform(t)
	baseURL := os.Getenv("OPPER_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.opper.ai"
	}
	api, err := client.New(baseURL, token, "acceptance")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	original, err := api.GetOrganizationModelList(ctx)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	// This singleton intentionally survives Terraform destroy. Restore its exact
	// original value explicitly even if an acceptance assertion fails.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		current, err := api.GetOrganizationModelList(ctx)
		if err != nil {
			t.Errorf("inspect original organization before restore: %v", err)
			return
		}
		if current.OrganizationUUID != original.OrganizationUUID {
			t.Error("credential organization changed; refusing cleanup write")
			return
		}
		if current == original {
			return
		}
		restored, err := api.SetOrganizationModelList(ctx, client.OrganizationModelListChange{CompatModelList: &original.CompatModelList, DynamicRoutes: &original.DynamicRoutes})
		if err != nil {
			t.Errorf("restore original model-list preference: %v", err)
			return
		}
		if restored != original {
			t.Error("original organization preference was not restored")
		}
	})
	config := func(value string) string {
		return fmt.Sprintf(`
provider "opper" {}
data "opper_organization_model_list" "current" {}
resource "opper_organization_model_list" "test" {
 organization_uuid=%q
 compat_model_list=%q
}
`, original.OrganizationUUID, value)
	}
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: func(*terraform.State) error {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		current, err := api.GetOrganizationModelList(ctx)
		if err != nil {
			return err
		}
		if current.OrganizationUUID != original.OrganizationUUID || current.CompatModelList != "simplified" {
			return fmt.Errorf("Terraform destroy changed the organization model-list preference")
		}
		return nil
	}, Steps: []resource.TestStep{
		{Config: config("all"), Check: resource.TestCheckResourceAttr("opper_organization_model_list.test", "compat_model_list", "all")},
		{Config: config("all"), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
		{Config: config("simplified"), Check: resource.TestCheckResourceAttr("opper_organization_model_list.test", "compat_model_list", "simplified")},
		{ResourceName: "opper_organization_model_list.test", ImportState: true, ImportStateVerify: true},
	}})
}

func organizationModelListRoutesTestConfig(baseURL, value, routes string) string {
	routesLine := ""
	if routes != "" {
		routesLine = "\n compat_model_list_dynamic_routes = " + routes
	}
	return fmt.Sprintf(`
provider "opper" {
 management_api_key = "op-mak-local-test"
 base_url = %q
}
resource "opper_organization_model_list" "test" {
 organization_uuid = %q
 compat_model_list = %q%s
}
data "opper_organization_model_list" "current" {}
`, baseURL, organizationModelListTestUUID, value, routesLine)
}

func checkOrganizationModelListRoutes(api *organizationModelListTestAPI, want bool) resource.TestCheckFunc {
	return func(*terraform.State) error {
		api.mu.Lock()
		defer api.mu.Unlock()
		if api.routes != want {
			return fmt.Errorf("expected dynamic routes %v, got %v", want, api.routes)
		}
		return nil
	}
}

// checkOrganizationModelListLastPut checks the field names of the latest PUT
// body: an omitted dynamic-routes setting must never be sent.
func checkOrganizationModelListLastPut(api *organizationModelListTestAPI, fields ...string) resource.TestCheckFunc {
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

// An organization set to none on the platform must stay readable, plannable
// and importable.
func TestOrganizationModelListReadsNone(t *testing.T) {
	requireTerraform(t)
	api := newOrganizationModelListTestAPI(t)
	api.value = "none"
	const address = "opper_organization_model_list.test"
	config := organizationModelListRoutesTestConfig(api.server.URL, "none", "")
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkOrganizationModelListWrites(api, 0, "none"), Steps: []resource.TestStep{
		{Config: config, Check: resource.ComposeTestCheckFunc(
			resource.TestCheckResourceAttr(address, "compat_model_list", "none"),
			resource.TestCheckResourceAttr(address, "compat_model_list_dynamic_routes", "true"),
			resource.TestCheckResourceAttr("data.opper_organization_model_list.current", "compat_model_list", "none"),
			resource.TestCheckResourceAttr("data.opper_organization_model_list.current", "compat_model_list_dynamic_routes", "true"),
			checkOrganizationModelListWrites(api, 0, "none"),
		)},
		{Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
		{ResourceName: address, ImportState: true, ImportStateVerify: true},
	}})
}

// A resource managing another value converges an organization that was
// switched to none outside Terraform.
func TestOrganizationModelListCorrectsDriftFromNone(t *testing.T) {
	requireTerraform(t)
	api := newOrganizationModelListTestAPI(t)
	const address = "opper_organization_model_list.test"
	config := organizationModelListRoutesTestConfig(api.server.URL, "all", "")
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkOrganizationModelListWrites(api, 2, "all"), Steps: []resource.TestStep{
		{Config: config, Check: checkOrganizationModelListWrites(api, 1, "all")},
		{PreConfig: func() { api.mu.Lock(); api.value = "none"; api.mu.Unlock() }, Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)}}, Check: resource.ComposeTestCheckFunc(checkOrganizationModelListWrites(api, 2, "all"), checkOrganizationModelListLastPut(api, "compat_model_list"))},
	}})
}

func TestOrganizationModelListDynamicRoutesLifecycle(t *testing.T) {
	requireTerraform(t)
	api := newOrganizationModelListTestAPI(t)
	const address = "opper_organization_model_list.test"
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkOrganizationModelListWrites(api, 6, "all"), Steps: []resource.TestStep{
		// Omitted: adopts and reports the current value, sends nothing for it.
		{Config: organizationModelListRoutesTestConfig(api.server.URL, "all", ""), Check: resource.ComposeTestCheckFunc(
			resource.TestCheckResourceAttr(address, "compat_model_list_dynamic_routes", "true"),
			checkOrganizationModelListWrites(api, 1, "all"),
			checkOrganizationModelListLastPut(api, "compat_model_list"),
		)},
		// Setting only the routes is an in-place update with both fields sent.
		{Config: organizationModelListRoutesTestConfig(api.server.URL, "all", "false"), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)}}, Check: resource.ComposeTestCheckFunc(
			resource.TestCheckResourceAttr(address, "compat_model_list_dynamic_routes", "false"),
			checkOrganizationModelListRoutes(api, false),
			checkOrganizationModelListWrites(api, 2, "all"),
			checkOrganizationModelListLastPut(api, "compat_model_list", "compat_model_list_dynamic_routes"),
		)},
		{Config: organizationModelListRoutesTestConfig(api.server.URL, "all", "false"), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: resource.TestCheckResourceAttr("data.opper_organization_model_list.current", "compat_model_list_dynamic_routes", "false")},
		{ResourceName: address, ImportState: true, ImportStateVerify: true},
		// Drift on a configured value is corrected.
		{PreConfig: func() { api.mu.Lock(); api.routes = true; api.mu.Unlock() }, Config: organizationModelListRoutesTestConfig(api.server.URL, "all", "false"), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)}}, Check: resource.ComposeTestCheckFunc(checkOrganizationModelListRoutes(api, false), checkOrganizationModelListWrites(api, 3, "all"))},
		// none and routes on in one PUT, from a state with the routes off.
		{Config: organizationModelListRoutesTestConfig(api.server.URL, "none", "true"), Check: resource.ComposeTestCheckFunc(
			resource.TestCheckResourceAttr(address, "compat_model_list", "none"),
			checkOrganizationModelListRoutes(api, true),
			checkOrganizationModelListWrites(api, 4, "none"),
			checkOrganizationModelListLastPut(api, "compat_model_list", "compat_model_list_dynamic_routes"),
		)},
		// Removing the attribute stops managing it: no plan, no PUT, and an
		// outside change to it is reported but not reverted.
		{Config: organizationModelListRoutesTestConfig(api.server.URL, "none", ""), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: checkOrganizationModelListWrites(api, 4, "none")},
		{PreConfig: func() { api.mu.Lock(); api.value, api.routes = "all", false; api.mu.Unlock() }, Config: organizationModelListRoutesTestConfig(api.server.URL, "all", ""), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr(address, "compat_model_list_dynamic_routes", "false"), checkOrganizationModelListWrites(api, 4, "all"))},
		// Changing only the list keeps the unmanaged routes value as it is.
		{Config: organizationModelListRoutesTestConfig(api.server.URL, "simplified", ""), Check: resource.ComposeTestCheckFunc(
			resource.TestCheckResourceAttr(address, "compat_model_list_dynamic_routes", "false"),
			checkOrganizationModelListRoutes(api, false),
			checkOrganizationModelListWrites(api, 5, "simplified"),
			checkOrganizationModelListLastPut(api, "compat_model_list"),
		)},
		{Config: organizationModelListRoutesTestConfig(api.server.URL, "all", ""), Check: checkOrganizationModelListWrites(api, 6, "all")},
	}})
}

func TestOrganizationModelListRejectsNoneWithoutRoutesAtPlan(t *testing.T) {
	requireTerraform(t)
	api := newOrganizationModelListTestAPI(t)
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, Steps: []resource.TestStep{
		{Config: organizationModelListRoutesTestConfig(api.server.URL, "none", "false"), PlanOnly: true, ExpectError: regexp.MustCompile(`(?s)Model list would be empty.*compat_model_list_dynamic_routes\s+must\s+be\s+true`)},
	}})
	if err := checkOrganizationModelListWrites(api, 0, "recommended")(nil); err != nil {
		t.Fatal(err)
	}
}

// With the routes omitted, none cannot be applied while the organization has
// them off; the provider explains this instead of sending a refused PUT.
func TestOrganizationModelListNoneWhileRoutesOffExplains(t *testing.T) {
	requireTerraform(t)
	api := newOrganizationModelListTestAPI(t)
	api.routes = false
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, Steps: []resource.TestStep{
		{Config: organizationModelListRoutesTestConfig(api.server.URL, "none", ""), ExpectError: regexp.MustCompile(`(?s)currently off.*compat_model_list_dynamic_routes\s+=\s+true`)},
	}})
	if err := checkOrganizationModelListWrites(api, 0, "recommended")(nil); err != nil {
		t.Fatal(err)
	}
}
