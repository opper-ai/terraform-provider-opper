package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

const dynamicRouteDraftGraph = `{"elements":[{"id":"START","type":"start","outputs":{"next":{"elementId":"END"}}},{"id":"END","type":"end"}]}`
const dynamicRouteChangedGraph = `{"elements":[{"id":"START","type":"start","properties":{"label":"draft change"},"outputs":{"next":{"elementId":"END"}}},{"id":"END","type":"end"}]}`

type dynamicRouteTestAPI struct {
	mu                               sync.Mutex
	routes                           map[string]client.DynamicRouteDetail
	creates, drafts, labels, deletes int
	getFailure, draftFailure         int
	lastDraftFields                  map[string]bool
	concurrentLabelGraph             json.RawMessage
	concurrentLabelDescription       *string
	server                           *httptest.Server
}

func newDynamicRouteTestAPI(t *testing.T) *dynamicRouteTestAPI {
	t.Helper()
	api := &dynamicRouteTestAPI{routes: make(map[string]client.DynamicRouteDetail)}
	api.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.mu.Lock()
		defer api.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer op-mak-local-test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		const prefix = "/management/v1/dynamic-routes"
		name := strings.TrimPrefix(r.URL.Path, prefix+"/")
		name = strings.TrimSuffix(name, "/draft")
		write := func(status int, d client.DynamicRouteDetail) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"data": d, "meta": map[string]any{}})
		}
		if strings.HasSuffix(r.URL.Path, "/deploy") {
			t.Error("draft resource attempted to deploy a route")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		switch r.Method {
		case http.MethodPost:
			var body struct {
				Name, DisplayName, Description string
				Graph                          json.RawMessage
				Deploy                         bool
			}
			// display_name needs its wire name; the rest match case-insensitively.
			var fields map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
				t.Error(err)
			}
			encoded, _ := json.Marshal(fields)
			_ = json.Unmarshal(encoded, &body)
			_ = json.Unmarshal(fields["display_name"], &body.DisplayName)
			if body.Deploy || string(fields["deploy"]) != "false" {
				t.Error("create must explicitly use deploy:false")
			}
			if _, ok := api.routes[body.Name]; ok {
				w.WriteHeader(http.StatusConflict)
				return
			}
			d := client.DynamicRouteDetail{}
			d.Route.Name, d.Route.UUID = body.Name, "22222222-2222-4222-8222-222222222222"
			d.Route.Description, d.Route.DisplayName = body.Description, body.DisplayName
			d.Route.InvocationModel, d.Route.CreatedAt = "dynamic/"+body.Name, "2026-11-03T09:00:00Z"
			d.Draft.Graph, d.Draft.Revision = normalizeDynamicRouteTestGraph(body.Graph), 1
			api.routes[body.Name] = d
			api.creates++
			write(http.StatusCreated, d)
		case http.MethodGet:
			if api.getFailure != 0 {
				w.WriteHeader(api.getFailure)
				return
			}
			d, ok := api.routes[name]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			write(http.StatusOK, d)
		case http.MethodPatch, http.MethodPut:
			d, ok := api.routes[name]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			var fields map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
				t.Error(err)
			}
			if _, ok := fields["layout"]; ok {
				t.Error("draft resource overwrote editor layout")
			}
			if r.Method == http.MethodPatch {
				if len(fields) != 1 {
					t.Errorf("metadata fields: %v", fields)
				}
				_ = json.Unmarshal(fields["display_name"], &d.Route.DisplayName)
				api.labels++
				// Simulate a UI draft edit after refresh but before this response.
				if api.concurrentLabelGraph != nil {
					d.Draft.Graph = normalizeDynamicRouteTestGraph(api.concurrentLabelGraph)
					d.Draft.Revision++
				}
				if api.concurrentLabelDescription != nil {
					d.Route.Description = *api.concurrentLabelDescription
				}
			} else {
				if api.draftFailure != 0 {
					w.WriteHeader(api.draftFailure)
					return
				}
				api.lastDraftFields = make(map[string]bool)
				for k := range fields {
					api.lastDraftFields[k] = true
				}
				if description, ok := fields["description"]; ok {
					_ = json.Unmarshal(description, &d.Route.Description)
				}
				if graph, ok := fields["graph"]; ok {
					d.Draft.Graph = normalizeDynamicRouteTestGraph(graph)
				}
				d.Draft.Revision++
				api.drafts++
			}
			api.routes[name] = d
			write(http.StatusOK, d)
		case http.MethodDelete:
			if _, ok := api.routes[name]; !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			delete(api.routes, name)
			api.deletes++
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(api.server.Close)
	return api
}

func normalizeDynamicRouteTestGraph(raw json.RawMessage) json.RawMessage {
	var graph map[string]any
	_ = json.Unmarshal(raw, &graph)
	for _, item := range graph["elements"].([]any) {
		e := item.(map[string]any)
		if e["outputs"] == nil || e["type"] == "end" {
			e["outputs"] = map[string]any{}
		}
	}
	encoded, _ := json.Marshal(graph)
	return encoded
}

