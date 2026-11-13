package provider

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

func apiKeyAllowedIPsConfig(baseURL, ips, rotation string) string {
	optional := ""
	if ips != "" {
		optional = "allowed_ips = " + ips
	}
	if rotation != "" {
		optional += fmt.Sprintf("\nrotation = %q", rotation)
	}
	return fmt.Sprintf(`
provider "opper" {
 management_api_key = "op-mak-local-test"
 base_url = %q
}
resource "opper_api_key" "test" {
 project_id = %q
 name = "service-key"
 %s
 lifecycle { create_before_destroy = true }
}
`, baseURL, keyTestProjectID, optional)
}

func TestAPIKeyAllowedIPsImportAndGuardedRotation(t *testing.T) {
	requireTerraform(t)
	api := newAPIKeyTestAPI(t)
	const address = "opper_api_key.test"
	direct, err := client.New(api.server.URL, "op-mak-local-test", "test")
	if err != nil {
		t.Fatal(err)
	}
	key, err := direct.CreateAPIKeyWithAllowedIPs(context.Background(), keyTestProjectID, "service-key", "", []string{"8.8.8.8"})
	if err != nil || key.ID != 42 {
		t.Fatal("could not seed restricted runtime key")
	}
	omitted := apiKeyAllowedIPsConfig(api.server.URL, "", "")
	rotated := apiKeyAllowedIPsConfig(api.server.URL, "", "rotate")
	explicitRotation := apiKeyAllowedIPsConfig(api.server.URL, `["8.8.8.8"]`, "rotate")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkAPIKeyDestroyed(api),
		Steps: []resource.TestStep{
			{Config: omitted, ResourceName: address, ImportState: true, ImportStatePersist: true, ImportStateId: keyTestProjectID + "/42", ImportStateCheck: checkImportedAPIKeyMetadata},
			{Config: omitted, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: resource.ComposeTestCheckFunc(resource.TestCheckTypeSetElemAttr(address, "allowed_ips.*", "8.8.8.8"), resource.TestCheckResourceAttr(address, "secret_available", "false"))},
			{Config: rotated, PlanOnly: true, ExpectError: regexp.MustCompile(`Configure the replacement key's allowlist`)},
			{PreConfig: func() {
				api.mu.Lock()
				defer api.mu.Unlock()
				if api.creates != 1 || api.deletes != 0 {
					t.Fatal("refused rotation changed the key")
				}
			}, Config: explicitRotation, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionCreateBeforeDestroy)}}, Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr(address, "id", "43"), resource.TestCheckTypeSetElemAttr(address, "allowed_ips.*", "8.8.8.8"), resource.TestCheckResourceAttr(address, "secret", "op-test-secret-43"))},
			{Config: explicitRotation, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
		},
	})
}

func TestAPIKeyAllowedIPsCanonicalEquivalentChangeDoesNotPatch(t *testing.T) {
	requireTerraform(t)
	api := newAPIKeyTestAPI(t)
	first := apiKeyAllowedIPsConfig(api.server.URL, `["8.8.8.9/24", "8.8.8.0/24"]`, "")
	canonical := apiKeyAllowedIPsConfig(api.server.URL, `["8.8.8.0/24"]`, "")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkAPIKeyDestroyed(api),
		Steps: []resource.TestStep{
			{Config: first},
			{Config: canonical, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("opper_api_key.test", plancheck.ResourceActionUpdate)}}},
			{Config: canonical, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
		},
	})
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.updates != 0 || api.creates != 1 {
		t.Fatalf("equivalent policy caused a write: patches=%d creates=%d", api.updates, api.creates)
	}
}

func TestAPIKeyAllowedIPsRestrictedReplacementRequiresKnownPolicy(t *testing.T) {
	requireTerraform(t)
	for _, field := range []string{"name", "project", "receipt", "unknown-policy"} {
		t.Run(field, func(t *testing.T) {
			api := newAPIKeyTestAPI(t)
			first := apiKeyAllowedIPsConfig(api.server.URL, `["8.8.8.8"]`, "")
			replacement := apiKeyAllowedIPsConfig(api.server.URL, "", "")
			switch field {
			case "name":
				replacement = strings.Replace(replacement, `name = "service-key"`, `name = "replacement"`, 1)
			case "project":
				replacement = strings.Replace(replacement, keyTestProjectID, initialAccessTestUser, 1)
			case "receipt":
				replacement = strings.Replace(replacement, `name = "service-key"`, "name = \"service-key\"\nidempotency_key = \"replacement-receipt\"", 1)
			case "unknown-policy":
				replacement = apiKeyAllowedIPsConfig(api.server.URL, `[terraform_data.ip.output]`, "replace") + `
resource "terraform_data" "ip" { input = "8.8.8.8" }
`
			}
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkAPIKeyDestroyed(api),
				Steps: []resource.TestStep{
					{Config: first},
					{Config: replacement, PlanOnly: true, ExpectError: regexp.MustCompile(`Configure the replacement key's allowlist`)},
					{Config: first, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
				},
			})
			api.mu.Lock()
			defer api.mu.Unlock()
			if api.creates != 1 || api.deletes != 1 || api.updates != 0 {
				t.Fatal("refused replacement changed key lifecycle")
			}
		})
	}
}

