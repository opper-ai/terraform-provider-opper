package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

type deploymentReceipt struct {
	version    *client.DynamicRouteVersion
	undeployed *int64
	payload    string
}

type deploymentTestAPI struct {
	*dynamicRouteTestAPI
	versions                                      map[int64]client.DynamicRouteVersion
	receipts                                      map[string]deploymentReceipt
	deployPOSTs, undeployPOSTs, receiptGETs       int
	dropDeploy, dropUndeploy, receiptsUnavailable bool
	foreignBeforeDeploy                           bool
	undeployedNumbers                             []int64
}

func newDeploymentTestAPI(t *testing.T) *deploymentTestAPI {
	t.Helper()
	base := newDynamicRouteTestAPI(t)
	oldURL, _ := url.Parse(base.server.URL)
	proxy := httputil.NewSingleHostReverseProxy(oldURL)
	api := &deploymentTestAPI{dynamicRouteTestAPI: base, versions: map[int64]client.DynamicRouteVersion{}, receipts: map[string]deploymentReceipt{}}
	base.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/management/v1/dynamic-routes/terraform-draft"
		part := strings.TrimPrefix(r.URL.Path, prefix)
		if !strings.HasPrefix(r.URL.Path, prefix) || (part != "/deployments" && part != "/undeploy" && !strings.HasPrefix(part, "/deployments/") && !strings.HasPrefix(part, "/versions/")) {
			proxy.ServeHTTP(w, r)
			return
		}
		api.mu.Lock()
		defer api.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer op-mak-local-test" {
			w.WriteHeader(401)
			return
		}
		d, ok := api.routes["terraform-draft"]
		if !ok {
			w.WriteHeader(404)
			return
		}
		respond := func(receipt deploymentReceipt, replayed bool) {
			meta := map[string]any{"idempotency_replayed": replayed}
			if receipt.undeployed != nil {
				meta["undeployed_version_number"] = *receipt.undeployed
				meta["undeployed_version"] = receipt.version
			} else {
				meta["deployed_version"] = receipt.version
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": api.routes["terraform-draft"], "meta": meta})
		}
		if strings.HasPrefix(part, "/versions/") {
			n, _ := strconv.ParseInt(strings.TrimPrefix(part, "/versions/"), 10, 64)
			v, ok := api.versions[n]
			if !ok {
				w.WriteHeader(404)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": v})
			return
		}
		if strings.HasPrefix(part, "/deployments/") {
			api.receiptGETs++
			if api.receiptsUnavailable {
				w.WriteHeader(503)
				return
			}
			receipt, ok := api.receipts["deploy/"+strings.TrimPrefix(part, "/deployments/")]
			if !ok {
				w.WriteHeader(404)
				return
			}
			respond(receipt, true)
			return
		}
		var body struct {
			RouteUUID     string `json:"expected_route_uuid"`
			DraftRevision int64  `json:"expected_draft_revision"`
			ActiveNumber  int64  `json:"expected_active_version_number"`
			Key           string `json:"idempotency_key"`
			Note          string `json:"note"`
		}
		if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&body) != nil || body.Key == "" {
			w.WriteHeader(400)
			return
		}
		kind := "deploy"
		if part == "/undeploy" {
			kind = "undeploy"
			api.undeployPOSTs++
		} else {
			api.deployPOSTs++
		}
		encoded, _ := json.Marshal(body)
		if prior, exists := api.receipts[kind+"/"+body.Key]; exists {
			if prior.payload != string(encoded) {
				w.WriteHeader(409)
				return
			}
			respond(prior, true)
			return
		}
		if kind == "deploy" && api.foreignBeforeDeploy {
			api.foreignBeforeDeploy = false
			api.addVersionLocked("external writer")
			d = api.routes["terraform-draft"]
		}
		active := int64(0)
		if d.ActiveVersion != nil {
			active = d.ActiveVersion.VersionNumber
		}
		if body.RouteUUID != d.Route.UUID || body.ActiveNumber != active || (kind == "deploy" && body.DraftRevision != d.Draft.Revision) {
			w.WriteHeader(409)
			return
		}
		receipt := deploymentReceipt{payload: string(encoded)}
		if kind == "deploy" {
			v := api.addVersionLocked(body.Note)
			receipt.version = &v
		} else {
			if active > 0 {
				v := api.versions[active]
				receipt.version = &v
			}
			receipt.undeployed = &active
			d.ActiveVersion = nil
			api.routes["terraform-draft"] = d
			api.undeployedNumbers = append(api.undeployedNumbers, active)
		}
		api.receipts[kind+"/"+body.Key] = receipt
		if kind == "deploy" && api.dropDeploy {
			api.dropDeploy = false
			w.WriteHeader(502)
			return
		}
		if kind == "undeploy" && api.dropUndeploy {
			api.dropUndeploy = false
			w.WriteHeader(502)
			return
		}
		respond(receipt, false)
	}))
	t.Cleanup(base.server.Close)
	return api
}

