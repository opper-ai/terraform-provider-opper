package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

func ruleTestConfig(url, kind, scope, config string, enabled bool, name string, version int64) string {
	return fmt.Sprintf(`
provider "opper" {
 management_api_key = "op-mak-local-test"
 base_url = %q
}
resource "opper_rule" "test" {
 kind = %q
 scope = %s
 config_json = %q
 enabled = %t
 name = %q
 schema_version = %d
}
`, url, kind, scope, config, enabled, name, version)
}
func TestRuleResourceLifecycle(t *testing.T) {
	requireTerraform(t)
	api := newRuleTestAPI(t)
	const address = "opper_rule.test"
	raw := `{"evaluators":[{"type":"judge"}],"sampling":{}}`
	initial := ruleTestConfig(api.server.URL, "observe", `{type="org"}`, raw, false, "", 1)
	formatted := ruleTestConfig(api.server.URL, "observe", `{type="org"}`, "{\n \"sampling\": {}, \"evaluators\": [{\"type\":\"judge\"}]\n}", false, "", 1)
	enabled := ruleTestConfig(api.server.URL, "observe", `{type="org"}`, raw, true, "scoring", 1)
	disabled := ruleTestConfig(api.server.URL, "observe", `{type="org"}`, raw, false, "scoring", 1)
	var firstID string
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
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(),
		CheckDestroy: func(_ *terraform.State) error {
			api.mu.Lock()
			defer api.mu.Unlock()
			if len(api.rules) != 0 {
				return fmt.Errorf("%d rules remain", len(api.rules))
			}
			if api.deletes != 1 {
				return fmt.Errorf("expected one final DELETE, got %d", api.deletes)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: initial, Check: resource.ComposeTestCheckFunc(checkWrites(1, 0), resource.TestCheckResourceAttr(address, "name", ""), resource.TestCheckResourceAttr(address, "enabled", "false"), resource.TestCheckResourceAttr(address, "config_json", raw), resource.TestCheckResourceAttrWith(address, "effective_config_json", func(value string) error {
				if !regexp.MustCompile(`"strategy"\s*:\s*"all"`).MatchString(value) {
					return fmt.Errorf("missing canonical sampling default: %s", value)
				}
				return nil
			}), resource.TestCheckResourceAttrWith(address, "id", func(value string) error { firstID = value; return nil }))},
			{Config: initial, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: checkWrites(1, 0)},
			{Config: formatted, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)}}, Check: checkWrites(1, 0)},
			{Config: formatted, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: checkWrites(1, 0)},
			{Config: enabled, Check: resource.ComposeTestCheckFunc(checkWrites(1, 1), resource.TestCheckResourceAttr(address, "enabled", "true"), func(_ *terraform.State) error {
				api.mu.Lock()
				defer api.mu.Unlock()
				patch := api.patches[len(api.patches)-1]
				if len(patch) != 2 || patch["enabled"] == nil || patch["name"] == nil {
					return fmt.Errorf("metadata update sent extra fields")
				}
				return nil
			})},
			{Config: disabled, Check: resource.ComposeTestCheckFunc(checkWrites(1, 2), resource.TestCheckResourceAttr(address, "enabled", "false"))},
			{ResourceName: address, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"config_json"}},
			{PreConfig: func() {
				api.drift(firstID, true, `{"evaluators":[{"type":"judge"}],"sampling":{"strategy":"rate","rate":10}}`)
			}, Config: disabled, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)}}, Check: resource.ComposeTestCheckFunc(checkWrites(1, 3), resource.TestCheckResourceAttr(address, "enabled", "false"), resource.TestCheckResourceAttr(address, "config_json", raw))},
			{PreConfig: func() { api.externalDelete(firstID) }, Config: disabled, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionCreate)}}, Check: resource.ComposeTestCheckFunc(checkWrites(2, 3), resource.TestCheckResourceAttrWith(address, "id", func(value string) error {
				if value == firstID {
					return fmt.Errorf("deleted rule was not recreated")
				}
				return nil
			}))},
		},
	})
}
func TestRuleResourceScopeReplacement(t *testing.T) {
	requireTerraform(t)
	api := newRuleTestAPI(t)
	raw := `{"type":"budget","monthly_limit_cents":10000}`
	const projectA = "11111111-1111-4111-8111-000000000001"
	const projectB = "11111111-1111-4111-8111-000000000002"
	initial := ruleTestConfig(api.server.URL, "comply", fmt.Sprintf(`{type="projects",project_uuids=[%q,%q]}`, projectA, projectB), raw, false, "budget", 1)
	reordered := ruleTestConfig(api.server.URL, "comply", fmt.Sprintf(`{type="projects",project_uuids=[%q,%q]}`, projectB, projectA), raw, false, "budget", 1)
	replacement := ruleTestConfig(api.server.URL, "comply", `{type="roles",role_names=["developer"]}`, raw, false, "budget", 1)
	var firstID string
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), Steps: []resource.TestStep{
		{Config: initial, Check: resource.TestCheckResourceAttrWith("opper_rule.test", "id", func(id string) error { firstID = id; return nil })},
		{Config: reordered, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
		{Config: replacement, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("opper_rule.test", plancheck.ResourceActionDestroyBeforeCreate)}}, Check: resource.TestCheckResourceAttrWith("opper_rule.test", "id", func(id string) error {
			if id == firstID {
				return fmt.Errorf("scope change did not replace rule UUID")
			}
			return nil
		})},
	}, CheckDestroy: func(_ *terraform.State) error {
		api.mu.Lock()
		defer api.mu.Unlock()
		if len(api.rules) != 0 || api.creates != 2 || api.deletes != 2 {
			return fmt.Errorf("scope replacement did not clean up")
		}
		return nil
	}})
}
func TestRuleResourcePreservesStateOnErrors(t *testing.T) {
	requireTerraform(t)
	api := newRuleTestAPI(t)
	cfg := ruleTestConfig(api.server.URL, "comply", `{type="org"}`, `{"type":"retention","retention_days":7}`, false, "retention", 1)
	var id string
	check := func(s *terraform.State) error {
		got := s.RootModule().Resources["opper_rule.test"].Primary.ID
		if id == "" {
			id = got
		} else if id != got {
			return fmt.Errorf("API failure changed rule identity")
		}
		api.mu.Lock()
		defer api.mu.Unlock()
		if api.creates != 1 {
			return fmt.Errorf("API failure recreated rule")
		}
		return nil
	}
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), Steps: []resource.TestStep{
		{Config: cfg, Check: check},
		{PreConfig: func() { api.mu.Lock(); api.listFailure = 404; api.mu.Unlock() }, Config: cfg, ExpectError: regexp.MustCompile(`404`)},
		{PreConfig: func() { api.mu.Lock(); api.listFailure = 403; api.mu.Unlock() }, Config: cfg, ExpectError: regexp.MustCompile(`403`)},
		{PreConfig: func() { api.mu.Lock(); api.listFailure = 0; api.mu.Unlock() }, Config: cfg, Check: check},
		{PreConfig: func() { api.mu.Lock(); api.deleteFailure = 403; api.mu.Unlock() }, Config: cfg, Destroy: true, ExpectError: regexp.MustCompile(`403`)},
		{PreConfig: func() { api.mu.Lock(); api.deleteFailure = 0; api.mu.Unlock() }, Config: cfg, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: check},
	}, CheckDestroy: func(_ *terraform.State) error {
		api.mu.Lock()
		defer api.mu.Unlock()
		if len(api.rules) != 0 {
			return fmt.Errorf("rule remains")
		}
		return nil
	}})
}
func TestRuleResourceValidation(t *testing.T) {
	requireTerraform(t)
	api := newRuleTestAPI(t)
	for _, tc := range []struct{ name, scope, config, kind, pattern string }{
		{"empty target", `{type="projects"}`, `{}`, "comply", "Missing scope targets"},
		{"wrong target", `{type="org",role_names=["developer"]}`, `{}`, "comply", "Unexpected scope targets"},
		{"member policy", `{type="roles",role_names=["developer"]}`, `{"type":"retention","retention_days":7}`, "comply", "Unsupported member scope"},
		{"nonobject", `{type="org"}`, `[]`, "route", "Invalid rule configuration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), Steps: []resource.TestStep{{Config: ruleTestConfig(api.server.URL, tc.kind, tc.scope, tc.config, false, "", 1), ExpectError: regexp.MustCompile(tc.pattern)}}})
		})
	}
}
func TestRuleResourceGuardCanonicalDefaults(t *testing.T) {
	requireTerraform(t)
	api := newRuleTestAPI(t)
	raw := `{"engine":"regex","patterns":[{"pattern":"test"}]}`
	cfg := ruleTestConfig(api.server.URL, "guard", `{type="org"}`, raw, false, "guard", 1)
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), Steps: []resource.TestStep{
		{Config: cfg, Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr("opper_rule.test", "config_json", raw), resource.TestCheckResourceAttrWith("opper_rule.test", "effective_config_json", func(value string) error {
			if !regexp.MustCompile(`"handle"`).MatchString(value) {
				return fmt.Errorf("missing generated handle")
			}
			return nil
		}))},
		{Config: cfg, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
	}})
}
func TestAccRuleResource(t *testing.T) {
	if os.Getenv("TF_ACC") != "1" {
		t.Skip("set TF_ACC=1 for live Opper acceptance tests")
	}
	if os.Getenv("OPPER_TEST_DISPOSABLE_ORG") != "1" {
		t.Skip("set OPPER_TEST_DISPOSABLE_ORG=1 only for an isolated disposable organization")
	}
	if os.Getenv("OPPER_MANAGEMENT_API_KEY") == "" {
		t.Fatal("OPPER_MANAGEMENT_API_KEY is required")
	}
	requireTerraform(t)
	origin := os.Getenv("OPPER_BASE_URL")
	if origin == "" {
		origin = "https://api.opper.ai"
	}
	api, err := client.New(origin, os.Getenv("OPPER_MANAGEMENT_API_KEY"), "acceptance")
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("terraform-rule-acc-%d", time.Now().UnixNano())
	config := func(enabled bool, retentionDays int) string {
		return fmt.Sprintf(`
provider "opper" {}
resource "opper_project" "test" {
 name = %q
}
resource "opper_rule" "test" {
 kind = "comply"
 name = %q
 enabled = %t
 scope = {
 type = "projects"
 project_uuids = [opper_project.test.id]
 }
 config_json = jsonencode({type="retention",retention_days=%d})
}
`, name+"-project", name, enabled, retentionDays)
	}
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), Steps: []resource.TestStep{
		{Config: config(false, 7), Check: resource.TestCheckResourceAttr("opper_rule.test", "enabled", "false")},
		{Config: config(false, 7), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
		{Config: config(true, 7), Check: resource.TestCheckResourceAttr("opper_rule.test", "enabled", "true")},
		{Config: config(false, 7), Check: resource.TestCheckResourceAttr("opper_rule.test", "enabled", "false")},
		{Config: config(false, 3), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("opper_rule.test", plancheck.ResourceActionUpdate)}}, Check: func(s *terraform.State) error {
			id := s.RootModule().Resources["opper_rule.test"].Primary.ID
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			rule, err := api.GetRule(ctx, id)
			if err != nil {
				return err
			}
			return compareRuleJSONForTest(string(rule.Config), `{"type":"retention","retention_days":3}`)
		}},
		{Config: config(false, 3), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
		{ResourceName: "opper_rule.test", ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"config_json"}, ImportStateCheck: checkImportedRuleConfig(`{"type":"retention","retention_days":3}`)},
	}, CheckDestroy: func(s *terraform.State) error {
		for _, rs := range s.RootModule().Resources {
			if rs.Primary == nil || (rs.Type != "opper_rule" && rs.Type != "opper_project") {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			var readErr error
			switch rs.Type {
			case "opper_rule":
				_, readErr = api.GetRule(ctx, rs.Primary.ID)
			case "opper_project":
				_, readErr = api.GetProject(ctx, rs.Primary.ID)
			}
			cancel()
			if readErr == nil {
				return fmt.Errorf("%s %s still exists", rs.Type, rs.Primary.ID)
			}
			if rs.Type == "opper_rule" && !errors.Is(readErr, client.ErrRuleNotFound) {
				return readErr
			}
			if rs.Type == "opper_project" && !client.IsNotFound(readErr) {
				return readErr
			}
		}
		return nil
	}})
}

