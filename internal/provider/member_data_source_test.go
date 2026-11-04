package provider

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/opper-ai/terraform-provider-opper/internal/client"
)

func memberTestConfig(baseURL, blocks string) string {
	return fmt.Sprintf(`
provider "opper" {
 management_api_key = "op-mak-local-test"
 base_url = %q
}
%s
`, baseURL, blocks)
}

func memberLookupBlocks(email string) string {
	return fmt.Sprintf(`
data "opper_member" "by_uuid" { uuid = %q }
data "opper_member" "by_email" { email = %q }
data "opper_members" "all" {}
`, memberTestAliceUUID, email)
}

func checkMemberFixtureRead(api *memberTestAPI) resource.TestCheckFunc {
	return func(*terraform.State) error {
		api.mu.Lock()
		defer api.mu.Unlock()
		if api.reads == 0 {
			return fmt.Errorf("data source did not read the member collection")
		}
		if len(api.unexpected) != 0 {
			return fmt.Errorf("unexpected membership operations: %v", api.unexpected)
		}
		return nil
	}
}

func TestMemberDataSourcesLifecycle(t *testing.T) {
	requireTerraform(t)
	api := newMemberTestAPI(t)
	first := memberTestConfig(api.server.URL, memberLookupBlocks("ALICE@EXAMPLE.COM"))
	changed := memberTestConfig(api.server.URL, memberLookupBlocks("ALICE.RENAMED@EXAMPLE.COM"))
	check := func(email, name, role, owner string) resource.TestCheckFunc {
		checks := []resource.TestCheckFunc{checkMemberFixtureRead(api),
			resource.TestCheckResourceAttr("data.opper_members.all", "id", "members"),
			resource.TestCheckResourceAttr("data.opper_members.all", "members.#", "2"),
			resource.TestCheckResourceAttr("data.opper_members.all", "members.0.uuid", memberTestAliceUUID),
			resource.TestCheckResourceAttr("data.opper_members.all", "members.1.uuid", memberTestBobUUID),
			resource.TestCheckResourceAttr("data.opper_members.all", "members.1.name", ""),
			resource.TestCheckResourceAttr("data.opper_members.all", "members.1.email", ""),
			resource.TestCheckResourceAttr("data.opper_members.all", "members.1.role", ""),
			resource.TestCheckNoResourceAttr("data.opper_members.all", "members.1.created_at"),
		}
		for _, address := range []string{"data.opper_member.by_uuid", "data.opper_member.by_email"} {
			checks = append(checks,
				resource.TestCheckResourceAttr(address, "id", memberTestAliceUUID),
				resource.TestCheckResourceAttr(address, "member.uuid", memberTestAliceUUID),
				resource.TestCheckResourceAttr(address, "member.email", email),
				resource.TestCheckResourceAttr(address, "member.name", name),
				resource.TestCheckResourceAttr(address, "member.role", role),
				resource.TestCheckResourceAttr(address, "member.is_owner", owner),
			)
		}
		return resource.ComposeTestCheckFunc(checks...)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true,
		CheckDestroy: checkMemberFixtureRead(api),
		Steps: []resource.TestStep{
			{Config: first, Check: resource.ComposeTestCheckFunc(check("alice@example.com", "Alice", "admin", "true"),
				resource.TestCheckResourceAttr("data.opper_member.by_uuid", "uuid", memberTestAliceUUID),
				resource.TestCheckResourceAttr("data.opper_member.by_email", "email", "ALICE@EXAMPLE.COM"),
				resource.TestCheckResourceAttr("data.opper_member.by_uuid", "member.created_at", "2026-11-03T09:00:00Z"))},
			{Config: first, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: check("alice@example.com", "Alice", "admin", "true")},
			{PreConfig: func() {
				api.mu.Lock()
				defer api.mu.Unlock()
				for i := range api.members {
					if api.members[i].UUID == memberTestAliceUUID {
						api.members[i].Email = "alice.renamed@example.com"
						api.members[i].Name = ""
						api.members[i].Role = "custom-reviewer"
						api.members[i].IsOwner = false
						api.members[i].CreatedAt = nil
					}
				}
			}, Config: changed, Check: resource.ComposeTestCheckFunc(check("alice.renamed@example.com", "", "custom-reviewer", "false"),
				resource.TestCheckResourceAttr("data.opper_member.by_email", "email", "ALICE.RENAMED@EXAMPLE.COM"),
				resource.TestCheckNoResourceAttr("data.opper_member.by_uuid", "member.created_at"))},
			{Config: changed, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: check("alice.renamed@example.com", "", "custom-reviewer", "false")},
		},
	})
}