func (api *deploymentTestAPI) addVersionLocked(note string) client.DynamicRouteVersion {
	d := api.routes["terraform-draft"]
	n := int64(len(api.versions) + 1)
	v := client.DynamicRouteVersion{UUID: fmt.Sprintf("33333333-3333-4333-8333-%012d", n), VersionNumber: n, RouteName: d.Route.Name, Description: d.Route.Description, Graph: append(json.RawMessage(nil), d.Draft.Graph...), Note: note, DraftRevision: d.Draft.Revision, CreatedAt: "2026-11-03T09:00:00Z"}
	api.versions[n] = v
	d.ActiveVersion = &struct {
		UUID          string `json:"uuid"`
		VersionNumber int64  `json:"version_number"`
	}{UUID: v.UUID, VersionNumber: n}
	api.routes["terraform-draft"] = d
	return v
}

func (api *deploymentTestAPI) seedRoute(t *testing.T) {
	t.Helper()
	c, _ := client.New(api.server.URL, "op-mak-local-test", "test")
	if _, err := c.CreateDynamicRoute(context.Background(), "terraform-draft", "", "", json.RawMessage(dynamicRouteDraftGraph)); err != nil {
		t.Fatal(err)
	}
}

func deploymentTestConfig(baseURL, description, note string, deployed, includeDraft bool) string {
	config := fmt.Sprintf(`provider "opper" {
management_api_key = "op-mak-local-test"
base_url = %q
}
`, baseURL)
	name, uuid, revision := `"terraform-draft"`, `"22222222-2222-4222-8222-222222222222"`, `1`
	if includeDraft {
		config += fmt.Sprintf(`resource "opper_dynamic_route" "test" {
name = "terraform-draft"
graph_json = %q
description = %q
}
`, dynamicRouteDraftGraph, description)
		name, uuid, revision = "opper_dynamic_route.test.name", "opper_dynamic_route.test.uuid", "opper_dynamic_route.test.draft_revision"
	}
	return config + fmt.Sprintf(`resource "opper_dynamic_route_deployment" "test" {
route_name = %s
route_uuid = %s
draft_revision = %s
note = %q
deployed = %t
}
`, name, uuid, revision, note, deployed)
}

func TestDynamicRouteDeploymentLifecycle(t *testing.T) {
	requireTerraform(t)
	api := newDeploymentTestAPI(t)
	const address = "opper_dynamic_route_deployment.test"
	first := deploymentTestConfig(api.server.URL, "first", "release", true, true)
	second := deploymentTestConfig(api.server.URL, "second", "release", true, true)
	off := deploymentTestConfig(api.server.URL, "second", "release", false, true)
	check := func(version string, deployed bool, postCount int) resource.TestCheckFunc {
		return resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr(address, "version_number", version), resource.TestCheckResourceAttr(address, "deployed", strconv.FormatBool(deployed)), resource.TestCheckResourceAttr(address, "operation_pending", "false"), func(_ *terraform.State) error {
			api.mu.Lock()
			defer api.mu.Unlock()
			if api.deployPOSTs != postCount {
				return fmt.Errorf("wanted %d deployment POSTs, got %d", postCount, api.deployPOSTs)
			}
			return nil
		})
	}
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, Steps: []resource.TestStep{
		{Config: first, Check: check("1", true, 1)},
		{Config: first, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: check("1", true, 1)},
		{Config: second, Check: check("2", true, 2)},
		{Config: off, Check: resource.ComposeTestCheckFunc(check("2", false, 2), resource.TestCheckResourceAttr(address, "observed_active_version_number", "0"))},
		{Config: second, Check: check("3", true, 3)},
		{ResourceName: address, ImportState: true, ImportStateId: "terraform-draft/3", ImportStateVerify: true, ImportStateVerifyIgnore: []string{"operation_id"}},
		{PreConfig: func() {
			api.mu.Lock()
			defer api.mu.Unlock()
			api.addVersionLocked("external same-revision deployment")
		}, Config: second, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(address, plancheck.ResourceActionUpdate)}}, Check: check("5", true, 4)},
	}, CheckDestroy: func(_ *terraform.State) error {
		api.mu.Lock()
		defer api.mu.Unlock()
		if len(api.routes) != 0 {
			return fmt.Errorf("route was not deleted after deployment cleanup")
		}
		if len(api.undeployedNumbers) != 2 || api.undeployedNumbers[0] != 2 || api.undeployedNumbers[1] != 5 {
			return fmt.Errorf("unexpected undeployed versions: %v", api.undeployedNumbers)
		}
		return nil
	}})
}