func TestAPIKeyAllowedIPsDriftAndOmission(t *testing.T) {
	requireTerraform(t)
	api := newAPIKeyTestAPI(t)
	const address = "opper_api_key.test"
	configured := apiKeyAllowedIPsConfig(api.server.URL, `["1.1.1.1"]`, "")
	omitted := apiKeyAllowedIPsConfig(api.server.URL, "", "")
	external := func(ip string) func() {
		return func() {
			api.mu.Lock()
			defer api.mu.Unlock()
			key := api.keys[42]
			key.AllowedIPs = []string{ip}
			api.keys[42] = key
		}
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkAPIKeyDestroyed(api),
		Steps: []resource.TestStep{
			{Config: configured},
			{PreConfig: external("8.8.8.8"), Config: configured, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)}}, Check: resource.TestCheckResourceAttr(address, "secret", "op-test-secret-42")},
			{Config: omitted, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
			{PreConfig: external("9.9.9.9"), Config: omitted, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: resource.TestCheckTypeSetElemAttr(address, "allowed_ips.*", "9.9.9.9")},
		},
	})
}

func TestAPIKeyAllowedIPsUpdateFailurePreservesIdentityAndSecret(t *testing.T) {
	requireTerraform(t)
	for _, code := range []int{403, 404, 409, 500} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			api := newAPIKeyTestAPI(t)
			first := apiKeyAllowedIPsConfig(api.server.URL, `["8.8.8.8"]`, "")
			updated := apiKeyAllowedIPsConfig(api.server.URL, `["1.1.1.1"]`, "")
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkAPIKeyDestroyed(api),
				Steps: []resource.TestStep{
					{Config: first},
					{PreConfig: func() { api.setFailure(http.MethodPatch, code) }, Config: updated, ExpectError: regexp.MustCompile(fmt.Sprint(code))},
					{PreConfig: func() { api.setFailure(http.MethodPatch, 0) }, Config: first, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr("opper_api_key.test", "id", "42"), resource.TestCheckResourceAttr("opper_api_key.test", "secret", "op-test-secret-42"))},
				},
			})
		})
	}
}

func TestAPIKeyAllowedIPsUnsupportedCreateDoesNotMint(t *testing.T) {
	requireTerraform(t)
	api := newAPIKeyTestAPI(t)
	api.unsupportedAllowlist = true
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkAPIKeyDestroyed(api), Steps: []resource.TestStep{{Config: apiKeyAllowedIPsConfig(api.server.URL, `["8.8.8.8"]`, ""), ExpectError: regexp.MustCompile(`(?i)allowlist|allowed_ips|support`)}}})
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.creates != 0 || api.deletes != 0 || api.updates != 0 {
		t.Fatal("unsupported backend was mutated")
	}
}

func TestAPIKeyAllowedIPsUnconfirmedCreateRetainsRecoveryIdentity(t *testing.T) {
	requireTerraform(t)
	api := newAPIKeyTestAPI(t)
	api.unconfirmedCreateIPs = true
	config := apiKeyAllowedIPsConfig(api.server.URL, `["8.8.8.8"]`, "")
	recovery := apiKeyAllowedIPsConfig(api.server.URL, `["8.8.8.8"]`, "explicit-replacement")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkAPIKeyDestroyed(api),
		Steps: []resource.TestStep{
			{Config: config, ExpectError: regexp.MustCompile(`(?i)allowlist.*not confirmed`)},
			{PreConfig: func() {
				api.mu.Lock()
				defer api.mu.Unlock()
				api.unconfirmedCreateIPs = false
				if api.creates != 1 || api.deletes != 0 {
					t.Fatal("unconfirmed creation was retried or automatically revoked")
				}
			}, Config: recovery, Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr("opper_api_key.test", "id", "43"), resource.TestCheckResourceAttr("opper_api_key.test", "secret", "op-test-secret-43"))},
		},
	})
}

func TestAPIKeyAllowedIPsMissingReadDoesNotClearRestrictions(t *testing.T) {
	requireTerraform(t)
	api := newAPIKeyTestAPI(t)
	config := apiKeyAllowedIPsConfig(api.server.URL, `["8.8.8.8"]`, "")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkAPIKeyDestroyed(api),
		Steps: []resource.TestStep{
			{Config: config},
			{PreConfig: func() { api.mu.Lock(); api.omitAllowedIPs = true; api.mu.Unlock() }, Config: config, PlanOnly: true, ExpectError: regexp.MustCompile(`(?i)allowed_ips|allowlist`)},
			{PreConfig: func() { api.mu.Lock(); api.omitAllowedIPs = false; api.mu.Unlock() }, Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: resource.TestCheckResourceAttr("opper_api_key.test", "secret", "op-test-secret-42")},
		},
	})
}