func dynamicRouteTestConfig(baseURL, graph, displayName, description string) string {
	return fmt.Sprintf(`
provider "opper" {
  management_api_key = "op-mak-local-test"
  base_url = %q
}
resource "opper_dynamic_route" "test" {
  name = "terraform-draft"
  display_name = %q
  description = %q
  graph_json = %q
}
`, baseURL, displayName, description, graph)
}

func TestDynamicRouteResourceLifecycle(t *testing.T) {
	requireTerraform(t)
	api := newDynamicRouteTestAPI(t)
	const addr = "opper_dynamic_route.test"
	checkWrites := func(creates, drafts, labels int) resource.TestCheckFunc {
		return func(_ *terraform.State) error {
			api.mu.Lock()
			defer api.mu.Unlock()
			if api.creates != creates || api.drafts != drafts || api.labels != labels {
				return fmt.Errorf("wanted %d create/%d draft/%d label, got %d/%d/%d", creates, drafts, labels, api.creates, api.drafts, api.labels)
			}
			return nil
		}
	}
	initial := dynamicRouteTestConfig(api.server.URL, dynamicRouteDraftGraph, "", "initial")
	updated := dynamicRouteTestConfig(api.server.URL, dynamicRouteDraftGraph, "Human label", "updated")
	labelOnly := dynamicRouteTestConfig(api.server.URL, dynamicRouteDraftGraph, "Other label", "updated")
	changed := dynamicRouteTestConfig(api.server.URL, dynamicRouteChangedGraph, "Other label", "")
	var pretty bytes.Buffer
	_ = json.Indent(&pretty, []byte(dynamicRouteDraftGraph), "", "  ")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true,
		CheckDestroy: func(_ *terraform.State) error {
			api.mu.Lock()
			defer api.mu.Unlock()
			if len(api.routes) != 0 || api.deletes != 1 {
				return fmt.Errorf("draft route not deleted: %d routes/%d deletes", len(api.routes), api.deletes)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: initial, Check: resource.ComposeTestCheckFunc(checkWrites(1, 0, 0), resource.TestCheckResourceAttr(addr, "id", "terraform-draft"), resource.TestCheckResourceAttr(addr, "graph_json", dynamicRouteDraftGraph), resource.TestCheckResourceAttr(addr, "draft_revision", "1"), resource.TestCheckNoResourceAttr(addr, "active_version_number"))},
			{Config: initial, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: checkWrites(1, 0, 0)},
			{Config: dynamicRouteTestConfig(api.server.URL, pretty.String(), "", "initial"), Check: checkWrites(1, 0, 0)},
			{Config: dynamicRouteTestConfig(api.server.URL, pretty.String(), "", "initial"), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: checkWrites(1, 0, 0)},
			{Config: updated, Check: resource.ComposeTestCheckFunc(checkWrites(1, 1, 1), resource.TestCheckResourceAttr(addr, "description", "updated"), func(_ *terraform.State) error {
				api.mu.Lock()
				defer api.mu.Unlock()
				if len(api.lastDraftFields) != 1 || !api.lastDraftFields["description"] {
					return fmt.Errorf("description edit sent unexpected fields: %v", api.lastDraftFields)
				}
				return nil
			})},
			{Config: labelOnly, Check: resource.ComposeTestCheckFunc(checkWrites(1, 1, 2), resource.TestCheckResourceAttr(addr, "draft_revision", "2"))},
			{Config: changed, Check: resource.ComposeTestCheckFunc(checkWrites(1, 2, 2), resource.TestCheckResourceAttr(addr, "description", ""))},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"graph_json"}},
			{PreConfig: func() {
				api.mu.Lock()
				defer api.mu.Unlock()
				d := api.routes["terraform-draft"]
				d.Draft.Graph = normalizeDynamicRouteTestGraph(json.RawMessage(dynamicRouteDraftGraph))
				d.Draft.Revision++
				api.routes["terraform-draft"] = d
			}, Config: changed, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate)}}, Check: checkWrites(1, 3, 2)},
			{PreConfig: func() {
				api.mu.Lock()
				defer api.mu.Unlock()
				d := api.routes["terraform-draft"]
				d.ActiveVersion = &struct {
					VersionNumber int64 `json:"version_number"`
				}{VersionNumber: 7}
				api.routes["terraform-draft"] = d
			}, Config: changed, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: resource.ComposeTestCheckFunc(checkWrites(1, 3, 2), resource.TestCheckResourceAttr(addr, "active_version_number", "7"))},
			{PreConfig: func() { api.mu.Lock(); delete(api.routes, "terraform-draft"); api.mu.Unlock() }, Config: changed, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionCreate)}}, Check: checkWrites(2, 3, 2)},
		},
	})
}

