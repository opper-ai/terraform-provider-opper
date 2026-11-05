package provider

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	helperresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const customModelAddress = "opper_custom_model.test"

func customModelConfig(baseURL, name, creationKey string, version int64, credential bool) string {
	key, versionLine, secret := "", "", ""
	if creationKey != "" {
		key = fmt.Sprintf("creation_idempotency_key = %q", creationKey)
	}
	if version >= 0 {
		versionLine = fmt.Sprintf("credential_version = %d", version)
	}
	if credential {
		secret = "credential_wo = var.credential"
	}
	return fmt.Sprintf(`
variable "credential" {
 type = string
 sensitive = true
 ephemeral = true
}
provider "opper" {
 management_api_key = "op-mak-local-test"
 base_url = %q
}
resource "opper_custom_model" "test" {
 name = %q
 identifier = "openai/gpt-test"
 %s
 %s
 %s
}
`, baseURL, name, key, versionLine, secret)
}

func checkCustomModelWrites(api *customModelTestAPI, creates, updates, rotations, patches int) helperresource.TestCheckFunc {
	return func(*terraform.State) error {
		api.mu.Lock()
		defer api.mu.Unlock()
		if api.creates != creates || api.updates != updates || api.rotations != rotations || api.patchAttempts != patches {
			return fmt.Errorf("custom model writes: wanted create/update/rotation/PATCH %d/%d/%d/%d, got %d/%d/%d/%d", creates, updates, rotations, patches, api.creates, api.updates, api.rotations, api.patchAttempts)
		}
		return nil
	}
}

func TestCustomModelResourceWriteOnlySchema(t *testing.T) {
	var response frameworkresource.SchemaResponse
	NewCustomModelResource().Schema(context.Background(), frameworkresource.SchemaRequest{}, &response)
	secret, ok := response.Schema.Attributes["credential_wo"].(schema.StringAttribute)
	if !ok || !secret.WriteOnly || !secret.Sensitive || !secret.Optional || secret.Computed {
		t.Fatal("provider credential must be optional, sensitive and write-only")
	}
	version, ok := response.Schema.Attributes["credential_version"].(schema.Int64Attribute)
	if !ok || !version.Optional || !version.Computed || version.Default != nil {
		t.Fatal("credential version must preserve omission without a static reset default")
	}
	if _, forbidden := response.Schema.Attributes["credential"]; forbidden {
		t.Fatal("a state-persisted credential alternative must not exist")
	}
}

func TestCustomModelResourceLifecycle(t *testing.T) {
	requireTerraform(t)
	t.Setenv("TF_VAR_credential", "custom-model-first-secret-canary")
	api := newCustomModelTestAPI(t)
	first := customModelConfig(api.server.URL, "terraform-model", "create-fixture", 1, true)
	renamed := customModelConfig(api.server.URL, "terraform-model-renamed", "create-fixture", 1, false)
	rotated := customModelConfig(api.server.URL, "terraform-model-renamed", "create-fixture", 2, true)
	helperresource.UnitTest(t, helperresource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true,
		CheckDestroy: func(*terraform.State) error {
			api.mu.Lock()
			defer api.mu.Unlock()
			if len(api.models) != 0 || api.deletes != 1 {
				return fmt.Errorf("custom model fixture not removed once: %d models, %d deletes", len(api.models), api.deletes)
			}
			return nil
		},
		Steps: []helperresource.TestStep{
			{Config: first, Check: helperresource.ComposeTestCheckFunc(helperresource.TestCheckResourceAttr(customModelAddress, "credential_version", "1"), helperresource.TestCheckResourceAttr(customModelAddress, "credential_present", "true"), helperresource.TestCheckResourceAttr(customModelAddress, "model_provider", "openai"), helperresource.TestCheckResourceAttr(customModelAddress, "type", "llm"), checkCustomModelWrites(api, 1, 0, 0, 0))},
			{Config: first, ConfigPlanChecks: helperresource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: checkCustomModelWrites(api, 1, 0, 0, 0)},
			{Config: renamed, Check: helperresource.ComposeTestCheckFunc(helperresource.TestCheckResourceAttr(customModelAddress, "name", "terraform-model-renamed"), checkCustomModelWrites(api, 1, 1, 0, 1))},
			{PreConfig: func() { t.Setenv("TF_VAR_credential", "custom-model-second-secret-canary") }, Config: rotated, Check: helperresource.ComposeTestCheckFunc(helperresource.TestCheckResourceAttr(customModelAddress, "credential_version", "2"), checkCustomModelWrites(api, 1, 2, 1, 2))},
			{PreConfig: func() { t.Setenv("TF_VAR_credential", "ignored-unchanged-version-canary") }, Config: rotated, ConfigPlanChecks: helperresource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: checkCustomModelWrites(api, 1, 2, 1, 2)},
			{ResourceName: customModelAddress, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"creation_idempotency_key", "credential_version", "operation_id", "operation_kind"}},
		},
	})
}

