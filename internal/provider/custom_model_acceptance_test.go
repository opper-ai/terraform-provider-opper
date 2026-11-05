package provider

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

// This suite adds its own opt-in so an older broad TestAcc invocation cannot
// accidentally start the new BYOK lifecycle with an unrelated acceptance key.
func TestAccCustomModelResource(t *testing.T) {
	if os.Getenv("TF_ACC") != "1" || os.Getenv("OPPER_TEST_DISPOSABLE_ORG") != "1" || os.Getenv("OPPER_TEST_CUSTOM_MODEL_ACCEPTANCE") != "1" {
		t.Skip("requires separately authorized disposable custom-model acceptance opt-in")
	}
	requireTerraform(t)
	token := os.Getenv("OPPER_MANAGEMENT_API_KEY")
	if token == "" || os.Getenv("OPPER_TEST_ORGANIZATION_UUID") == "" {
		t.Fatal("requires a privately supplied key for the separately verified disposable organization")
	}
	base := os.Getenv("OPPER_BASE_URL")
	if base == "" {
		base = "https://api.opper.ai"
	}
	runCustomModelAcceptance(t, base, token, func(c resource.TestCase) { resource.Test(t, c) })
}

// Exercise the identical live-suite workflow locally without setting TF_ACC or
// granting another opt-in test permission to contact production.
func TestCustomModelAcceptanceWorkflowAgainstFixture(t *testing.T) {
	api := newCustomModelTestAPI(t)
	runCustomModelAcceptance(t, api.server.URL, "op-mak-local-test", func(c resource.TestCase) { resource.UnitTest(t, c) })
}

func runCustomModelAcceptance(t *testing.T, base, token string, run func(resource.TestCase)) {
	t.Helper()
	requireTerraform(t)
	t.Setenv("OPPER_MANAGEMENT_API_KEY", token)
	c, err := client.New(base, token, "acceptance")
	if err != nil {
		t.Fatal(err)
	}
	before, err := c.ListCustomModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprint(time.Now().UnixNano())
	name := "tf-acc-byok-" + suffix
	key := "tf-acc-byok-create-" + suffix
	config := func(label string, version int64, credential string) string {
		return fmt.Sprintf(`provider "opper" { base_url = %q }
resource "opper_custom_model" "test" {
 name = %q
 identifier = "openai/gpt-test"
 creation_idempotency_key = %q
 credential_wo = %q
 credential_version = %d
}
data "opper_custom_model" "test" { id = opper_custom_model.test.id }
data "opper_custom_models" "all" {}
`, base, label, key, credential, version)
	}
	first := config(name, 1, "not-a-provider-credential-"+suffix)
	renamed := config(name+"-renamed", 1, "not-a-provider-credential-"+suffix)
	rotated := config(name+"-renamed", 2, "not-a-provider-rotation-"+suffix)
	const address = "opper_custom_model.test"
	run(resource.TestCase{
		ProtoV6ProviderFactories:  projectProviderFactories(),
		PreventPostDestroyRefresh: true,
		CheckDestroy: func(*terraform.State) error {
			after, err := c.ListCustomModels(context.Background())
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(before, after) {
				return fmt.Errorf("custom-model inventory was not restored after acceptance cleanup")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: first, Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr(address, "name", name), resource.TestCheckResourceAttr(address, "credential_version", "1"), resource.TestCheckResourceAttr(address, "operation_pending", "false"), resource.TestCheckNoResourceAttr(address, "credential_wo"))},
			{Config: first, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
			{Config: renamed, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)}}},
			{Config: rotated, Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr(address, "credential_version", "2"), resource.TestCheckResourceAttr(address, "definition_changed", "false"), resource.TestCheckNoResourceAttr(address, "credential_wo"))},
			{ResourceName: address, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"creation_idempotency_key", "credential_version", "operation_id", "operation_kind"}},
			{Config: rotated, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
		},
	})
}
