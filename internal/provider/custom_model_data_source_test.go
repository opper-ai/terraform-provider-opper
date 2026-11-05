package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const customLookupFirstID = "11111111-1111-4111-8111-111111111111"
const customLookupSecondID = "22222222-2222-4222-8222-222222222222"

// Keep this fixture independent of resource registration while the resource is
// implemented in parallel. It exercises the actual Terraform protocol and CLI.
type customLookupProvider struct{ OpperProvider }

func (p *customLookupProvider) DataSources(context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{NewCustomModelDataSource, NewCustomModelsDataSource}
}

func customLookupFactories() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){"opper": providerserver.NewProtocol6WithError(&customLookupProvider{OpperProvider{version: "test"}})}
}

type customLookupAPI struct {
	mu              sync.Mutex
	server          *httptest.Server
	models          []map[string]any
	reads, pages    int
	status          int
	wrongIdentity   bool
	incomplete      bool
	unwanted        []string
	responsePrivate string
}

func newCustomLookupAPI(t *testing.T) *customLookupAPI {
	t.Helper()
	a := &customLookupAPI{responsePrivate: "private-body-credential-canary", models: []map[string]any{
		{"id": customLookupSecondID, "name": "legacy", "identifier": "bedrock/example", "provider": nil, "type": nil, "credential_present": false, "created_at": "2026-11-05T12:00:00Z", "updated_at": "2026-11-05T12:00:00Z"},
		{"id": customLookupFirstID, "name": "support", "identifier": "openai/gpt-test", "provider": "openai", "type": "llm", "credential_present": true, "created_at": "2026-11-05T12:00:00Z", "updated_at": "2026-11-05T12:00:00Z"},
	}}
	a.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer op-mak-local-test" || r.Method != http.MethodGet {
			a.unwanted = append(a.unwanted, r.Method+" "+r.URL.Path)
			w.WriteHeader(403)
			return
		}
		a.reads++
		if a.status != 0 {
			w.WriteHeader(a.status)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": a.responsePrivate})
			return
		}
		if r.URL.Path == "/management/v1/custom-models" {
			offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			if limit <= 0 {
				a.unwanted = append(a.unwanted, "unbounded inventory request")
				w.WriteHeader(400)
				return
			}
			// Short pages prove the client exhausts total_count rather than
			// mistaking the first short page for the whole collection.
			page := []map[string]any{}
			if offset < len(a.models) && !a.incomplete {
				page = append(page, a.models[offset])
			}
			a.pages++
			_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{"offset": offset, "limit": limit, "total_count": len(a.models)}, "data": page})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/management/v1/custom-models/") && r.URL.RawQuery == "" {
			id := strings.TrimPrefix(r.URL.Path, "/management/v1/custom-models/")
			for _, model := range a.models {
				if model["id"] == id {
					if a.wrongIdentity {
						model = a.models[0]
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{}, "data": model})
					return
				}
			}
			w.WriteHeader(404)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": a.responsePrivate})
			return
		}
		a.unwanted = append(a.unwanted, r.Method+" "+r.URL.Path)
		w.WriteHeader(404)
	}))
	t.Cleanup(a.server.Close)
	return a
}

func customLookupConfig(a *customLookupAPI, blocks string) string {
	return fmt.Sprintf("provider \"opper\" {\nmanagement_api_key=\"op-mak-local-test\"\nbase_url=%q\n}\n%s", a.server.URL, blocks)
}