// This harness runs the installed Terraform CLI against a built provider and
// keeps artifacts so tests can inspect the actual saved plan and private state.
type customModelCLI struct {
	t   *testing.T
	dir string
	env []string
}

func newCustomModelCLI(t *testing.T, config string) *customModelCLI {
	t.Helper()
	requireTerraform(t)
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
	if err := os.WriteFile(cliConfig, []byte(fmt.Sprintf("provider_installation {\n dev_overrides { \"registry.terraform.io/opper-ai/opper\" = %q }\n direct {}\n}\n", filepath.Dir(binary))), 0600); err != nil {
		t.Fatal(err)
	}
	h := &customModelCLI{t: t, dir: dir, env: []string{"TF_CLI_CONFIG_FILE=" + cliConfig, "CHECKPOINT_DISABLE=1", "TF_IN_AUTOMATION=1"}}
	for _, key := range []string{"PATH", "HOME", "GOPATH", "GOCACHE", "GOMODCACHE"} {
		if value := os.Getenv(key); value != "" {
			h.env = append(h.env, key+"="+value)
		}
	}
	h.write(config)
	return h
}

func (h *customModelCLI) write(config string) {
	h.t.Helper()
	const source = "terraform {\n required_version = \">= 1.11.0\"\n required_providers { opper = { source = \"opper-ai/opper\" } }\n}\n"
	if err := os.WriteFile(filepath.Join(h.dir, "main.tf"), []byte(source+config), 0600); err != nil {
		h.t.Fatal(err)
	}
}

func (h *customModelCLI) run(credential, wantError string, args ...string) string {
	h.t.Helper()
	cmd := exec.Command(os.Getenv("TF_ACC_TERRAFORM_PATH"), args...)
	cmd.Dir, cmd.Env = h.dir, append(append([]string{}, h.env...), "TF_VAR_credential="+credential)
	out, err := cmd.CombinedOutput()
	if wantError != "" {
		if err == nil || !regexp.MustCompile(wantError).Match(out) {
			h.t.Fatalf("terraform %v: expected %q, got %v\n%s", args, wantError, err, out)
		}
	} else if err != nil {
		h.t.Fatalf("terraform %v: %v\n%s", args, err, out)
	}
	if credential != "" && strings.Contains(string(out), credential) {
		h.t.Fatal("Terraform output exposed write-only credential")
	}
	return string(out)
}

func (h *customModelCLI) attributes() map[string]any {
	h.t.Helper()
	raw, err := os.ReadFile(filepath.Join(h.dir, "terraform.tfstate"))
	if err != nil {
		h.t.Fatal(err)
	}
	var state struct {
		Resources []struct {
			Type      string `json:"type"`
			Instances []struct {
				Attributes map[string]any `json:"attributes"`
			} `json:"instances"`
		} `json:"resources"`
	}
	if json.Unmarshal(raw, &state) != nil {
		h.t.Fatal("invalid Terraform state")
	}
	for _, resource := range state.Resources {
		if resource.Type == "opper_custom_model" && len(resource.Instances) == 1 {
			return resource.Instances[0].Attributes
		}
	}
	h.t.Fatal("custom model missing from Terraform state")
	return nil
}

