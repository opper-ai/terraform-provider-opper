package provider

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

func projectProviderFactories() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){"opper": providerserver.NewProtocol6WithError(New("test")())}
}

func requireTerraform(t *testing.T) {
	t.Helper()
	if os.Getenv("TF_ACC_TERRAFORM_PATH") != "" {
		return
	}
	path, err := exec.LookPath("terraform")
	if err != nil {
		t.Skip("Terraform CLI is required for provider lifecycle tests")
	}
	t.Setenv("TF_ACC_TERRAFORM_PATH", path)
}

func projectTestConfig(baseURL, name, description string, includeDescription bool) string {
	desc := ""
	if includeDescription {
		desc = fmt.Sprintf("description = %q", description)
	}
	return fmt.Sprintf(`
provider "opper" {
  management_api_key = "op-mak-local-test"
  base_url = %q
}
resource "opper_project" "test" {
  name = %q
  %s
}
data "opper_project" "test" {
  id = opper_project.test.id
}
`, baseURL, name, desc)
}

func TestProjectResourceLifecycle(t *testing.T) {
	requireTerraform(t)
	api := newProjectTestAPI(t)
	const address = "opper_project.test"
	var originalID, replacementID string
	checkID := func(dst *string) resource.TestCheckFunc {
		return func(s *terraform.State) error {
			rs, ok := s.RootModule().Resources[address]
			if !ok || rs.Primary.ID == "" {
				return fmt.Errorf("project ID missing from state")
			}
			*dst = rs.Primary.ID
			return nil
		}
	}
	checkWrites := func(creates, updates int) resource.TestCheckFunc {
		return func(_ *terraform.State) error {
			api.mu.Lock()
			defer api.mu.Unlock()
			if api.creates != creates || api.updates != updates {
				return fmt.Errorf("expected %d creates/%d updates, got %d/%d", creates, updates, api.creates, api.updates)
			}
			return nil
		}
	}
	first := projectTestConfig(api.server.URL, "terraform-project", "initial description", true)
	updated := projectTestConfig(api.server.URL, "terraform-project-renamed", "updated description", true)
	cleared := projectTestConfig(api.server.URL, "terraform-project-renamed", "", false)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories:  projectProviderFactories(),
		PreventPostDestroyRefresh: true,
		CheckDestroy: func(_ *terraform.State) error {
			api.mu.Lock()
			defer api.mu.Unlock()
			if len(api.projects) != 0 {
				return fmt.Errorf("project was not deleted: %d remain", len(api.projects))
			}
			if api.deletes != 1 {
				return fmt.Errorf("expected one DELETE request, got %d", api.deletes)
			}
			if api.reads == 0 {
				return fmt.Errorf("expected project refresh requests")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: first, Check: resource.ComposeTestCheckFunc(
				checkID(&originalID), checkWrites(1, 0),
				resource.TestCheckResourceAttr(address, "name", "terraform-project"),
				resource.TestCheckResourceAttr(address, "description", "initial description"),
				resource.TestCheckResourceAttr(address, "created_at", "2026-11-03T09:00:00Z"),
				resource.TestCheckResourceAttrPair("data.opper_project.test", "id", address, "id"),
				resource.TestCheckResourceAttr("data.opper_project.test", "description", "initial description"),
			)},
			{Config: first, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: checkWrites(1, 0)},
			{Config: updated, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)}}, Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttrWith(address, "id", func(id string) error {
					if id != originalID {
						return fmt.Errorf("rename replaced project")
					}
					return nil
				}),
				resource.TestCheckResourceAttr(address, "name", "terraform-project-renamed"),
				resource.TestCheckResourceAttr(address, "description", "updated description"), checkWrites(1, 1),
			)},
			{Config: cleared, Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr(address, "description", ""), checkWrites(1, 2))},
			{ResourceName: address, ImportState: true, ImportStateVerify: true},
			{PreConfig: func() { api.drift(originalID, "ui-edit", "edited outside Terraform") }, Config: cleared,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)}},
				Check:            resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr(address, "name", "terraform-project-renamed"), resource.TestCheckResourceAttr(address, "description", ""), checkWrites(1, 3))},
			{PreConfig: func() { api.externalDelete(originalID) }, Config: cleared,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionCreate)}},
				Check: resource.ComposeTestCheckFunc(checkID(&replacementID), checkWrites(2, 3), func(_ *terraform.State) error {
					if replacementID == originalID {
						return fmt.Errorf("externally deleted project was not recreated")
					}
					return nil
				})},
		},
	})
}

func TestProjectDataSourceNotFound(t *testing.T) {
	requireTerraform(t)
	api := newProjectTestAPI(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(),
		Steps: []resource.TestStep{{Config: fmt.Sprintf(`
provider "opper" {
management_api_key = "op-mak-local-test"
base_url = %q
}
data "opper_project" "missing" { id = "11111111-1111-4111-8111-999999999999" }
`, api.server.URL), ExpectError: regexp.MustCompile(`404`)}},
	})
}