func TestDynamicRouteDeploymentLostResponses(t *testing.T) {
	requireTerraform(t)
	api := newDeploymentTestAPI(t)
	api.dropDeploy = true
	first := deploymentTestConfig(api.server.URL, "first", "release", true, true)
	second := deploymentTestConfig(api.server.URL, "second", "release", true, true)
	off := deploymentTestConfig(api.server.URL, "second", "release", false, true)
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, Steps: []resource.TestStep{
		{Config: first, Check: resource.TestCheckResourceAttr("opper_dynamic_route_deployment.test", "version_number", "1")},
		{PreConfig: func() { api.mu.Lock(); api.dropDeploy = true; api.mu.Unlock() }, Config: second, Check: resource.TestCheckResourceAttr("opper_dynamic_route_deployment.test", "version_number", "2")},
		{PreConfig: func() { api.mu.Lock(); api.dropUndeploy = true; api.mu.Unlock() }, Config: off, Check: resource.TestCheckResourceAttr("opper_dynamic_route_deployment.test", "deployed", "false")},
		{Config: off, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: func(_ *terraform.State) error {
			api.mu.Lock()
			defer api.mu.Unlock()
			if api.deployPOSTs != 2 || api.undeployPOSTs != 1 || api.receiptGETs != 2 {
				return fmt.Errorf("lost response caused duplicate mutations: deploy=%d undeploy=%d receipts=%d", api.deployPOSTs, api.undeployPOSTs, api.receiptGETs)
			}
			return nil
		}},
	}})
}

func TestDynamicRouteDeploymentRejectsUnsafeOwnership(t *testing.T) {
	requireTerraform(t)
	api := newDeploymentTestAPI(t)
	api.seedRoute(t)
	api.mu.Lock()
	foreign := api.addVersionLocked("existing writer")
	api.mu.Unlock()
	config := deploymentTestConfig(api.server.URL, "", "", true, false)
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), Steps: []resource.TestStep{{Config: config, ExpectError: regexp.MustCompile(`409`)}}})
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.routes["terraform-draft"].ActiveVersion.UUID != foreign.UUID || api.undeployPOSTs != 0 || len(api.versions) != 1 {
		t.Fatal("initial create adopted or stopped another writer's deployment")
	}
}

func TestDynamicRouteDeploymentRefusesForeignDestroy(t *testing.T) {
	requireTerraform(t)
	api := newDeploymentTestAPI(t)
	config := deploymentTestConfig(api.server.URL, "", "", true, true)
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, Steps: []resource.TestStep{
		{Config: config},
		{PreConfig: func() { api.mu.Lock(); api.addVersionLocked("external writer"); api.mu.Unlock() }, Config: config, Destroy: true, ExpectError: regexp.MustCompile(`Another writer`)},
		{Config: config, Check: resource.TestCheckResourceAttr("opper_dynamic_route_deployment.test", "version_number", "3")},
	}, CheckDestroy: func(_ *terraform.State) error {
		api.mu.Lock()
		defer api.mu.Unlock()
		for _, n := range api.undeployedNumbers {
			if n == 2 {
				return fmt.Errorf("provider stopped foreign deployment")
			}
		}
		return nil
	}})
}