func assertCustomModelSecretAbsent(t *testing.T, raw []byte, secret string, depth int) {
	t.Helper()
	digest := sha256.Sum256([]byte(secret))
	if bytes.Contains(raw, []byte(secret)) || bytes.Contains(raw, []byte(fmt.Sprintf("%x", digest))) {
		t.Fatal("Terraform plan/state/private data retained credential material")
	}
	if depth >= 6 {
		return
	}
	// Framework private state uses base64 JSON wrappers. Inspect their decoded
	// contents too; searching only the visible state would miss a leaked value.
	for _, token := range regexp.MustCompile(`[A-Za-z0-9+/_=-]{12,}`).FindAll(raw, -1) {
		for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			decoded, err := encoding.DecodeString(string(token))
			if err == nil && !bytes.Equal(decoded, raw) {
				assertCustomModelSecretAbsent(t, decoded, secret, depth+1)
			}
		}
	}
}

func (h *customModelCLI) assertArtifacts(names []string, secrets ...string) {
	h.t.Helper()
	for _, name := range names {
		path := filepath.Join(h.dir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			h.t.Fatal(err)
		}
		for _, secret := range secrets {
			assertCustomModelSecretAbsent(h.t, raw, secret, 0)
		}
		if strings.HasSuffix(name, ".tfplan") {
			archive, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
			if err != nil {
				h.t.Fatal(err)
			}
			for _, member := range archive.File {
				reader, err := member.Open()
				if err != nil {
					h.t.Fatal(err)
				}
				data, err := io.ReadAll(reader)
				_ = reader.Close()
				if err != nil {
					h.t.Fatal(err)
				}
				for _, secret := range secrets {
					assertCustomModelSecretAbsent(h.t, data, secret, 0)
				}
			}
		}
	}
}

func TestCustomModelResourceCredentialAbsentFromArtifacts(t *testing.T) {
	api := newCustomModelTestAPI(t)
	config := customModelConfig(api.server.URL, "artifact-model", "artifact-create", 1, true)
	h := newCustomModelCLI(t, config)
	first, second, ignored := "artifact-first-secret-canary", "artifact-rotation-secret-canary", "artifact-ignored-secret-canary"
	h.run(first, "", "plan", "-input=false", "-no-color", "-out=first.tfplan")
	h.assertArtifacts([]string{"first.tfplan"}, first)
	h.run(first, "", "apply", "-input=false", "-no-color", "first.tfplan")
	h.assertArtifacts([]string{"first.tfplan", "terraform.tfstate"}, first)
	h.write(customModelConfig(api.server.URL, "artifact-model", "artifact-create", 2, true))
	h.run(second, "", "plan", "-input=false", "-no-color", "-out=rotation.tfplan")
	h.assertArtifacts([]string{"rotation.tfplan", "terraform.tfstate"}, first, second)
	h.run(second, "", "apply", "-input=false", "-no-color", "rotation.tfplan")
	h.run(ignored, "", "plan", "-input=false", "-no-color", "-detailed-exitcode")
	h.assertArtifacts([]string{"rotation.tfplan", "terraform.tfstate", "terraform.tfstate.backup"}, first, second, ignored)
	if err := checkCustomModelWrites(api, 1, 1, 1, 1)(nil); err != nil {
		t.Fatal(err)
	}
	h.run(ignored, "", "destroy", "-auto-approve", "-input=false", "-no-color")
}

