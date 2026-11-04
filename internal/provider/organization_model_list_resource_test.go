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
	for _, tc := range []struct{ uuid, value string }{{"not-a-uuid", "all"}, {"AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA", "all"}, {organizationModelListTestUUID, "ALL"}, {organizationModelListTestUUID, ""}, {organizationModelListTestUUID, "unknown"}} {
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
		restored, err := api.SetOrganizationModelList(ctx, original.CompatModelList)
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
