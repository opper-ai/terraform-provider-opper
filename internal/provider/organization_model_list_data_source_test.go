package provider

import (
	"fmt"
	"regexp"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

func TestOrganizationModelListDataSource(t *testing.T) {
	requireTerraform(t)
	api := newOrganizationModelListTestAPI(t)
	config := fmt.Sprintf(`
provider "opper" {
 management_api_key="op-mak-local-test"
 base_url=%q
}
data "opper_organization_model_list" "current" {}
`, api.server.URL)
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkOrganizationModelListWrites(api, 0, "simplified"), Steps: []resource.TestStep{
		{Config: config, Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr("data.opper_organization_model_list.current", "id", organizationModelListTestUUID), resource.TestCheckResourceAttr("data.opper_organization_model_list.current", "compat_model_list", "recommended"))},
		{Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
		{PreConfig: func() { api.mu.Lock(); api.value = "simplified"; api.mu.Unlock() }, Config: config, Check: resource.TestCheckResourceAttr("data.opper_organization_model_list.current", "compat_model_list", "simplified")},
	}})
}
func TestOrganizationModelListDataSourceFailure(t *testing.T) {
	requireTerraform(t)
	for _, code := range []int{403, 404, 500} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			api := newOrganizationModelListTestAPI(t)
			api.getFailure = code
			config := fmt.Sprintf("provider \"opper\" {\nmanagement_api_key=\"op-mak-local-test\"\nbase_url=%q\n}\ndata \"opper_organization_model_list\" \"current\" {}\n", api.server.URL)
			resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, Steps: []resource.TestStep{{Config: config, ExpectError: regexp.MustCompile(strconv.Itoa(code))}}})
			if err := checkOrganizationModelListWrites(api, 0, "recommended")(nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestOrganizationModelListOrganizationUUIDRequiresReplacement(t *testing.T) {
	requireTerraform(t)
	api := newOrganizationModelListTestAPI(t)
	config := organizationModelListTestConfig(api.server.URL, organizationModelListTestUUID, "all")
	replacement := organizationModelListTestConfig(api.server.URL, organizationModelListOtherUUID, "all")
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkOrganizationModelListWrites(api, 1, "all"), Steps: []resource.TestStep{
		{Config: config},
		{Config: replacement, PlanOnly: true, ExpectNonEmptyPlan: true, ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectResourceAction("opper_organization_model_list.test", plancheck.ResourceActionDestroyBeforeCreate)}}},
	}})
}
