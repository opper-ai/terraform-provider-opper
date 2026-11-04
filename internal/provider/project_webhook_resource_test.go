package provider

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

func projectWebhookTestConfig(baseURL, projectID, url string) string {
	return fmt.Sprintf(`
provider "opper" {
 management_api_key = "op-mak-local-test"
 base_url = %q
}
resource "opper_project_webhook" "test" {
 project_uuid = %q
 url = %q
}
`, baseURL, projectID, url)
}

func checkWebhookWrites(api *projectWebhookTestAPI, patches, clears int) resource.TestCheckFunc {
	return func(*terraform.State) error {
		api.mu.Lock()
		defer api.mu.Unlock()
		if api.patches != patches || api.clears != clears {
			return fmt.Errorf("expected %d URL patches/%d clears, got %d/%d", patches, clears, api.patches, api.clears)
		}
		return nil
	}
}

func checkWebhookCleared(api *projectWebhookTestAPI) resource.TestCheckFunc {
	return func(*terraform.State) error {
		api.mu.Lock()
		defer api.mu.Unlock()
		overview, ok := api.projects[webhookTestProjectID]
		if !ok {
			return fmt.Errorf("destroy deleted the parent project")
		}
		if overview.URL != nil {
			return fmt.Errorf("destroy did not clear webhook URL")
		}
		if overview.SecretCreatedAt == nil || *overview.SecretCreatedAt != "2026-11-03T09:00:00Z" || overview.PreviousSecretValidUntil == nil || *overview.PreviousSecretValidUntil != "2026-11-04T09:00:00Z" {
			return fmt.Errorf("URL lifecycle changed signing-secret metadata")
		}
		return nil
	}
}

func TestProjectWebhookResourceLifecycle(t *testing.T) {
	requireTerraform(t)
	api := newProjectWebhookTestAPI(t)
	const address = "opper_project_webhook.test"
	first := projectWebhookTestConfig(api.server.URL, webhookTestProjectID, "https://example.com/terraform/first")
	updated := projectWebhookTestConfig(api.server.URL, webhookTestProjectID, "https://example.com/terraform/updated")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true,
		CheckDestroy: resource.ComposeTestCheckFunc(checkWebhookCleared(api), checkWebhookWrites(api, 4, 1)),
		Steps: []resource.TestStep{
			{Config: first, Check: resource.ComposeTestCheckFunc(
				resource.TestCheckResourceAttr(address, "id", webhookTestProjectID),
				resource.TestCheckResourceAttr(address, "project_uuid", webhookTestProjectID),
				resource.TestCheckResourceAttr(address, "url", "https://example.com/terraform/first"), checkWebhookWrites(api, 1, 0))},
			{Config: first, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: checkWebhookWrites(api, 1, 0)},
			{Config: updated, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)}}, Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr(address, "url", "https://example.com/terraform/updated"), checkWebhookWrites(api, 2, 0))},
			{ResourceName: address, ImportState: true, ImportStateVerify: true},
			{Config: updated, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: checkWebhookWrites(api, 2, 0)},
			{PreConfig: func() {
				api.setURL(webhookTestProjectID, webhookString("https://example.com/changed-outside-terraform"))
			}, Config: updated,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)}}, Check: checkWebhookWrites(api, 3, 0)},
		},
	})
}

func TestProjectWebhookResourceExternalClearRecreates(t *testing.T) {
	requireTerraform(t)
	api := newProjectWebhookTestAPI(t)
	config := projectWebhookTestConfig(api.server.URL, webhookTestProjectID, "https://example.com/terraform")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkWebhookCleared(api),
		Steps: []resource.TestStep{
			{Config: config},
			{PreConfig: func() { api.setURL(webhookTestProjectID, nil) }, Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("opper_project_webhook.test", plancheck.ResourceActionCreate)}}, Check: checkWebhookWrites(api, 2, 0)},
		},
	})
}