func TestMemberDataSourceMissingAndAmbiguous(t *testing.T) {
	requireTerraform(t)
	for _, tc := range []struct {
		name, selector string
		ambiguous      bool
	}{
		{"missing_uuid", `uuid = "99999999-9999-4999-8999-999999999999"`, false},
		{"missing_email", `email = "missing@example.com"`, false},
		{"unmatched_email_string", `email = "not-an-email"`, false},
		{"exact_email_only", `email = "ali@example.com"`, false},
		{"ambiguous_email", `email = "ALICE@EXAMPLE.COM"`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := newMemberTestAPI(t)
			if tc.ambiguous {
				api.members = append(api.members, memberTestRecord{UUID: "33333333-3333-4333-8333-333333333333", Email: "Alice@Example.Com", Name: "Another account", Role: "viewer"})
			}
			want := `(?i)not found|no member|no.*match`
			if tc.ambiguous {
				want = `(?i)ambiguous|multiple|more than one`
			}
			config := memberTestConfig(api.server.URL, fmt.Sprintf(`data "opper_member" "test" { %s }`, tc.selector))
			resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, Steps: []resource.TestStep{{Config: config, ExpectError: regexp.MustCompile(want)}}})
		})
	}
}

func TestMemberDataSourceSelectorValidation(t *testing.T) {
	requireTerraform(t)
	for _, tc := range []struct{ name, selector string }{
		{"neither", ``},
		{"both", `uuid = "11111111-1111-4111-8111-111111111111"
email = "alice@example.com"`},
		{"malformed_uuid", `uuid = "not-a-uuid"`},
		{"uppercase_uuid", `uuid = "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA"`},
		{"empty_email", `email = ""`},
		{"spaced_email", `email = " alice@example.com "`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := newMemberTestAPI(t)
			config := memberTestConfig(api.server.URL, fmt.Sprintf("data \"opper_member\" \"test\" {\n%s\n}", tc.selector))
			resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, Steps: []resource.TestStep{{Config: config, ExpectError: regexp.MustCompile(`(?i)uuid|email|selector|attribute|exactly one`)}}})
			api.mu.Lock()
			defer api.mu.Unlock()
			if api.reads != 0 {
				t.Fatal("invalid member selector reached the API")
			}
		})
	}
}

func TestMemberDataSourceUnknownSelectorDeferred(t *testing.T) {
	requireTerraform(t)
	api := newMemberTestAPI(t)
	// terraform_data is built into Terraform. Its output is unknown until apply,
	// so this exercises real deferred data-source reads without an Opper write.
	config := memberTestConfig(api.server.URL, `
resource "terraform_data" "selector" { input = "ALICE@EXAMPLE.COM" }
data "opper_member" "test" { email = terraform_data.selector.output }
`)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true,
		Steps: []resource.TestStep{
			{Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("data.opper_member.test", plancheck.ResourceActionRead)}}, Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr("data.opper_member.test", "id", memberTestAliceUUID), resource.TestCheckResourceAttr("data.opper_member.test", "email", "ALICE@EXAMPLE.COM"), checkMemberFixtureRead(api))},
			{Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
		},
	})
}

func TestMemberDataSourceMembershipDisappears(t *testing.T) {
	requireTerraform(t)
	api := newMemberTestAPI(t)
	original := append([]memberTestRecord{}, api.members...)
	config := memberTestConfig(api.server.URL, fmt.Sprintf(`data "opper_member" "test" { uuid = %q }`, memberTestAliceUUID))
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true,
		Steps: []resource.TestStep{
			{Config: config, Check: resource.TestCheckResourceAttr("data.opper_member.test", "id", memberTestAliceUUID)},
			{PreConfig: func() { api.setMembers(nil) }, Config: config, PlanOnly: true, ExpectError: regexp.MustCompile(`(?i)not found|no member|no.*match`)},
			{PreConfig: func() { api.setMembers(original) }, Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: resource.TestCheckResourceAttr("data.opper_member.test", "id", memberTestAliceUUID)},
		},
	})
}