func TestProjectResourceAuthenticationFailure(t *testing.T) {
	requireTerraform(t)
	api := newProjectTestAPI(t)
	config := strings.ReplaceAll(projectTestConfig(api.server.URL, "unauthenticated", "", false), "op-mak-local-test", "op-mak-wrong")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(),
		Steps:                    []resource.TestStep{{Config: config, ExpectError: regexp.MustCompile(`401`)}},
	})
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.projects) != 0 || api.creates != 0 {
		t.Fatal("authentication failure created a project")
	}
}

func TestAccProjectResource(t *testing.T) {
	if os.Getenv("TF_ACC") != "1" {
		t.Skip("set TF_ACC=1 to run live Opper acceptance tests")
	}
	if os.Getenv("OPPER_TEST_DISPOSABLE_ORG") != "1" {
		t.Skip("set OPPER_TEST_DISPOSABLE_ORG=1 only for an isolated disposable test organization")
	}
	key := os.Getenv("OPPER_MANAGEMENT_API_KEY")
	if strings.TrimSpace(key) == "" {
		t.Fatal("OPPER_MANAGEMENT_API_KEY is required for live acceptance tests")
	}
	requireTerraform(t)
	baseURL := os.Getenv("OPPER_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.opper.ai"
	}
	api, err := client.New(baseURL, key, "acceptance")
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("terraform-acc-%d", time.Now().UnixNano())
	liveConfig := func(projectName, description string, includeDescription bool) string {
		desc := ""
		if includeDescription {
			desc = fmt.Sprintf("description = %q", description)
		}
		return fmt.Sprintf(`
provider "opper" {}
resource "opper_project" "test" {
 name = %q
 %s
}
data "opper_project" "test" { id = opper_project.test.id }
`, projectName, desc)
	}
	config := liveConfig(name, "Disposable Terraform acceptance test", true)
	updated := liveConfig(name+"-renamed", "Updated disposable test", true)
	cleared := liveConfig(name+"-renamed", "", false)
	var projectID string
	rememberID := func(s *terraform.State) error {
		projectID = s.RootModule().Resources["opper_project.test"].Primary.ID
		if projectID == "" {
			return fmt.Errorf("missing live project ID")
		}
		return nil
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories:  projectProviderFactories(),
		PreventPostDestroyRefresh: true,
		CheckDestroy: func(s *terraform.State) error {
			for _, rs := range s.RootModule().Resources {
				if rs.Type != "opper_project" || rs.Primary == nil || rs.Primary.ID == "" {
					continue
				}
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				_, err := api.GetProject(ctx, rs.Primary.ID)
				cancel()
				if err == nil {
					return fmt.Errorf("test project %s still exists", rs.Primary.ID)
				}
				if !client.IsNotFound(err) {
					return err
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: config, Check: resource.ComposeTestCheckFunc(rememberID, resource.TestCheckResourceAttr("opper_project.test", "name", name), resource.TestCheckResourceAttrPair("data.opper_project.test", "id", "opper_project.test", "id"))},
			{Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
			{Config: updated, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("opper_project.test", plancheck.ResourceActionUpdate)}}, Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr("opper_project.test", "description", "Updated disposable test"), resource.TestCheckResourceAttrWith("opper_project.test", "id", func(id string) error {
				if id != projectID {
					return fmt.Errorf("rename replaced the live project")
				}
				return nil
			}))},
			{Config: cleared, Check: resource.TestCheckResourceAttr("opper_project.test", "description", "")},
			{ResourceName: "opper_project.test", ImportState: true, ImportStateVerify: true},
			{PreConfig: func() {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				driftName, driftDescription := name+"-ui-edit", "Edited outside Terraform"
				if _, err := api.UpdateProject(ctx, projectID, &driftName, &driftDescription); err != nil {
					t.Fatal(err)
				}
			}, Config: cleared,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("opper_project.test", plancheck.ResourceActionUpdate)}},
				Check:            resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr("opper_project.test", "name", name+"-renamed"), resource.TestCheckResourceAttr("opper_project.test", "description", ""))},
			{PreConfig: func() {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if err := api.DeleteProject(ctx, projectID); err != nil {
					t.Fatal(err)
				}
			}, Config: cleared,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("opper_project.test", plancheck.ResourceActionCreate)}},
				Check: resource.TestCheckResourceAttrWith("opper_project.test", "id", func(id string) error {
					if id == projectID {
						return fmt.Errorf("deleted live project was not recreated")
					}
					return nil
				})},
		},
	})
}