func TestProjectWebhookResourceProjectReplacement(t *testing.T) {
	requireTerraform(t)
	api := newProjectWebhookTestAPI(t)
	config := projectWebhookTestConfig(api.server.URL, webhookTestProjectID, "https://example.com/terraform")
	replacement := projectWebhookTestConfig(api.server.URL, webhookTestOtherProjectID, "https://example.com/terraform")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true,
		CheckDestroy: func(*terraform.State) error {
			api.mu.Lock()
			defer api.mu.Unlock()
			for id, overview := range api.projects {
				if overview.URL != nil {
					return fmt.Errorf("URL left on project %s", id)
				}
			}
			if len(api.projects) != 2 || api.clears != 2 {
				return fmt.Errorf("replacement changed parent projects or did not clear both URLs")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: config},
			{Config: replacement, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("opper_project_webhook.test", plancheck.ResourceActionDestroyBeforeCreate)}}, Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr("opper_project_webhook.test", "id", webhookTestOtherProjectID), checkWebhookWrites(api, 3, 1))},
		},
	})
}

func TestProjectWebhookResourceExistingURLRequiresImport(t *testing.T) {
	requireTerraform(t)
	for _, existing := range []string{"https://example.com/existing-owner", "https://example.com/terraform"} {
		t.Run(existing, func(t *testing.T) {
			api := newProjectWebhookTestAPI(t)
			api.setURL(webhookTestProjectID, &existing)
			config := projectWebhookTestConfig(api.server.URL, webhookTestProjectID, "https://example.com/terraform")
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true,
				Steps: []resource.TestStep{{Config: config, ExpectError: regexp.MustCompile(`(?i)import`)}},
			})
			api.mu.Lock()
			defer api.mu.Unlock()
			if api.patches != 0 || api.projects[webhookTestProjectID].URL == nil || *api.projects[webhookTestProjectID].URL != existing {
				t.Fatal("create overwrote an existing webhook")
			}
		})
	}
}

func TestProjectWebhookResourceUncertainCreateRequiresExplicitImport(t *testing.T) {
	requireTerraform(t)
	api := newProjectWebhookTestAPI(t)
	api.malformedNextPatchResponse = true
	config := projectWebhookTestConfig(api.server.URL, webhookTestProjectID, "https://example.com/terraform")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true,
		CheckDestroy: resource.ComposeTestCheckFunc(checkWebhookCleared(api), checkWebhookWrites(api, 2, 1)),
		Steps: []resource.TestStep{
			{Config: config, ExpectError: regexp.MustCompile(`(?i)import|uncertain`)},
			{PreConfig: func() {
				api.mu.Lock()
				defer api.mu.Unlock()
				if api.patches != 1 || api.clears != 0 || api.projects[webhookTestProjectID].URL == nil {
					t.Fatal("uncertain creation was retried or cleared before explicit import")
				}
			}, Config: config, ResourceName: "opper_project_webhook.test", ImportState: true, ImportStateId: webhookTestProjectID, ImportStatePersist: true,
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					if len(states) != 1 || states[0].ID != webhookTestProjectID || states[0].Attributes["url"] != "https://example.com/terraform" {
						return fmt.Errorf("explicit import did not recover uncertain webhook")
					}
					return nil
				}},
			{Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: checkWebhookWrites(api, 1, 0)},
		},
	})
}

func TestProjectWebhookResourceImportValidation(t *testing.T) {
	requireTerraform(t)
	for _, tc := range []struct {
		name, id string
		exists   bool
		want     string
	}{
		{"malformed", "not-a-uuid", true, `(?i)UUID`},
		{"uppercase", "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA", true, `(?i)UUID`},
		{"null_url", webhookTestProjectID, true, `(?i)non.existent|not.*configured|no.*webhook`},
		{"missing_project", webhookTestProjectID, false, `(?i)non.existent|404|not.*found`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := newProjectWebhookTestAPI(t)
			if !tc.exists {
				delete(api.projects, webhookTestProjectID)
			}
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true,
				Steps: []resource.TestStep{{Config: projectWebhookTestConfig(api.server.URL, webhookTestProjectID, "https://example.com/terraform"), ResourceName: "opper_project_webhook.test", ImportState: true, ImportStateId: tc.id, ExpectError: regexp.MustCompile(tc.want)}},
			})
			api.mu.Lock()
			defer api.mu.Unlock()
			if api.patches != 0 {
				t.Fatal("import wrote webhook settings")
			}
			if (tc.name == "malformed" || tc.name == "uppercase") && api.reads != 0 {
				t.Fatal("invalid import reached the API")
			}
		})
	}
}