func TestCustomModelResourceStaleSavedPlansRefuseMutation(t *testing.T) {
	api := newCustomModelTestAPI(t)
	h := newCustomModelCLI(t, customModelConfig(api.server.URL, "saved-plan-model", "saved-plan-create", 1, true))
	h.run("saved-plan-create-secret", "", "apply", "-auto-approve", "-input=false", "-no-color")
	id := h.attributes()["id"].(string)
	h.write(customModelConfig(api.server.URL, "saved-plan-renamed", "saved-plan-create", 1, false))
	h.run("unused", "", "plan", "-input=false", "-no-color", "-out=update.tfplan")
	api.externalEdit(id, "", "saved-plan-external-credential")
	h.run("unused", `(?i)409|changed|conflict`, "apply", "-input=false", "-no-color", "update.tfplan")
	api.mu.Lock()
	if api.updates != 0 || api.patchAttempts != 1 || api.models[id].Credential != "saved-plan-external-credential" {
		t.Fatal("saved plan overwrote a newer credential or retried PATCH")
	}
	api.mu.Unlock()
	h.run("unused", `(?i)changed|import`, "plan", "-input=false", "-no-color")
	h.run("unused", "", "state", "rm", customModelAddress)
	h.write(customModelConfig(api.server.URL, "saved-plan-model", "", -1, false))
	h.run("unused", "", "import", "-input=false", "-no-color", customModelAddress, id)
	h.run("unused", "", "plan", "-destroy", "-input=false", "-no-color", "-out=destroy.tfplan")
	api.externalEdit(id, "", "saved-destroy-external-credential")
	h.run("unused", `409`, "apply", "-input=false", "-no-color", "destroy.tfplan")
	api.mu.Lock()
	if api.deletes != 0 || api.deleteAttempts != 1 {
		t.Fatal("saved destroy refreshed its token or retried deletion")
	}
	api.mu.Unlock()
	h.run("unused", "", "state", "rm", customModelAddress)
	h.run("unused", "", "import", "-input=false", "-no-color", customModelAddress, id)
	h.run("unused", "", "destroy", "-auto-approve", "-input=false", "-no-color")
}

func TestCustomModelResourcePendingCreateRequiresImport(t *testing.T) {
	api := newCustomModelTestAPI(t)
	api.dropCreate, api.blockAfterDrop = true, true
	h := newCustomModelCLI(t, customModelConfig(api.server.URL, "pending-create-model", "pending-create-key", 1, true))
	h.run("pending-create-secret", `(?i)creation.*recovery`, "apply", "-auto-approve", "-input=false", "-no-color")
	attrs := h.attributes()
	if attrs["operation_pending"] != true || attrs["credential_version"] != float64(0) || !strings.HasPrefix(attrs["id"].(string), "pending-create:") {
		t.Fatal("uncertain creation did not retain a guarded pending marker")
	}
	api.mu.Lock()
	api.creationReceiptFail = 0
	id := api.creationReceipts["pending-create-key"].UUID
	api.mu.Unlock()
	h.run("unused", `(?i)recovery|import`, "plan", "-input=false", "-no-color")
	api.mu.Lock()
	if api.postAttempts != 1 || api.creates != 1 || api.deletes != 0 {
		t.Fatal("pending create retried or destroyed its recovered definition")
	}
	api.mu.Unlock()
	h.run("unused", "", "state", "rm", customModelAddress)
	h.write(customModelConfig(api.server.URL, "pending-create-model", "", -1, false))
	h.run("unused", "", "import", "-input=false", "-no-color", customModelAddress, id)
	h.run("unused", "", "plan", "-input=false", "-no-color", "-detailed-exitcode")
	h.run("unused", "", "destroy", "-auto-approve", "-input=false", "-no-color")
}

func TestCustomModelResourceUncertainRotationRecoversWithoutCredential(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(fmt.Sprint(unknown), func(t *testing.T) {
			api := newCustomModelTestAPI(t)
			h := newCustomModelCLI(t, customModelConfig(api.server.URL, "pending-rotation-model", "pending-rotation-key", 1, true))
			h.run("first-rotation-secret", "", "apply", "-auto-approve", "-input=false", "-no-color")
			h.write(customModelConfig(api.server.URL, "pending-rotation-model", "pending-rotation-key", 2, true))
			api.mu.Lock()
			api.dropUpdate, api.blockAfterDrop, api.unknownMatch = !unknown, !unknown, unknown
			api.mu.Unlock()
			h.run("second-rotation-secret", `(?i)recovery|review`, "apply", "-auto-approve", "-input=false", "-no-color")
			attrs := h.attributes()
			if attrs["operation_pending"] != true || attrs["credential_version"] != float64(1) {
				t.Fatal("unresolved rotation advanced its acknowledged credential version")
			}
			h.run("unused", `(?i)recovery|review`, "plan", "-input=false", "-no-color")
			api.mu.Lock()
			api.updateReceiptFail, api.unknownMatch = 0, false
			api.mu.Unlock()
			h.write(customModelConfig(api.server.URL, "pending-rotation-model", "pending-rotation-key", 2, false))
			h.run("unused", "", "apply", "-auto-approve", "-input=false", "-no-color")
			attrs = h.attributes()
			if attrs["operation_pending"] != false || attrs["credential_version"] != float64(2) {
				t.Fatal("pure recovery did not acknowledge the original rotation")
			}
			if err := checkCustomModelWrites(api, 1, 1, 1, 1)(nil); err != nil {
				t.Fatal(err)
			}
			h.run("unused", "", "destroy", "-auto-approve", "-input=false", "-no-color")
		})
	}
}