func TestRuleResourceReconcilesUncertainCreate(t *testing.T) {
	requireTerraform(t)
	api := newRuleTestAPI(t)
	api.createFailureAfterWrite = true
	cfg := ruleTestConfig(api.server.URL, "comply", `{type="org"}`, `{"type":"retention","retention_days":7}`, false, "uncertain create", 1)
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), Steps: []resource.TestStep{
		{Config: cfg},
		{PreConfig: func() { api.mu.Lock(); api.createFailureAfterWrite = false; api.mu.Unlock() }, Config: cfg, Check: func(s *terraform.State) error {
			api.mu.Lock()
			defer api.mu.Unlock()
			id := s.RootModule().Resources["opper_rule.test"].Primary.ID
			if _, ok := api.rules[id]; !ok || api.creates != 1 {
				return fmt.Errorf("uncertain creation was not recovered by its original UUID")
			}
			return nil
		}},
		{Config: cfg, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
	}, CheckDestroy: func(_ *terraform.State) error {
		api.mu.Lock()
		defer api.mu.Unlock()
		if len(api.rules) != 0 {
			return fmt.Errorf("recovered rule remains")
		}
		return nil
	}})
}

func TestRuleResourceKindReplacementAndSchemaVersion(t *testing.T) {
	requireTerraform(t)
	api := newRuleTestAPI(t)
	initial := ruleTestConfig(api.server.URL, "comply", `{type="org"}`, `{"type":"retention","retention_days":7}`, false, "policy", 1)
	versioned := ruleTestConfig(api.server.URL, "comply", `{type="org"}`, `{"type":"retention","retention_days":7}`, false, "policy", 2)
	route := ruleTestConfig(api.server.URL, "route", `{type="org"}`, `{"type":"cache","ttl_seconds":300}`, false, "policy", 2)
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), Steps: []resource.TestStep{
		{Config: initial},
		{Config: versioned, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("opper_rule.test", plancheck.ResourceActionUpdate)}}, Check: func(_ *terraform.State) error {
			api.mu.Lock()
			defer api.mu.Unlock()
			patch := api.patches[len(api.patches)-1]
			if len(patch) != 1 || patch["schema_version"] == nil {
				return fmt.Errorf("version-only update included unrelated fields")
			}
			return nil
		}},
		{Config: route, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("opper_rule.test", plancheck.ResourceActionDestroyBeforeCreate)}}, Check: resource.TestCheckResourceAttr("opper_rule.test", "kind", "route")},
	}, CheckDestroy: func(_ *terraform.State) error {
		api.mu.Lock()
		defer api.mu.Unlock()
		if len(api.rules) != 0 || api.creates != 2 || api.deletes != 2 {
			return fmt.Errorf("kind replacement did not remove both rules")
		}
		return nil
	}})
}

