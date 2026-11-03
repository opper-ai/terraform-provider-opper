package provider

import (
	"context"
	"fmt"
	"net/http"
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

func apiKeyTestConfig(baseURL, name, rotation, idempotencyKey string, createBeforeDestroy bool) string {
	optional := ""
	if rotation != "" {
		optional += fmt.Sprintf("rotation = %q\n", rotation)
	}
	if idempotencyKey != "" {
		optional += fmt.Sprintf("idempotency_key = %q\n", idempotencyKey)
	}
	if createBeforeDestroy {
		optional += "lifecycle { create_before_destroy = true }\n"
	}
	return fmt.Sprintf(`
provider "opper" {
 management_api_key = "op-mak-local-test"
 base_url = %q
}
resource "opper_api_key" "test" {
 project_id = %q
 name = %q
 %s
}
`, baseURL, keyTestProjectID, name, optional)
}

func apiKeyImportID(s *terraform.State) (string, error) {
	rs, ok := s.RootModule().Resources["opper_api_key.test"]
	if !ok || rs.Primary == nil || rs.Primary.ID == "" {
		return "", fmt.Errorf("key missing from state")
	}
	return rs.Primary.Attributes["project_id"] + "/" + rs.Primary.ID, nil
}

func checkImportedAPIKeyMetadata(states []*terraform.InstanceState) error {
	if len(states) != 1 {
		return fmt.Errorf("expected one imported runtime key")
	}
	if states[0].Attributes["secret"] != "" {
		return fmt.Errorf("import unexpectedly recovered a runtime secret")
	}
	if states[0].Attributes["secret_available"] != "false" {
		return fmt.Errorf("import did not report unavailable runtime secret")
	}
	return nil
}

func checkAPIKeyDestroyed(api *apiKeyTestAPI) resource.TestCheckFunc {
	return func(*terraform.State) error {
		api.mu.Lock()
		defer api.mu.Unlock()
		if len(api.keys) != 0 {
			return fmt.Errorf("%d keys remain after destroy", len(api.keys))
		}
		return nil
	}
}

func TestAPIKeyResourceLifecycle(t *testing.T) {
	requireTerraform(t)
	api := newAPIKeyTestAPI(t)
	const address = "opper_api_key.test"
	var originalID, replacementID string
	remember := func(dst *string) resource.TestCheckFunc {
		return func(s *terraform.State) error { *dst = s.RootModule().Resources[address].Primary.ID; return nil }
	}
	first := apiKeyTestConfig(api.server.URL, "service-key", "", "", false)
	renamed := apiKeyTestConfig(api.server.URL, "service-key-renamed", "", "", false)
	rotated := apiKeyTestConfig(api.server.URL, "service-key-renamed", "rotation-1", "", false)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkAPIKeyDestroyed(api),
		Steps: []resource.TestStep{
			{Config: first, Check: resource.ComposeTestCheckFunc(remember(&originalID), resource.TestCheckResourceAttr(address, "id", "42"), resource.TestCheckResourceAttr(address, "project_id", keyTestProjectID), resource.TestCheckResourceAttr(address, "kind", "project"), resource.TestCheckResourceAttr(address, "secret", "op-test-secret-42"), resource.TestCheckResourceAttr(address, "secret_available", "true"), resource.TestCheckResourceAttr(address, "created_at", "2026-11-03T09:00:00Z"))},
			{Config: first, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: resource.TestCheckResourceAttr(address, "secret", "op-test-secret-42")},
			{ResourceName: address, ImportState: true, ImportStateIdFunc: apiKeyImportID, ImportStateCheck: checkImportedAPIKeyMetadata, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"secret", "secret_available", "idempotency_key", "rotation"}},
			{Config: first, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: resource.TestCheckResourceAttr(address, "secret", "op-test-secret-42")},
			{Config: renamed, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionDestroyBeforeCreate)}}, Check: resource.TestCheckResourceAttr(address, "id", "43")},
			{Config: rotated, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionDestroyBeforeCreate)}}, Check: resource.ComposeTestCheckFunc(remember(&replacementID), resource.TestCheckResourceAttr(address, "id", "44"), resource.TestCheckResourceAttr(address, "secret", "op-test-secret-44"))},
			{PreConfig: func() { api.externalDelete(replacementID) }, Config: rotated, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionCreate)}}, Check: resource.TestCheckResourceAttr(address, "id", "45")},
		},
	})
	api.mu.Lock()
	defer api.mu.Unlock()
	if originalID == "" || api.creates != 4 || api.deletes != 3 || api.reads == 0 {
		t.Fatalf("unexpected lifecycle counts: creates=%d deletes=%d reads=%d", api.creates, api.deletes, api.reads)
	}
}