func TestCustomModelResourceRecoversCommitWithPriorStateSnapshot(t *testing.T) {
	api := newCustomModelTestAPI(t)
	h := newCustomModelCLI(t, customModelConfig(api.server.URL, "hard-crash-model", "hard-crash-key", 1, true))
	h.run("hard-crash-first-secret", "", "apply", "-auto-approve", "-input=false", "-no-color")
	oldState, err := os.ReadFile(filepath.Join(h.dir, "terraform.tfstate"))
	if err != nil {
		t.Fatal(err)
	}
	h.write(customModelConfig(api.server.URL, "hard-crash-renamed", "hard-crash-key", 2, true))
	h.run("hard-crash-second-secret", "", "apply", "-auto-approve", "-input=false", "-no-color")
	id := h.attributes()["id"].(string)
	// Restore the pre-write local state, modeling a process loss after the API
	// commit but before Terraform persisted its new public/private state.
	if err := os.WriteFile(filepath.Join(h.dir, "terraform.tfstate"), oldState, 0600); err != nil {
		t.Fatal(err)
	}
	h.write(customModelConfig(api.server.URL, "hard-crash-renamed", "hard-crash-key", 2, false))
	h.run("ignored-new-ephemeral-value", "", "apply", "-auto-approve", "-input=false", "-no-color")
	if attrs := h.attributes(); attrs["credential_version"] != float64(2) || attrs["operation_pending"] != false {
		t.Fatal("receipt recovery did not repair the pre-commit local state snapshot")
	}
	if err := checkCustomModelWrites(api, 1, 1, 1, 1)(nil); err != nil {
		t.Fatal(err)
	}
	api.mu.Lock()
	if api.models[id].Credential != "hard-crash-second-secret" {
		t.Fatal("recovery resent a changed ephemeral credential")
	}
	api.mu.Unlock()
	if err := os.WriteFile(filepath.Join(h.dir, "terraform.tfstate"), oldState, 0600); err != nil {
		t.Fatal(err)
	}
	api.externalEdit(id, "", "later-third-party-rotation")
	h.run("ignored-new-ephemeral-value", `(?i)changed|import`, "apply", "-auto-approve", "-input=false", "-no-color")
	if err := checkCustomModelWrites(api, 1, 1, 1, 1)(nil); err != nil {
		t.Fatal(err)
	}
	h.run("unused", "", "state", "rm", customModelAddress)
	h.write(customModelConfig(api.server.URL, "hard-crash-renamed", "", -1, false))
	h.run("unused", "", "import", "-input=false", "-no-color", customModelAddress, id)
	h.run("unused", "", "destroy", "-auto-approve", "-input=false", "-no-color")
}

func TestCustomModelResourceReplacementNeedsFreshCreationKey(t *testing.T) {
	api := newCustomModelTestAPI(t)
	h := newCustomModelCLI(t, customModelConfig(api.server.URL, "replacement-model", "first-replacement-key", 1, true))
	h.run("replacement-secret", "", "apply", "-auto-approve", "-input=false", "-no-color")
	h.run("replacement-secret", `(?i)creation.*completed|fresh.*key`, "plan", "-replace="+customModelAddress, "-input=false", "-no-color")
	api.mu.Lock()
	if api.creates != 1 || api.deletes != 0 {
		t.Fatal("an invalid replacement deleted the existing model")
	}
	api.mu.Unlock()
	h.write(customModelConfig(api.server.URL, "replacement-model", "fresh-replacement-key", 1, true))
	h.run("replacement-secret", "", "apply", "-auto-approve", "-input=false", "-no-color")
	api.mu.Lock()
	if api.creates != 2 || api.deletes != 1 {
		t.Fatal("a fresh creation key did not permit reviewed replacement")
	}
	api.mu.Unlock()
	h.run("replacement-secret", "", "destroy", "-auto-approve", "-input=false", "-no-color")
}