func TestDynamicRouteResourceRecoversPartialUpdate(t *testing.T) {
	requireTerraform(t)
	api := newDynamicRouteTestAPI(t)
	initial := dynamicRouteTestConfig(api.server.URL, dynamicRouteDraftGraph, "", "initial")
	updated := dynamicRouteTestConfig(api.server.URL, dynamicRouteDraftGraph, "Updated label", "updated")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true,
		Steps: []resource.TestStep{
			{Config: initial},
			{PreConfig: func() { api.mu.Lock(); api.draftFailure = 500; api.mu.Unlock() }, Config: updated, ExpectError: regexp.MustCompile(`500`)},
			{PreConfig: func() { api.mu.Lock(); api.draftFailure = 0; api.mu.Unlock() }, Config: updated, Check: func(_ *terraform.State) error {
				api.mu.Lock()
				defer api.mu.Unlock()
				if api.creates != 1 || api.labels != 1 || api.drafts != 1 {
					return fmt.Errorf("partial update repeated completed work: creates=%d labels=%d drafts=%d", api.creates, api.labels, api.drafts)
				}
				return nil
			}},
		},
	})
}

func TestDynamicRouteResourceRejectsIgnoredGraphFields(t *testing.T) {
	requireTerraform(t)
	api := newDynamicRouteTestAPI(t)
	graph := strings.Replace(dynamicRouteDraftGraph, `"type":"start"`, `"type":"start","typo":"not stored"`, 1)
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), Steps: []resource.TestStep{{Config: dynamicRouteTestConfig(api.server.URL, graph, "", ""), ExpectError: regexp.MustCompile(`Invalid dynamic route graph`)}}})
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.creates != 0 {
		t.Fatal("invalid graph reached the API")
	}
}

func TestDynamicRouteResourceConcurrentDraftDuringLabelUpdate(t *testing.T) {
	testDynamicRouteConcurrentDraft(t, "initial")
}

func TestDynamicRouteResourceConcurrentDraftDuringPlannedDraftUpdate(t *testing.T) {
	testDynamicRouteConcurrentDraft(t, "planned description")
}

func testDynamicRouteConcurrentDraft(t *testing.T, plannedDescription string) {
	t.Helper()
	requireTerraform(t)
	api := newDynamicRouteTestAPI(t)
	initial := dynamicRouteTestConfig(api.server.URL, dynamicRouteDraftGraph, "", "initial")
	labelOnly := dynamicRouteTestConfig(api.server.URL, dynamicRouteDraftGraph, "New label", plannedDescription)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true,
		Steps: []resource.TestStep{
			{Config: initial},
			{PreConfig: func() {
				api.mu.Lock()
				defer api.mu.Unlock()
				api.concurrentLabelGraph = json.RawMessage(dynamicRouteChangedGraph)
				value := "concurrent UI description"
				api.concurrentLabelDescription = &value
			}, Config: labelOnly, ExpectError: regexp.MustCompile(`(?i)concurrent.*draft|draft.*changed`)},
			{PreConfig: func() {
				api.mu.Lock()
				defer api.mu.Unlock()
				d := api.routes["terraform-draft"]
				if api.drafts != 0 || api.labels != 1 || d.Route.DisplayName != "New label" || d.Route.Description != "concurrent UI description" || !dynamicRouteJSONEqual(string(d.Draft.Graph), string(normalizeDynamicRouteTestGraph(json.RawMessage(dynamicRouteChangedGraph)))) {
					t.Fatal("label-only apply overwrote concurrent draft content or lost completed metadata")
				}
				api.concurrentLabelGraph = nil
				api.concurrentLabelDescription = nil
			}, Config: labelOnly,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("opper_dynamic_route.test", plancheck.ResourceActionUpdate)}},
				Check: func(_ *terraform.State) error {
					api.mu.Lock()
					defer api.mu.Unlock()
					if api.drafts != 1 || api.labels != 1 {
						return fmt.Errorf("reconciled plan did not preserve completed label: drafts=%d labels=%d", api.drafts, api.labels)
					}
					return nil
				}},
		},
	})
}

func TestAccDynamicRouteResource(t *testing.T) {
	if os.Getenv("TF_ACC") != "1" || os.Getenv("OPPER_TEST_DISPOSABLE_ORG") != "1" {
		t.Skip("live tests require TF_ACC=1 and OPPER_TEST_DISPOSABLE_ORG=1 for a disposable organization")
	}
	key := os.Getenv("OPPER_MANAGEMENT_API_KEY")
	if strings.TrimSpace(key) == "" {
		t.Fatal("OPPER_MANAGEMENT_API_KEY is required")
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
	name := fmt.Sprintf("terraform-acc-draft-%d", time.Now().UnixNano())
	config := fmt.Sprintf(`provider "opper" {}
resource "opper_dynamic_route" "test" {
  name = %q
  graph_json = %q
}
`, name, dynamicRouteDraftGraph)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true,
		CheckDestroy: func(_ *terraform.State) error {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, err := api.GetDynamicRoute(ctx, name)
			if !client.IsNotFound(err) {
				return fmt.Errorf("test draft still exists or could not be verified: %v", err)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: config, Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr("opper_dynamic_route.test", "id", name), resource.TestCheckNoResourceAttr("opper_dynamic_route.test", "active_version_number"))},
			{Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
			{ResourceName: "opper_dynamic_route.test", ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"graph_json"}},
		},
	})
}