func TestMembersDataSourceEmptyCollection(t *testing.T) {
	requireTerraform(t)
	api := newMemberTestAPI(t)
	api.setMembers(nil)
	config := memberTestConfig(api.server.URL, `data "opper_members" "all" {}`)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true,
		Steps: []resource.TestStep{
			{Config: config, Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr("data.opper_members.all", "id", "members"), resource.TestCheckResourceAttr("data.opper_members.all", "members.#", "0"), checkMemberFixtureRead(api))},
			{Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}},
		},
	})
}

func TestMemberDataSourcesAPIFailure(t *testing.T) {
	requireTerraform(t)
	for _, code := range []int{403, 500} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			api := newMemberTestAPI(t)
			config := memberTestConfig(api.server.URL, memberLookupBlocks("alice@example.com"))
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true,
				Steps: []resource.TestStep{
					{Config: config, Check: resource.TestCheckResourceAttr("data.opper_members.all", "members.#", "2")},
					{PreConfig: func() { api.mu.Lock(); api.failure = code; api.mu.Unlock() }, Config: config, PlanOnly: true, ExpectError: regexp.MustCompile(strconv.Itoa(code))},
					{PreConfig: func() { api.mu.Lock(); api.failure = 0; api.mu.Unlock() }, Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr("data.opper_members.all", "members.#", "2"), resource.TestCheckResourceAttr("data.opper_member.by_uuid", "id", memberTestAliceUUID))},
				},
			})
		})
	}
}

func TestAccMemberDataSources(t *testing.T) {
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
	readMembers := func() ([]client.Member, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		members, err := api.ListMembers(ctx)
		sort.Slice(members, func(i, j int) bool { return members[i].UUID < members[j].UUID })
		return members, err
	}
	original, err := readMembers()
	if err != nil {
		t.Fatal(err)
	}
	if len(original) == 0 {
		t.Skip("live member lookup requires an existing member; no membership is created by this test")
	}
	selected := original[0]
	blocks := fmt.Sprintf("data \"opper_members\" \"all\" {}\ndata \"opper_member\" \"by_uuid\" { uuid = %q }\n", selected.UUID)
	checks := []resource.TestCheckFunc{
		resource.TestCheckResourceAttr("data.opper_members.all", "id", "members"),
		resource.TestCheckResourceAttr("data.opper_members.all", "members.#", strconv.Itoa(len(original))),
		resource.TestCheckResourceAttr("data.opper_member.by_uuid", "id", selected.UUID),
		resource.TestCheckResourceAttr("data.opper_member.by_uuid", "member.email", selected.Email),
		resource.TestCheckResourceAttr("data.opper_member.by_uuid", "member.name", selected.Name),
		resource.TestCheckResourceAttr("data.opper_member.by_uuid", "member.role", selected.Role),
		resource.TestCheckResourceAttr("data.opper_member.by_uuid", "member.is_owner", strconv.FormatBool(selected.IsOwner)),
	}
	if selected.Email != "" {
		matches := 0
		for _, member := range original {
			if strings.EqualFold(member.Email, selected.Email) {
				matches++
			}
		}
		if matches == 1 {
			email := strings.ToUpper(selected.Email)
			blocks += fmt.Sprintf("data \"opper_member\" \"by_email\" { email = %q }\n", email)
			checks = append(checks, resource.TestCheckResourceAttr("data.opper_member.by_email", "id", selected.UUID), resource.TestCheckResourceAttr("data.opper_member.by_email", "email", email))
		}
	}
	// This live configuration contains only data sources, and only the member
	// collection endpoint is read by the client. No invites or test users exist.
	config := "provider \"opper\" {}\n" + blocks
	unchanged := func(*terraform.State) error {
		current, err := readMembers()
		if err != nil {
			return err
		}
		if len(current) != len(original) {
			return fmt.Errorf("live membership changed while testing its read-only data sources")
		}
		for i := range original {
			a, b := original[i], current[i]
			if a.UUID != b.UUID || a.Email != b.Email || a.Name != b.Name || a.Role != b.Role || a.IsOwner != b.IsOwner || (a.CreatedAt == nil) != (b.CreatedAt == nil) || (a.CreatedAt != nil && *a.CreatedAt != *b.CreatedAt) {
				return fmt.Errorf("live member metadata changed during read-only lookup test")
			}
		}
		return nil
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: projectProviderFactories(), PreventPostDestroyRefresh: true, CheckDestroy: unchanged,
		Steps: []resource.TestStep{
			{Config: config, Check: resource.ComposeTestCheckFunc(append(checks, unchanged)...)},
			{Config: config, ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}, Check: unchanged},
		},
	})
}