func TestCustomModelResourceRejectsConfiguredUnknownVersion(t *testing.T) {
	requireTerraform(t)
	t.Setenv("TF_VAR_credential", "unknown-version-secret")
	api := newCustomModelTestAPI(t)
	first := customModelConfig(api.server.URL, "unknown-version-model", "unknown-version-create", 1, true)
	second := customModelConfig(api.server.URL, "unknown-version-model", "unknown-version-create", 2, true)
	second = strings.Replace(second, "credential_version = 2", "credential_version = terraform_data.version.output", 1) + "\nresource \"terraform_data\" \"version\" { input = 2 }\n"
	helperresource.UnitTest(t, helperresource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true,
		Steps: []helperresource.TestStep{
			{Config: first},
			{Config: second, PlanOnly: true, ExpectError: regexp.MustCompile(`(?i)known credential version`)},
			{Config: customModelConfig(api.server.URL, "unknown-version-model", "unknown-version-create", 2, true), Check: helperresource.ComposeTestCheckFunc(helperresource.TestCheckResourceAttr(customModelAddress, "credential_version", "2"), checkCustomModelWrites(api, 1, 1, 1, 1))},
		},
	})
}

func TestCustomModelResourceInvalidCreateAndRotationIntents(t *testing.T) {
	requireTerraform(t)
	t.Setenv("TF_VAR_credential", "validation-secret-canary")
	for _, tc := range []struct {
		name, creationKey string
		version           int64
		credential        bool
		want              string
	}{{"missing-key", "", 1, true, `(?i)creation.*key`}, {"missing-version", "validation-key", -1, true, `(?i)credential.version|credential version`}, {"missing-credential", "validation-key", 1, false, `(?i)credential`}} {
		t.Run(tc.name, func(t *testing.T) {
			api := newCustomModelTestAPI(t)
			helperresource.UnitTest(t, helperresource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, Steps: []helperresource.TestStep{{Config: customModelConfig(api.server.URL, "invalid-create", tc.creationKey, tc.version, tc.credential), ExpectError: regexp.MustCompile(tc.want)}}})
			api.mu.Lock()
			defer api.mu.Unlock()
			if api.postAttempts != 0 || api.creates != 0 || api.patchAttempts != 0 || api.deleteAttempts != 0 {
				t.Fatal("invalid creation intent reached a mutating endpoint")
			}
		})
	}
	api := newCustomModelTestAPI(t)
	first := customModelConfig(api.server.URL, "invalid-rotation", "valid-create", 1, true)
	helperresource.UnitTest(t, helperresource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, Steps: []helperresource.TestStep{
		{Config: first},
		{Config: customModelConfig(api.server.URL, "invalid-rotation", "valid-create", 0, false), PlanOnly: true, ExpectError: regexp.MustCompile(`(?i)cannot decrease`)},
		{Config: customModelConfig(api.server.URL, "invalid-rotation", "valid-create", 2, false), PlanOnly: true, ExpectError: regexp.MustCompile(`(?i)rotation credential required|credential_wo`)},
		{Config: first, ConfigPlanChecks: helperresource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: checkCustomModelWrites(api, 1, 0, 0, 0)},
	}})
}

func TestCustomModelResourceReadFailuresPreserveState(t *testing.T) {
	requireTerraform(t)
	t.Setenv("TF_VAR_credential", "read-failure-secret")
	for _, code := range []int{403, 500} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			api := newCustomModelTestAPI(t)
			config := customModelConfig(api.server.URL, "read-failure", "read-failure-create", 1, true)
			helperresource.UnitTest(t, helperresource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, Steps: []helperresource.TestStep{
				{Config: config},
				{PreConfig: func() { api.mu.Lock(); api.getFailure = code; api.mu.Unlock() }, Config: config, PlanOnly: true, ExpectError: regexp.MustCompile(fmt.Sprint(code))},
				{PreConfig: func() { api.mu.Lock(); api.getFailure = 0; api.mu.Unlock() }, Config: config, ConfigPlanChecks: helperresource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: checkCustomModelWrites(api, 1, 0, 0, 0)},
			}})
		})
	}
}