func TestCustomModelDataSourcesLifecycle(t *testing.T) {
	requireTerraform(t)
	a := newCustomLookupAPI(t)
	config := customLookupConfig(a, `data "opper_custom_model" "test" { id="`+customLookupFirstID+`" }
data "opper_custom_models" "all" {}`)
	check := resource.ComposeTestCheckFunc(
		resource.TestCheckResourceAttr("data.opper_custom_model.test", "name", "support"),
		resource.TestCheckResourceAttr("data.opper_custom_model.test", "identifier", "openai/gpt-test"),
		resource.TestCheckResourceAttr("data.opper_custom_model.test", "credential_present", "true"),
		resource.TestCheckResourceAttr("data.opper_custom_model.test", "model_provider", "openai"),
		resource.TestCheckResourceAttr("data.opper_custom_models.all", "id", "custom-models"),
		resource.TestCheckResourceAttr("data.opper_custom_models.all", "models.#", "2"),
		resource.TestCheckResourceAttr("data.opper_custom_models.all", "models.0.id", customLookupFirstID),
		resource.TestCheckResourceAttr("data.opper_custom_models.all", "models.1.id", customLookupSecondID),
		resource.TestCheckNoResourceAttr("data.opper_custom_models.all", "models.1.model_provider"),
		resource.TestCheckNoResourceAttr("data.opper_custom_models.all", "models.1.type"),
		func(s *terraform.State) error {
			a.mu.Lock()
			defer a.mu.Unlock()
			if a.pages < 2 || len(a.unwanted) != 0 {
				return fmt.Errorf("incomplete pagination or unexpected operations: %v", a.unwanted)
			}
			for _, rs := range s.RootModule().Resources {
				for key, value := range rs.Primary.Attributes {
					if key == "credential" || key == "change_token" || key == "extra" || strings.Contains(value, a.responsePrivate) {
						return fmt.Errorf("private model data reached Terraform state")
					}
				}
			}
			return nil
		})
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: customLookupFactories(), PreventPostDestroyRefresh: true, Steps: []resource.TestStep{
		{Config: config, Check: check},
		{Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: check},
		{PreConfig: func() {
			a.mu.Lock()
			defer a.mu.Unlock()
			a.models[1]["name"] = "support-renamed"
			a.models[1]["provider"] = nil
			a.models[1]["credential_present"] = false
		}, Config: config, Check: resource.ComposeTestCheckFunc(
			resource.TestCheckResourceAttr("data.opper_custom_model.test", "name", "support-renamed"),
			resource.TestCheckResourceAttr("data.opper_custom_models.all", "models.0.name", "support-renamed"),
			resource.TestCheckResourceAttr("data.opper_custom_model.test", "credential_present", "false"),
			resource.TestCheckNoResourceAttr("data.opper_custom_model.test", "model_provider"),
		)},
		{Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
	}})
}

func TestCustomModelDataSourcesRefuseMissingForeignAndAPIError(t *testing.T) {
	requireTerraform(t)
	for _, tc := range []struct {
		name, block string
		status      int
		wrongID     bool
	}{
		{"missing_or_foreign", `data "opper_custom_model" "test" { id="33333333-3333-4333-8333-333333333333" }`, 0, false},
		{"wrong_identity", `data "opper_custom_model" "test" { id="` + customLookupFirstID + `" }`, 0, true},
		{"forbidden", `data "opper_custom_models" "test" {}`, 403, false},
		{"server_error", `data "opper_custom_model" "test" { id="` + customLookupFirstID + `" }`, 503, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newCustomLookupAPI(t)
			a.status, a.wrongIdentity = tc.status, tc.wrongID
			resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: customLookupFactories(), PreventPostDestroyRefresh: true, Steps: []resource.TestStep{{Config: customLookupConfig(a, tc.block), ExpectError: regexp.MustCompile(`(?i)unable to read custom.model`)}}})
		})
	}
}

func TestCustomModelDataSourceRejectsInvalidUUIDBeforeHTTP(t *testing.T) {
	requireTerraform(t)
	for _, id := range []string{"", "model-name", "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA", "00000000-0000-0000-0000-000000000000"} {
		t.Run(id, func(t *testing.T) {
			a := newCustomLookupAPI(t)
			resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: customLookupFactories(), PreventPostDestroyRefresh: true, Steps: []resource.TestStep{{Config: customLookupConfig(a, fmt.Sprintf(`data "opper_custom_model" "test" { id=%q }`, id)), ExpectError: regexp.MustCompile(`(?i)UUID`)}}})
			a.mu.Lock()
			defer a.mu.Unlock()
			if a.reads != 0 {
				t.Fatal("invalid selector reached the API")
			}
		})
	}
}

func TestCustomModelsDataSourceEmptyInventory(t *testing.T) {
	requireTerraform(t)
	a := newCustomLookupAPI(t)
	a.models = nil
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: customLookupFactories(), PreventPostDestroyRefresh: true, Steps: []resource.TestStep{{Config: customLookupConfig(a, `data "opper_custom_models" "test" {}`), Check: resource.TestCheckResourceAttr("data.opper_custom_models.test", "models.#", "0")}}})
}

func TestCustomModelsDataSourceRefusesIncompleteInventory(t *testing.T) {
	requireTerraform(t)
	a := newCustomLookupAPI(t)
	a.incomplete = true
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: customLookupFactories(), PreventPostDestroyRefresh: true, Steps: []resource.TestStep{{Config: customLookupConfig(a, `data "opper_custom_models" "test" {}`), ExpectError: regexp.MustCompile(`(?i)unable to read custom.model inventory`)}}})
}
