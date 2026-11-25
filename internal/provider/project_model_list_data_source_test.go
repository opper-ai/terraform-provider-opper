package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestProjectModelListDataSource(t *testing.T) {
	requireTerraform(t)
	api := newProjectModelListTestAPI(t)
	api.lists[projectModelListOtherUUID] = &projectModelListEntry{list: "none", routes: true, enforce: true}
	config := fmt.Sprintf(`
provider "opper" {
 management_api_key="op-mak-local-test"
 base_url=%q
}
data "opper_project_model_list" "own" { project_uuid = %q }
data "opper_project_model_list" "follows" { project_uuid = %q }
`, api.server.URL, projectModelListOtherUUID, projectModelListTestUUID)
	const own, follows = "data.opper_project_model_list.own", "data.opper_project_model_list.follows"
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkProjectModelListWrites(api, 0, 0), Steps: []resource.TestStep{
		{Config: config, Check: resource.ComposeTestCheckFunc(
			resource.TestCheckResourceAttr(own, "id", projectModelListOtherUUID),
			resource.TestCheckResourceAttr(own, "follows_organization", "false"),
			resource.TestCheckResourceAttr(own, "compat_model_list", "none"),
			resource.TestCheckResourceAttr(own, "compat_model_list_dynamic_routes", "true"),
			resource.TestCheckResourceAttr(own, "compat_model_list_enforced", "true"),
			resource.TestCheckResourceAttr(follows, "follows_organization", "true"),
			resource.TestCheckNoResourceAttr(follows, "compat_model_list"),
			resource.TestCheckNoResourceAttr(follows, "compat_model_list_dynamic_routes"),
			resource.TestCheckNoResourceAttr(follows, "compat_model_list_enforced"),
		)},
		{PreConfig: func() {
			api.mu.Lock()
			api.lists[projectModelListTestUUID] = &projectModelListEntry{list: "simplified", routes: false}
			delete(api.lists, projectModelListOtherUUID)
			api.mu.Unlock()
		}, Config: config, Check: resource.ComposeTestCheckFunc(
			resource.TestCheckResourceAttr(own, "follows_organization", "true"),
			resource.TestCheckResourceAttr(follows, "follows_organization", "false"),
			resource.TestCheckResourceAttr(follows, "compat_model_list", "simplified"),
			resource.TestCheckResourceAttr(follows, "compat_model_list_dynamic_routes", "false"),
			resource.TestCheckResourceAttr(follows, "compat_model_list_enforced", "false"),
		)},
	}})
}

func TestProjectModelListDataSourceFailure(t *testing.T) {
	requireTerraform(t)
	api := newProjectModelListTestAPI(t)
	api.getFailure = 403
	config := fmt.Sprintf("provider \"opper\" {\nmanagement_api_key=\"op-mak-local-test\"\nbase_url=%q\n}\ndata \"opper_project_model_list\" \"x\" { project_uuid = %q }\n", api.server.URL, projectModelListTestUUID)
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, Steps: []resource.TestStep{{Config: config, ExpectError: regexp.MustCompile(`403`)}}})
}