func TestProjectProviderEnvironment(t *testing.T) {
	requireTerraform(t)
	api := newProjectTestAPI(t)
	t.Setenv("OPPER_MANAGEMENT_API_KEY", "op-mak-local-test")
	t.Setenv("OPPER_BASE_URL", api.server.URL)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(),
		Steps: []resource.TestStep{{Config: `
provider "opper" {}
resource "opper_project" "test" { name = "environment-configured" }
`, Check: resource.TestCheckResourceAttr("opper_project.test", "description", "")}},
		CheckDestroy: func(_ *terraform.State) error {
			api.mu.Lock()
			defer api.mu.Unlock()
			if len(api.projects) != 0 {
				return fmt.Errorf("environment-configured project was not deleted")
			}
			return nil
		},
	})
}

func TestProjectProviderMissingCredential(t *testing.T) {
	requireTerraform(t)
	t.Setenv("OPPER_MANAGEMENT_API_KEY", "")
	t.Setenv("OPPER_BASE_URL", "")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(),
		Steps: []resource.TestStep{{Config: `
provider "opper" {}
data "opper_project" "test" { id = "11111111-1111-4111-8111-999999999999" }
`, ExpectError: regexp.MustCompile(`Missing management API key`)}},
	})
}

func TestProjectResourcePreservesStateOnAPIFailure(t *testing.T) {
	requireTerraform(t)
	api := newProjectTestAPI(t)
	config := projectTestConfig(api.server.URL, "recoverable-project", "", false)
	var id string
	sameID := func(s *terraform.State) error {
		got := s.RootModule().Resources["opper_project.test"].Primary.ID
		if id == "" {
			id = got
		} else if got != id {
			return fmt.Errorf("API error lost project state: expected %s, got %s", id, got)
		}
		api.mu.Lock()
		defer api.mu.Unlock()
		if api.creates != 1 || api.deletes != 0 {
			return fmt.Errorf("API failure recreated or deleted the project: %d creates/%d deletes", api.creates, api.deletes)
		}
		return nil
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true,
		Steps: []resource.TestStep{
			{Config: config, Check: sameID},
			{PreConfig: func() { api.mu.Lock(); api.getFailure = 403; api.mu.Unlock() }, Config: config, ExpectError: regexp.MustCompile(`403`)},
			{PreConfig: func() { api.mu.Lock(); api.getFailure = 0; api.mu.Unlock() }, Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: sameID},
			{PreConfig: func() { api.mu.Lock(); api.deleteFailure = 403; api.mu.Unlock() }, Config: config, Destroy: true, ExpectError: regexp.MustCompile(`403`)},
			{PreConfig: func() { api.mu.Lock(); api.deleteFailure = 0; api.mu.Unlock() }, Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: sameID},
		},
		CheckDestroy: func(_ *terraform.State) error {
			api.mu.Lock()
			defer api.mu.Unlock()
			if len(api.projects) != 0 || api.deletes != 1 {
				return fmt.Errorf("final destroy did not remove the retained project")
			}
			return nil
		},
	})
}

func TestProjectResourceProtectedNameDescriptionUpdate(t *testing.T) {
	requireTerraform(t)
	api := newProjectTestAPI(t)
	// The Opper organization's default project rejects any PATCH containing
	// name, including its current value. A description-only edit is permitted.
	api.rejectNamePatch = true
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true,
		Steps: []resource.TestStep{
			{Config: projectTestConfig(api.server.URL, "default", "original", true)},
			{Config: projectTestConfig(api.server.URL, "default", "updated description", true),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("opper_project.test", plancheck.ResourceActionUpdate)}},
				Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr("opper_project.test", "description", "updated description"), func(_ *terraform.State) error {
					api.mu.Lock()
					defer api.mu.Unlock()
					if len(api.lastPatchFields) != 1 || !api.lastPatchFields["description"] {
						return fmt.Errorf("description-only change sent unexpected PATCH fields: %v", api.lastPatchFields)
					}
					return nil
				})},
		},
	})
}

func TestProjectResourceNameOnlyUpdate(t *testing.T) {
	requireTerraform(t)
	api := newProjectTestAPI(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true,
		Steps: []resource.TestStep{
			{Config: projectTestConfig(api.server.URL, "rename-source", "preserved description", true)},
			{Config: projectTestConfig(api.server.URL, "rename-target", "preserved description", true),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("opper_project.test", plancheck.ResourceActionUpdate)}},
				Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr("opper_project.test", "name", "rename-target"), resource.TestCheckResourceAttr("opper_project.test", "description", "preserved description"), func(_ *terraform.State) error {
					api.mu.Lock()
					defer api.mu.Unlock()
					if len(api.lastPatchFields) != 1 || !api.lastPatchFields["name"] {
						return fmt.Errorf("name-only change sent unexpected PATCH fields: %v", api.lastPatchFields)
					}
					return nil
				})},
		},
	})
}