func TestProjectWebhookResourceUnauthenticatedCreate(t *testing.T) {
	requireTerraform(t)
	api := newProjectWebhookTestAPI(t)
	config := strings.ReplaceAll(projectWebhookTestConfig(api.server.URL, webhookTestProjectID, "https://example.com/terraform"), "op-mak-local-test", "op-mak-unauthorized")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true,
		Steps: []resource.TestStep{{Config: config, ExpectError: regexp.MustCompile(`401`)}},
	})
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.patches != 0 || api.projects[webhookTestProjectID].URL != nil {
		t.Fatal("authentication failure changed webhook settings")
	}
}

func TestProjectWebhookResourceMissingProject(t *testing.T) {
	requireTerraform(t)
	api := newProjectWebhookTestAPI(t)
	delete(api.projects, webhookTestProjectID)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true,
		Steps: []resource.TestStep{{Config: projectWebhookTestConfig(api.server.URL, webhookTestProjectID, "https://example.com/terraform"), ExpectError: regexp.MustCompile(`404`)}},
	})
	if api.patches != 0 {
		t.Fatal("missing project caused a webhook write")
	}
}

func TestProjectWebhookResourceExternalProjectDeletion(t *testing.T) {
	requireTerraform(t)
	api := newProjectWebhookTestAPI(t)
	config := projectWebhookTestConfig(api.server.URL, webhookTestProjectID, "https://example.com/terraform")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true,
		Steps: []resource.TestStep{
			{Config: config},
			{PreConfig: func() { api.mu.Lock(); delete(api.projects, webhookTestProjectID); api.mu.Unlock() }, Config: config, PlanOnly: true, ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectResourceAction("opper_project_webhook.test", plancheck.ResourceActionCreate)}}},
		},
	})
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.patches != 1 {
		t.Fatal("deleting the parent caused another webhook write")
	}
}

func TestProjectWebhookResourceAPIFailurePreservesState(t *testing.T) {
	requireTerraform(t)
	api := newProjectWebhookTestAPI(t)
	config := projectWebhookTestConfig(api.server.URL, webhookTestProjectID, "https://example.com/terraform")
	updated := projectWebhookTestConfig(api.server.URL, webhookTestProjectID, "https://example.com/updated")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkWebhookCleared(api),
		Steps: []resource.TestStep{
			{Config: config},
			{PreConfig: func() { api.mu.Lock(); api.getFailure = http.StatusForbidden; api.mu.Unlock() }, Config: config, PlanOnly: true, ExpectError: regexp.MustCompile(`403`)},
			{PreConfig: func() { api.mu.Lock(); api.getFailure = 0; api.mu.Unlock() }, Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: checkWebhookWrites(api, 1, 0)},
			{PreConfig: func() { api.mu.Lock(); api.patchFailure = http.StatusForbidden; api.mu.Unlock() }, Config: updated, ExpectError: regexp.MustCompile(`403`)},
			{PreConfig: func() { api.mu.Lock(); api.patchFailure = 0; api.mu.Unlock() }, Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: checkWebhookWrites(api, 1, 0)},
			{PreConfig: func() { api.mu.Lock(); api.patchFailure = http.StatusForbidden; api.mu.Unlock() }, Config: config, Destroy: true, ExpectError: regexp.MustCompile(`403`)},
			{PreConfig: func() { api.mu.Lock(); api.patchFailure = 0; api.mu.Unlock() }, Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr("opper_project_webhook.test", "id", webhookTestProjectID), checkWebhookWrites(api, 1, 0))},
		},
	})
}

func TestProjectWebhookResourceConcurrentDestroyRefusesChangedURL(t *testing.T) {
	requireTerraform(t)
	for _, interleaved := range []bool{false, true} {
		t.Run(fmt.Sprintf("interleaved_%t", interleaved), func(t *testing.T) {
			api := newProjectWebhookTestAPI(t)
			config := projectWebhookTestConfig(api.server.URL, webhookTestProjectID, "https://example.com/terraform")
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkWebhookCleared(api),
				Steps: []resource.TestStep{
					{Config: config},
					{PreConfig: func() {
						api.mu.Lock()
						if interleaved {
							api.replaceAfterNextRead = webhookString("https://example.com/concurrent-owner")
						} else {
							ov := api.projects[webhookTestProjectID]
							ov.URL = webhookString("https://example.com/concurrent-owner")
							api.projects[webhookTestProjectID] = ov
						}
						api.mu.Unlock()
					}, Config: config, Destroy: true, ExpectError: regexp.MustCompile(`(?i)changed|differ|refus|conflict`)},
					{PreConfig: func() {
						api.mu.Lock()
						defer api.mu.Unlock()
						if api.patches != 1 || api.projects[webhookTestProjectID].URL == nil || *api.projects[webhookTestProjectID].URL != "https://example.com/concurrent-owner" {
							t.Fatal("destroy overwrote a concurrent webhook edit")
						}
						ov := api.projects[webhookTestProjectID]
						ov.URL = webhookString("https://example.com/terraform")
						api.projects[webhookTestProjectID] = ov
					}, Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: checkWebhookWrites(api, 1, 0)},
				},
			})
		})
	}
}

