package provider

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

func TestCanonicalRuleConfigPreservesPolicyValues(t *testing.T) {
	raw := json.RawMessage(`{ "z": ["second", "first", null, true], "nested": {"z": 1.2300, "a": "<policy>&\u2028\u2029"}, "limit": 9007199254740993, "a": 9007199254740992 }`)
	want := `{"a":9007199254740992,"limit":9007199254740993,"nested":{"a":"\u003cpolicy\u003e\u0026\u2028\u2029","z":1.2300},"z":["second","first",null,true]}`
	got := canonicalRuleConfig(raw)
	if got.ValueString() != want {
		t.Fatalf("normalization changed JSON values: got %s, want %s", got.ValueString(), want)
	}
	if canonicalRuleConfig(json.RawMessage(want)).ValueString() != want {
		t.Fatal("canonicalization is not stable")
	}
}

func TestRuleRefreshPreservesExistingDesiredJSON(t *testing.T) {
	desired := jsontypes.NewNormalizedValue("{\n  \"type\": \"retention\", \"retention_days\": 7\n}")
	m := ruleModel{ConfigJSON: desired}
	rule := client.Rule{Config: json.RawMessage(`{"type":"retention","retention_days":7}`), Scope: client.RuleScope{Type: "org"}}
	m.setRule(rule, false)
	if !m.ConfigJSON.Equal(desired) || m.EffectiveConfigJSON.ValueString() != string(rule.Config) {
		t.Fatal("refresh changed existing desired JSON or rewrote the effective API snapshot")
	}
}

func TestRuleDeclarativeImportWithJSONEncodeNeedsNoUpdate(t *testing.T) {
	api := newRuleTestAPI(t)
	const id = "11111111-1111-4111-8111-000000000009"
	api.rules[id] = client.Rule{
		ID: id, Kind: "comply", Scope: client.RuleScope{Type: "org"}, SchemaVersion: 1,
		Config:    json.RawMessage(`{"type":"retention","retention_days":7}`),
		CreatedAt: "2026-11-03T10:00:00Z", UpdatedAt: "2026-11-03T10:00:00Z",
	}
	config := func(days int) string {
		return fmt.Sprintf(`
provider "opper" {
  management_api_key = "op-mak-local-test"
  base_url = %q
}
resource "opper_rule" "test" {
  kind = "comply"
  scope = { type = "org" }
  config_json = jsonencode({type="retention",retention_days=%d})
}
import {
  to = opper_rule.test
  id = %q
}
`, api.server.URL, days, id)
	}
	h := newCustomModelCLI(t, config(7))
	h.run("", "", "plan", "-input=false", "-no-color", "-out=import.tfplan")
	var plan struct {
		Resources []struct {
			Address string `json:"address"`
			Change  struct {
				Actions   []string `json:"actions"`
				Importing struct {
					ID string `json:"id"`
				} `json:"importing"`
			} `json:"change"`
		} `json:"resource_changes"`
	}
	if err := json.Unmarshal([]byte(h.run("", "", "show", "-json", "import.tfplan")), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Resources) != 1 || plan.Resources[0].Address != "opper_rule.test" ||
		fmt.Sprint(plan.Resources[0].Change.Actions) != "[no-op]" || plan.Resources[0].Change.Importing.ID != id {
		t.Fatalf("expected one unchanged import, got %+v", plan.Resources)
	}
	h.run("", "", "apply", "-input=false", "-no-color", "import.tfplan")
	h.run("", "", "plan", "-input=false", "-no-color", "-detailed-exitcode")
	api.mu.Lock()
	for _, request := range api.requests {
		if !strings.HasPrefix(request, "GET ") {
			t.Errorf("import attempted a mutation: %s", request)
		}
	}
	if api.creates != 0 || api.updates != 0 || api.deletes != 0 {
		t.Errorf("import mutated the API: creates=%d updates=%d deletes=%d", api.creates, api.updates, api.deletes)
	}
	api.mu.Unlock()

	// Normalizing imports must not suppress a real desired policy change.
	h.write(config(8))
	h.run("", "", "apply", "-input=false", "-no-color", "-auto-approve")
	h.run("", "", "plan", "-input=false", "-no-color", "-detailed-exitcode")
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.creates != 0 || api.updates != 1 || api.deletes != 0 {
		t.Fatalf("real policy update was lost: creates=%d updates=%d deletes=%d", api.creates, api.updates, api.deletes)
	}
}