func TestCustomModelResourceHistoricalResultNeedsImport(t *testing.T) {
	api := newCustomModelTestAPI(t)
	h := newCustomModelCLI(t, customModelConfig(api.server.URL, "historical-result-model", "historical-create", 1, true))
	h.run("historical-first-secret", "", "apply", "-auto-approve", "-input=false", "-no-color")
	id := h.attributes()["id"].(string)
	h.write(customModelConfig(api.server.URL, "historical-result-model", "historical-create", 2, true))
	api.mu.Lock()
	api.changeAfterUpdate = true
	api.mu.Unlock()
	h.run("historical-second-secret", `(?i)review|recovery`, "apply", "-auto-approve", "-input=false", "-no-color")
	attrs := h.attributes()
	if attrs["credential_version"] != float64(1) || attrs["operation_pending"] != true {
		t.Fatal("a historical receipt acknowledged a superseded credential")
	}
	h.run("unused", `(?i)recovery|changed|import`, "plan", "-input=false", "-no-color")
	if err := checkCustomModelWrites(api, 1, 1, 1, 1)(nil); err != nil {
		t.Fatal(err)
	}
	api.mu.Lock()
	if api.models[id].Credential != "later-credential-fixture" {
		t.Fatal("recovery overwrote a later credential")
	}
	api.mu.Unlock()
	h.run("unused", "", "state", "rm", customModelAddress)
	h.write(customModelConfig(api.server.URL, "historical-result-model", "", -1, false))
	h.run("unused", "", "import", "-input=false", "-no-color", customModelAddress, id)
	h.run("unused", "", "destroy", "-auto-approve", "-input=false", "-no-color")
}

func TestCustomModelResourceHiddenCreationEditCannotBeAcknowledged(t *testing.T) {
	api := newCustomModelTestAPI(t)
	api.hiddenAfterCreate = true
	h := newCustomModelCLI(t, customModelConfig(api.server.URL, "hidden-create-model", "hidden-create-key", 1, true))
	h.run("hidden-create-original-secret", `(?i)confirm|review|import`, "apply", "-auto-approve", "-input=false", "-no-color")
	attrs := h.attributes()
	if attrs["operation_pending"] != true || attrs["credential_version"] != float64(0) {
		t.Fatal("matching public metadata incorrectly acknowledged a changed private definition")
	}
	id := attrs["id"].(string)
	api.mu.Lock()
	if api.models[id].Credential != "hidden-create-credential-fixture" || api.creates != 1 || api.patchAttempts != 0 || !api.creationReceipts["hidden-create-key"].Stateful {
		t.Fatal("creation proof did not retain the one original identity or used the legacy create contract")
	}
	api.mu.Unlock()
	h.run("unused", `(?i)recovery|import`, "plan", "-input=false", "-no-color")
	h.run("unused", "", "state", "rm", customModelAddress)
	h.write(customModelConfig(api.server.URL, "hidden-create-model", "", -1, false))
	h.run("unused", "", "import", "-input=false", "-no-color", customModelAddress, id)
	h.run("unused", "", "destroy", "-auto-approve", "-input=false", "-no-color")
}

func TestCustomModelResourceNeverFallsBackFromStatefulCreation(t *testing.T) {
	api := newCustomModelTestAPI(t)
	api.stateCreateMissing = true
	h := newCustomModelCLI(t, customModelConfig(api.server.URL, "old-server-model", "old-server-key", 1, true))
	h.run("old-server-secret", `(?i)404|creation|create`, "apply", "-auto-approve", "-input=false", "-no-color")
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.creates != 0 || api.patchAttempts != 0 {
		t.Fatal("an old server created a definition without the transactional baseline proof")
	}
	for _, request := range api.requestLog {
		if request == "POST /management/v1/custom-models" || strings.HasPrefix(request, "GET /management/v1/custom-models/operations/") {
			t.Fatal("the provider fell back to the legacy creation contract")
		}
	}
}