func TestAPIKeyResourceCreateBeforeDestroy(t *testing.T) {
	requireTerraform(t)
	api := newAPIKeyTestAPI(t)
	first := apiKeyTestConfig(api.server.URL, "service-key", "first", "", true)
	rotated := apiKeyTestConfig(api.server.URL, "service-key", "second", "", true)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkAPIKeyDestroyed(api),
		Steps: []resource.TestStep{
			{Config: first},
			{Config: rotated, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("opper_api_key.test", plancheck.ResourceActionCreateBeforeDestroy)}}, Check: func(*terraform.State) error {
				api.mu.Lock()
				defer api.mu.Unlock()
				if fmt.Sprint(api.events) != "[create:42 create:43 delete:42]" {
					return fmt.Errorf("replacement did not mint new key before revocation: %v", api.events)
				}
				return nil
			}},
		},
	})
}

func TestAPIKeyResourceReadFailurePreservesState(t *testing.T) {
	requireTerraform(t)
	for _, code := range []int{403, 500} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			api := newAPIKeyTestAPI(t)
			config := apiKeyTestConfig(api.server.URL, "service-key", "", "", false)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkAPIKeyDestroyed(api),
				Steps: []resource.TestStep{
					{Config: config},
					{PreConfig: func() { api.setFailure(http.MethodGet, code) }, Config: config, PlanOnly: true, ExpectError: regexp.MustCompile(strconv.Itoa(code))},
					{PreConfig: func() { api.setFailure(http.MethodGet, 0) }, Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr("opper_api_key.test", "id", "42"), resource.TestCheckResourceAttr("opper_api_key.test", "secret", "op-test-secret-42"))},
				},
			})
		})
	}
}

func TestAPIKeyResourceDeleteFailurePreservesState(t *testing.T) {
	requireTerraform(t)
	api := newAPIKeyTestAPI(t)
	config := apiKeyTestConfig(api.server.URL, "service-key", "", "", false)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkAPIKeyDestroyed(api),
		Steps: []resource.TestStep{
			{Config: config},
			{PreConfig: func() { api.setFailure(http.MethodDelete, 403) }, Config: config, Destroy: true, ExpectError: regexp.MustCompile(`403`)},
			{PreConfig: func() { api.setFailure(http.MethodDelete, 0) }, Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: resource.TestCheckResourceAttr("opper_api_key.test", "id", "42")},
		},
	})
}

func TestAPIKeyResourceMissingSecretRecovery(t *testing.T) {
	requireTerraform(t)
	api := newAPIKeyTestAPI(t)
	api.omitNextSecret = true
	config := apiKeyTestConfig(api.server.URL, "service-key", "", "first-operation", false)
	recovered := apiKeyTestConfig(api.server.URL, "service-key", "", "recovery-operation", false)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkAPIKeyDestroyed(api),
		Steps: []resource.TestStep{
			{Config: config, ExpectError: regexp.MustCompile(`(?i)secret`)},
			{PreConfig: func() {
				api.mu.Lock()
				defer api.mu.Unlock()
				if len(api.keys) != 1 || api.creates != 1 || api.deletes != 0 {
					t.Fatal("missing-secret response was automatically retried or revoked")
				}
			}, Config: recovered, Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr("opper_api_key.test", "id", "43"), resource.TestCheckResourceAttr("opper_api_key.test", "secret", "op-test-secret-43"), func(*terraform.State) error {
				api.mu.Lock()
				defer api.mu.Unlock()
				if api.deletes != 1 {
					return fmt.Errorf("known key ID was lost from state after failed creation")
				}
				return nil
			})},
		},
	})
}

func TestAPIKeyResourceRejectsPersonalImport(t *testing.T) {
	requireTerraform(t)
	api := newAPIKeyTestAPI(t)
	personalID := api.seedPersonal()
	config := apiKeyTestConfig(api.server.URL, "service-key", "", "", false)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true,
		Steps: []resource.TestStep{{Config: config, ResourceName: "opper_api_key.test", ImportState: true, ImportStateId: fmt.Sprintf("%s/%d", keyTestProjectID, personalID), ExpectError: regexp.MustCompile(`(?i)personal|project.owned`)}},
	})
	api.mu.Lock()
	defer api.mu.Unlock()
	if _, ok := api.keys[personalID]; !ok {
		t.Fatal("personal key was revoked")
	}
}