func TestRuleResourceAdditionalTargetSets(t *testing.T) {
	requireTerraform(t)
	for _, tc := range []struct{ name, kind, scope, config string }{
		{"functions", "route", `{type="functions",function_uuids=["11111111-1111-4111-8111-000000000001"]}`, `{"default_model":null}`},
		{"members", "comply", `{type="members",member_uuids=["11111111-1111-4111-8111-000000000001"]}`, `{"type":"budget","monthly_limit_cents":10000}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := newRuleTestAPI(t)
			cfg := ruleTestConfig(api.server.URL, tc.kind, tc.scope, tc.config, false, "targeted", 1)
			resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), Steps: []resource.TestStep{
				{Config: cfg, Check: resource.TestCheckResourceAttr("opper_rule.test", "scope.type", tc.name)},
				{Config: cfg, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
			}})
		})
	}
}

func TestRuleResourceUppercaseScopeUUIDRejected(t *testing.T) {
	requireTerraform(t)
	api := newRuleTestAPI(t)
	config := ruleTestConfig(api.server.URL, "comply", `{type="projects",project_uuids=["AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA"]}`, `{"type":"retention","retention_days":7}`, false, "noncanonical scope", 1)
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), Steps: []resource.TestStep{{Config: config, ExpectError: regexp.MustCompile(`must be a canonical lowercase UUID`)}}})
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.creates != 0 {
		t.Fatal("noncanonical scope reached the API")
	}
}

func TestRuleResourceConcurrentConfigChangeDuringMetadataUpdate(t *testing.T) {
	requireTerraform(t)
	api := newRuleTestAPI(t)
	raw := `{"evaluators":[{"type":"judge"}],"sampling":{}}`
	initial := ruleTestConfig(api.server.URL, "observe", `{type="org"}`, raw, false, "concurrent", 1)
	enabled := ruleTestConfig(api.server.URL, "observe", `{type="org"}`, raw, true, "concurrent", 1)
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), Steps: []resource.TestStep{
		{Config: initial},
		{PreConfig: func() {
			api.mu.Lock()
			api.concurrentConfigOnPatch = `{"evaluators":[{"type":"judge"}],"sampling":{"strategy":"rate","rate":10}}`
			api.mu.Unlock()
		}, Config: enabled, ExpectError: regexp.MustCompile(`Rule configuration changed during update`)},
		{Config: enabled, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("opper_rule.test", plancheck.ResourceActionUpdate)}}, Check: func(_ *terraform.State) error {
			api.mu.Lock()
			defer api.mu.Unlock()
			if api.creates != 1 || api.updates != 2 {
				return fmt.Errorf("concurrent policy was not restored")
			}
			patch := api.patches[len(api.patches)-1]
			if len(patch) != 1 || patch["config"] == nil {
				return fmt.Errorf("reconciliation did not update the drifted configuration")
			}
			return nil
		}},
		{Config: enabled, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
	}})
}

func TestRuleResourceUnresolvedCreateKeepsIdentityForCleanup(t *testing.T) {
	requireTerraform(t)
	api := newRuleTestAPI(t)
	api.createFailureAfterWrite = true
	api.listFailure = 403
	cfg := ruleTestConfig(api.server.URL, "comply", `{type="org"}`, `{"type":"retention","retention_days":7}`, false, "unresolved create", 1)
	var originalID string
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), Steps: []resource.TestStep{
		{Config: cfg, ExpectError: regexp.MustCompile(`tainted`)},
		{PreConfig: func() {
			api.mu.Lock()
			defer api.mu.Unlock()
			for id := range api.rules {
				originalID = id
			}
			api.createFailureAfterWrite = false
			api.listFailure = 0
		}, Config: cfg,
			ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("opper_rule.test", plancheck.ResourceActionDestroyBeforeCreate)}},
			Check: func(_ *terraform.State) error {
				api.mu.Lock()
				defer api.mu.Unlock()
				if _, ok := api.rules[originalID]; ok || api.creates != 2 || api.deletes != 1 {
					return fmt.Errorf("tainted create identity was lost or leaked")
				}
				return nil
			}},
	}, CheckDestroy: func(_ *terraform.State) error {
		api.mu.Lock()
		defer api.mu.Unlock()
		if len(api.rules) != 0 || api.deletes != 2 {
			return fmt.Errorf("tainted/replacement rules remain")
		}
		return nil
	}})
}

// ImportStateVerify compares strings byte-for-byte. Rule configs are JSON
// objects, whose field order and whitespace do not change the imported policy.
func checkImportedRuleConfig(expected string) resource.ImportStateCheckFunc {
	return func(states []*terraform.InstanceState) error {
		if len(states) != 1 {
			return fmt.Errorf("expected one imported rule")
		}
		return compareRuleJSONForTest(states[0].Attributes["config_json"], expected)
	}
}
func compareRuleJSONForTest(actual, expected string) error {
	var got, want map[string]any
	if json.Unmarshal([]byte(actual), &got) != nil || got == nil {
		return fmt.Errorf("imported or read rule config is not a JSON object")
	}
	if json.Unmarshal([]byte(expected), &want) != nil || want == nil {
		return fmt.Errorf("expected rule config is not a JSON object")
	}
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("imported or read rule configuration differs from the desired policy")
	}
	return nil
}
func TestImportedRuleConfigSemanticComparison(t *testing.T) {
	expected := `{"retention_days":3,"type":"retention"}`
	for _, tc := range []struct {
		name, actual string
		valid        bool
	}{
		{"reordered", `{"type":"retention","retention_days":3}`, true},
		{"formatted", "{\n  \"type\": \"retention\", \"retention_days\":3\n}", true},
		{"policy differs", `{"type":"retention","retention_days":7}`, false},
		{"extra policy field", `{"type":"retention","retention_days":3,"extra":true}`, false},
		{"invalid", "not-json", false},
		{"null", "null", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkImportedRuleConfig(expected)([]*terraform.InstanceState{{Attributes: map[string]string{"config_json": tc.actual}}})
			if (err == nil) != tc.valid {
				t.Fatalf("semantic import verification mismatch: %v", err)
			}
		})
	}
}