func TestDynamicRouteDeploymentPendingCreateRequiresImport(t *testing.T) {
	requireTerraform(t)
	api := newDeploymentTestAPI(t)
	api.seedRoute(t)
	api.dropDeploy, api.receiptsUnavailable = true, true
	// This recovery must exercise the operator's actual state-rm/import
	// sequence: importing directly onto the retained tainted address fails.
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	dir := t.TempDir()
	binary := filepath.Join(dir, "bin", "terraform-provider-opper")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build provider: %v\n%s", err, out)
	}
	cliConfig := filepath.Join(dir, "development.tfrc")
	if err := os.WriteFile(cliConfig, []byte(fmt.Sprintf(`provider_installation {
 dev_overrides { "registry.terraform.io/opper-ai/opper" = %q }
 direct {}
}
`, filepath.Dir(binary))), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`terraform {
 required_providers {
  opper = { source = "opper-ai/opper" }
 }
}
`+deploymentTestConfig(api.server.URL, "", "", true, false)), 0600); err != nil {
		t.Fatal(err)
	}
	env := []string{"TF_CLI_CONFIG_FILE=" + cliConfig, "CHECKPOINT_DISABLE=1", "TF_IN_AUTOMATION=1"}
	for _, key := range []string{"PATH", "HOME", "GOPATH", "GOCACHE"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	run := func(wantError string, args ...string) string {
		t.Helper()
		cmd := exec.Command(os.Getenv("TF_ACC_TERRAFORM_PATH"), args...)
		cmd.Dir, cmd.Env = dir, env
		out, err := cmd.CombinedOutput()
		if wantError != "" {
			if err == nil || !strings.Contains(string(out), wantError) {
				t.Fatalf("terraform %v: wanted %q, got %v\n%s", args, wantError, err, out)
			}
		} else if err != nil {
			t.Fatalf("terraform %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("502", "apply", "-auto-approve", "-input=false", "-no-color")
	api.mu.Lock()
	api.receiptsUnavailable = false
	api.mu.Unlock()
	run("Recovered deployment needs explicit import", "plan", "-input=false", "-no-color")
	run("", "state", "rm", "opper_dynamic_route_deployment.test")
	run("", "import", "-input=false", "-no-color", "opper_dynamic_route_deployment.test", "terraform-draft/1")
	run("", "plan", "-input=false", "-no-color", "-detailed-exitcode")
	api.mu.Lock()
	if api.deployPOSTs != 1 || len(api.versions) != 1 {
		t.Fatalf("pending create retried deployment instead of importing: %d POSTs/%d versions", api.deployPOSTs, len(api.versions))
	}
	api.mu.Unlock()
	run("", "destroy", "-auto-approve", "-input=false", "-no-color")
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.routes["terraform-draft"].ActiveVersion != nil {
		t.Fatal("recovered deployment was not undeployed")
	}
}

func TestAccDynamicRouteDeploymentResource(t *testing.T) {
	if os.Getenv("TF_ACC") != "1" || os.Getenv("OPPER_TEST_DISPOSABLE_ORG") != "1" {
		t.Skip("live deployment tests require TF_ACC=1 and OPPER_TEST_DISPOSABLE_ORG=1")
	}
	model := strings.TrimSpace(os.Getenv("OPPER_TEST_MODEL_ID"))
	if model == "" {
		t.Skip("set OPPER_TEST_MODEL_ID to an exact current model permitted by the disposable organization; the conditional deployment backend must be deployed first")
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
	name := fmt.Sprintf("terraform-acc-live-%d", time.Now().UnixNano())
	graph := fmt.Sprintf(`{"elements":[{"id":"START","type":"start","outputs":{"next":{"elementId":"answer"}}},{"id":"answer","type":"model","properties":{"model":%q},"outputs":{"success":{"elementId":"END"},"fallback":{"elementId":"END"}}},{"id":"END","type":"end","outputs":{}}]}`, model)
	config := func(description string, deployed bool) string {
		return fmt.Sprintf(`provider "opper" {}
resource "opper_dynamic_route" "test" {
 name = %q
 description = %q
 graph_json = %q
}
resource "opper_dynamic_route_deployment" "test" {
 route_name = opper_dynamic_route.test.name
 route_uuid = opper_dynamic_route.test.uuid
 draft_revision = opper_dynamic_route.test.draft_revision
 deployed = %t
}
`, name, description, graph, deployed)
	}
	first, second, off := config("Initial disposable route", true), config("Updated disposable route", true), config("Updated disposable route", false)
	const address = "opper_dynamic_route_deployment.test"
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, Steps: []resource.TestStep{
		{Config: first, Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr(address, "version_number", "1"), resource.TestCheckResourceAttr(address, "deployed", "true"))},
		{Config: first, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
		{Config: second, Check: resource.TestCheckResourceAttr(address, "version_number", "2")},
		{Config: off, Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr(address, "deployed", "false"), resource.TestCheckResourceAttr(address, "observed_active_version_number", "0"))},
		{Config: second, Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr(address, "version_number", "3"), resource.TestCheckResourceAttr(address, "deployed", "true"))},
		{ResourceName: address, ImportState: true, ImportStateId: name + "/3", ImportStateVerify: true, ImportStateVerifyIgnore: []string{"operation_id"}},
	}, CheckDestroy: func(_ *terraform.State) error {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := api.GetDynamicRoute(ctx, name)
		if !client.IsNotFound(err) {
			return fmt.Errorf("task-owned live route was not deleted or could not be verified: %v", err)
		}
		return nil
	}})
}