func TestAPIKeyAllowedIPsUnknownValuesResolveAtApply(t *testing.T) {
	requireTerraform(t)
	api := newAPIKeyTestAPI(t)
	config := apiKeyAllowedIPsConfig(api.server.URL, `[terraform_data.ip.output]`, "") + `
resource "terraform_data" "ip" { input = "8.8.8.8" }
`
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkAPIKeyDestroyed(api), Steps: []resource.TestStep{{Config: config, Check: resource.TestCheckTypeSetElemAttr("opper_api_key.test", "allowed_ips.*", "8.8.8.8")}, {Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}}}})
}

func TestAPIKeyAllowedIPsLifecycle(t *testing.T) {
	requireTerraform(t)
	api := newAPIKeyTestAPI(t)
	const address = "opper_api_key.test"
	first := apiKeyAllowedIPsConfig(api.server.URL, `["8.8.8.9/24", "2001:4860:4860::8888/128", "8.8.8.0/24"]`, "first")
	reordered := apiKeyAllowedIPsConfig(api.server.URL, `["8.8.8.0/24", "2001:4860:4860::8888/128", "8.8.8.9/24"]`, "first")
	updated := apiKeyAllowedIPsConfig(api.server.URL, `["1.1.1.1"]`, "first")
	removed := apiKeyAllowedIPsConfig(api.server.URL, "", "first")
	cleared := apiKeyAllowedIPsConfig(api.server.URL, `[]`, "first")
	keyUnchanged := resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr(address, "id", "42"), resource.TestCheckResourceAttr(address, "secret", "op-test-secret-42"), resource.TestCheckResourceAttr(address, "secret_available", "true"))
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkAPIKeyDestroyed(api),
		Steps: []resource.TestStep{
			{Config: first, Check: resource.ComposeTestCheckFunc(keyUnchanged, resource.TestCheckResourceAttr(address, "allowed_ips.#", "3"))},
			{Config: first, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
			{Config: reordered, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
			{Config: updated, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)}}, Check: resource.ComposeTestCheckFunc(keyUnchanged, resource.TestCheckTypeSetElemAttr(address, "allowed_ips.*", "1.1.1.1"))},
			{Config: removed, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: keyUnchanged},
			{Config: cleared, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)}}, Check: resource.ComposeTestCheckFunc(keyUnchanged, resource.TestCheckResourceAttr(address, "allowed_ips.#", "0"))},
			{Config: cleared, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
		},
	})
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.creates != 1 || api.deletes != 1 {
		t.Fatalf("allowlist changes rotated the key: creates=%d deletes=%d", api.creates, api.deletes)
	}
}

func TestAPIKeyAllowedIPsUnconfirmedUpdatePreservesSecret(t *testing.T) {
	requireTerraform(t)
	api := newAPIKeyTestAPI(t)
	first := apiKeyAllowedIPsConfig(api.server.URL, `["8.8.8.8"]`, "")
	updated := apiKeyAllowedIPsConfig(api.server.URL, `["1.1.1.1"]`, "")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: checkAPIKeyDestroyed(api),
		Steps: []resource.TestStep{
			{Config: first},
			{PreConfig: func() { api.mu.Lock(); api.unconfirmedUpdateIPs = true; api.mu.Unlock() }, Config: updated, ExpectError: regexp.MustCompile(`(?i)allowlist|allowed_ips`)},
			{PreConfig: func() { api.mu.Lock(); api.unconfirmedUpdateIPs = false; api.mu.Unlock() }, Config: updated, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr("opper_api_key.test", "id", "42"), resource.TestCheckResourceAttr("opper_api_key.test", "secret", "op-test-secret-42"))},
		},
	})
}

func TestAPIKeyAllowedIPsValidation(t *testing.T) {
	requireTerraform(t)
	entries := []string{}
	for i := 1; i <= 101; i++ {
		entries = append(entries, fmt.Sprintf("%q", fmt.Sprintf("8.8.0.%d", i)))
	}
	for i, ips := range []string{`["127.0.0.1"]`, `["10.0.0.0/8"]`, `["8.0.0.0/7"]`, `["not-an-address"]`, `[null]`, "[" + strings.Join(entries, ",") + "]"} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			api := newAPIKeyTestAPI(t)
			resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), Steps: []resource.TestStep{{Config: apiKeyAllowedIPsConfig(api.server.URL, ips, "first"), PlanOnly: true, ExpectError: regexp.MustCompile(`(?i)allowed_ips|IP address`)}}})
			api.mu.Lock()
			defer api.mu.Unlock()
			if api.creates != 0 {
				t.Fatal("invalid allowlist reached key creation")
			}
		})
	}
}