func TestAccProjectWebhookResource(t *testing.T) {
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
	name := fmt.Sprintf("terraform-webhook-acc-%d", time.Now().UnixNano())
	firstURL := "https://example.com/" + name + "/first"
	updatedURL := "https://example.com/" + name + "/updated"
	config := func(url string) string {
		webhook := ""
		if url != "" {
			webhook = fmt.Sprintf(`resource "opper_project_webhook" "test" {
 project_uuid = opper_project.test.id
 url = %q
}`, url)
		}
		return fmt.Sprintf("provider \"opper\" {}\nresource \"opper_project\" \"test\" { name = %q }\n%s\n", name, webhook)
	}
	var projectID string
	var secretCreatedAt string
	checkOverview := func(wantURL *string) resource.TestCheckFunc {
		return func(s *terraform.State) error {
			projectID = s.RootModule().Resources["opper_project.test"].Primary.ID
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			overview, err := api.GetProjectWebhook(ctx, projectID)
			if err != nil {
				return err
			}
			if (overview.URL == nil) != (wantURL == nil) || (wantURL != nil && *overview.URL != *wantURL) {
				return fmt.Errorf("unexpected project webhook URL")
			}
			// The backend creates the first signing secret when the URL is saved.
			// Later overview/read/update/import/clear must retain that secret.
			if secretCreatedAt == "" && wantURL == nil {
				if overview.SecretCreatedAt != nil {
					return fmt.Errorf("fresh project already has a signing secret")
				}
			} else {
				if overview.SecretCreatedAt == nil {
					return fmt.Errorf("URL save did not initialize its signing secret")
				}
				if secretCreatedAt == "" {
					secretCreatedAt = *overview.SecretCreatedAt
				}
				if *overview.SecretCreatedAt != secretCreatedAt {
					return fmt.Errorf("URL lifecycle rotated its signing secret")
				}
			}
			if overview.PreviousSecretValidUntil != nil {
				return fmt.Errorf("URL lifecycle created a secret rotation overlap")
			}
			return nil
		}
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true,
		CheckDestroy: func(*terraform.State) error {
			if projectID == "" {
				return nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, err := api.GetProject(ctx, projectID)
			if err == nil {
				return fmt.Errorf("task-owned webhook project still exists")
			}
			if !client.IsNotFound(err) {
				return err
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: config(""), Check: checkOverview(nil)},
			{Config: config(firstURL), Check: resource.ComposeTestCheckFunc(checkOverview(&firstURL), resource.TestCheckResourceAttrPair("opper_project_webhook.test", "id", "opper_project.test", "id"))},
			{Config: config(firstURL), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: checkOverview(&firstURL)},
			{Config: config(updatedURL), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("opper_project_webhook.test", plancheck.ResourceActionUpdate)}}, Check: checkOverview(&updatedURL)},
			{ResourceName: "opper_project_webhook.test", ImportState: true, ImportStateVerify: true},
			{Config: config(updatedURL), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: checkOverview(&updatedURL)},
			{PreConfig: func() {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				driftURL := "https://example.com/" + name + "/external-edit"
				if _, err := api.UpdateProjectWebhook(ctx, projectID, &driftURL); err != nil {
					t.Fatal(err)
				}
			}, Config: config(updatedURL), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("opper_project_webhook.test", plancheck.ResourceActionUpdate)}}, Check: checkOverview(&updatedURL)},
			// Retain the parent while destroying only the webhook, proving destroy
			// clears the URL independently of eventual parent-project deletion.
			{Config: config(""), Check: checkOverview(nil)},
		},
	})
}