func TestAccAPIKeyResource(t *testing.T) {
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
	name := fmt.Sprintf("terraform-key-acc-%d", time.Now().UnixNano())
	config := fmt.Sprintf(`
provider "opper" {}
resource "opper_project" "test" { name = %q }
resource "opper_api_key" "test" {
 project_id = opper_project.test.id
 name = "acceptance-service"
}
`, name)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true,
		CheckDestroy: func(s *terraform.State) error {
			for _, rs := range s.RootModule().Resources {
				if rs.Type != "opper_api_key" || rs.Primary == nil || rs.Primary.ID == "" {
					continue
				}
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				keys, err := api.ListAPIKeys(ctx, rs.Primary.Attributes["project_id"])
				cancel()
				if client.IsNotFound(err) {
					continue
				}
				if err != nil {
					return err
				}
				for _, key := range keys {
					if strconv.FormatInt(key.ID, 10) == rs.Primary.ID {
						return fmt.Errorf("test runtime key still exists")
					}
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: config, Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr("opper_api_key.test", "kind", "project"), resource.TestCheckResourceAttr("opper_api_key.test", "secret_available", "true"), func(s *terraform.State) error {
				rs := s.RootModule().Resources["opper_api_key.test"]
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				keys, err := api.ListAPIKeys(ctx, rs.Primary.Attributes["project_id"])
				if err != nil {
					return err
				}
				for _, key := range keys {
					if strconv.FormatInt(key.ID, 10) == rs.Primary.ID && key.Kind == "project" {
						return nil
					}
				}
				return fmt.Errorf("project-owned key missing from API")
			})},
			{Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
			{ResourceName: "opper_api_key.test", ImportState: true, ImportStateIdFunc: apiKeyImportID, ImportStateCheck: checkImportedAPIKeyMetadata, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"secret", "secret_available", "idempotency_key", "rotation"}},
		},
	})
}

func TestAPIKeyResourceIdempotencyReplayRecovery(t *testing.T) {
	requireTerraform(t)
	api := newAPIKeyTestAPI(t)
	direct, err := client.New(api.server.URL, "op-mak-local-test", "test")
	if err != nil {
		t.Fatal(err)
	}
	original, err := direct.CreateAPIKey(context.Background(), keyTestProjectID, "service-key", "lost-operation")
	if err != nil || original.ID != 42 {
		t.Fatal("could not seed previously completed create")
	}
	config := apiKeyTestConfig(api.server.URL, "service-key", "", "lost-operation", false)
	recovered := apiKeyTestConfig(api.server.URL, "service-key", "", "replacement-operation", false)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkAPIKeyDestroyed(api),
		Steps: []resource.TestStep{
			{Config: config, ExpectError: regexp.MustCompile(`(?i)secret`)},
			{PreConfig: func() {
				api.mu.Lock()
				defer api.mu.Unlock()
				if api.creates != 1 || len(api.keys) != 1 {
					t.Fatal("replay minted an additional key")
				}
			}, Config: recovered, Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr("opper_api_key.test", "id", "43"), resource.TestCheckResourceAttr("opper_api_key.test", "secret_available", "true"), func(*terraform.State) error {
				api.mu.Lock()
				defer api.mu.Unlock()
				if api.creates != 2 || api.deletes != 1 {
					return fmt.Errorf("replay identity not retained for replacement cleanup")
				}
				return nil
			})},
		},
	})
}

func TestAPIKeyResourceMissingParentRemovesState(t *testing.T) {
	requireTerraform(t)
	api := newAPIKeyTestAPI(t)
	config := apiKeyTestConfig(api.server.URL, "service-key", "", "", false)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkAPIKeyDestroyed(api),
		Steps: []resource.TestStep{
			{Config: config},
			{PreConfig: func() { api.mu.Lock(); defer api.mu.Unlock(); api.projectExists = false; clear(api.keys) }, Config: config, PlanOnly: true, ExpectNonEmptyPlan: true},
		},
	})
}

func TestAPIKeyResourceRejectsReusedIdempotencyKeyOnReplacement(t *testing.T) {
	requireTerraform(t)
	for _, test := range []struct{ name, newName, newRotation string }{
		{name: "rotation", newName: "service-key", newRotation: "second"},
		{name: "name", newName: "renamed-service-key", newRotation: "first"},
	} {
		t.Run(test.name, func(t *testing.T) {
			api := newAPIKeyTestAPI(t)
			initial := apiKeyTestConfig(api.server.URL, "service-key", "first", "fixed-operation", false)
			replacement := apiKeyTestConfig(api.server.URL, test.newName, test.newRotation, "fixed-operation", false)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkAPIKeyDestroyed(api),
				Steps: []resource.TestStep{
					{Config: initial, Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr("opper_api_key.test", "id", "42"), resource.TestCheckResourceAttr("opper_api_key.test", "secret", "op-test-secret-42"))},
					{Config: replacement, PlanOnly: true, ExpectError: regexp.MustCompile(`Change the idempotency key`)},
					{PreConfig: func() {
						api.mu.Lock()
						defer api.mu.Unlock()
						if api.creates != 1 || api.deletes != 0 || len(api.keys) != 1 {
							t.Fatal("unsafe replacement changed runtime credentials before rejecting reused idempotency key")
						}
					}, Config: initial, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr("opper_api_key.test", "id", "42"), resource.TestCheckResourceAttr("opper_api_key.test", "secret", "op-test-secret-42"))},
					{Config: apiKeyTestConfig(api.server.URL, test.newName, test.newRotation, "replacement-operation", false), Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr("opper_api_key.test", "id", "43"), resource.TestCheckResourceAttr("opper_api_key.test", "secret", "op-test-secret-43"))},
				},
			})
		})
	}
}
