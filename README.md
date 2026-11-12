# Terraform provider for Opper

Manage Opper configuration through the [Management API](https://docs.opper.ai/control-plane/management-api).
The first release includes projects and access policies, project webhooks and runtime keys, organization model-list preferences, rules, dynamic-route drafts and deployments, BYOK custom models, custom roles and existing member-role assignments. Source is maintained in [opper-ai/terraform-provider-opper](https://github.com/opper-ai/terraform-provider-opper). Version **0.1.0 is being prepared**; no Terraform Registry release is published yet.

The [coverage plan](docs/coverage.md) distinguishes supported configuration from deferred features. The APIs required by the implemented resources are deployed. Custom roles require Enterprise, and project-access policy writes require activation for the credential's organization. Member-role assignments manage existing memberships only; invitations, removals and ownership transfer remain outside provider scope.

| Resource | Manages |
|---|---|
| [`opper_custom_role`](docs/resources/custom_role.md) | Enterprise custom definitions, pinned updates/deletion and receipt recovery |
| [`opper_member_role`](docs/resources/member_role.md) | Existing member roles with reviewed destination definitions and explicit retain/fallback destruction |
| [`opper_custom_model`](docs/resources/custom_model.md) | Default OpenAI BYOK definitions, write-only credentials and guarded recovery; requires Terraform 1.11+ |
| [`opper_project`](docs/resources/project.md) | Projects, UUID import, name and description updates |
| [`opper_project_access`](docs/resources/project_access.md) | Conditional policies on existing projects, UUID import and retain-on-destroy; policy writes require organization activation |
| [`opper_project_webhook`](docs/resources/project_webhook.md) | Default webhook URL, project UUID import and URL clearing on destroy |
| [`opper_organization_model_list`](docs/resources/organization_model_list.md) | Advertised compat model-list preference; destroy retains the preference |
| [`opper_api_key`](docs/resources/api_key.md) | Project-owned runtime keys, replacement and rotation |
| [`opper_rule`](docs/resources/rule.md) | Compliance, guard, observe and routing rules through typed scopes and JSON configuration |
| [`opper_dynamic_route`](docs/resources/dynamic_route.md) | Route metadata and drafts; active deployment is observed only |
| [`opper_dynamic_route_deployment`](docs/resources/dynamic_route_deployment.md) | Live activation/off/on with exact draft and active-version preconditions |

The [`opper_project` data source](docs/data-sources/project.md) looks up an existing project by UUID.

[`opper_project_access`](docs/resources/project_access.md) manages an existing project's Organization or Restricted policy using dedicated `project_access:read` and `project_access:write` scopes. Writes remain disabled by default pending controlled activation. See the [existing-project example](examples/project-access/main.tf); creating a project and restricting it afterward does not make it private from creation. The organization and project cannot change after adoption; use a new resource address for another project and keep the old resource until the new policy apply succeeds. Destroy retains the policy.

[`opper_role`](docs/data-sources/role.md) and [`opper_roles`](docs/data-sources/roles.md) expose definitions and the permission catalog, including reviewed assignment tokens. [`opper_member_role`](docs/data-sources/member_role.md) reads an existing assignment without changing it. See the [roles example](examples/roles/main.tf).

[`opper_custom_model`](docs/data-sources/custom_model.md) reads one stored model by exact UUID; [`opper_custom_models`](docs/data-sources/custom_models.md) reads complete, ordered metadata inventory. Both need only `custom_models:read` and expose no credentials, arbitrary extras or state tokens.

The [`opper_member` data source](docs/data-sources/member.md) finds one existing member by UUID or email, so configurations can reference their UUID in rules. [`opper_members`](docs/data-sources/members.md) lists the organization's members in stable UUID order. Both require only `members:read` and perform no membership changes.

The [`opper_organization_model_list` data source](docs/data-sources/organization_model_list.md) reads the credential's organization UUID and current advertised-model preference without changing it.

## Install and configure

Use Terraform **1.11 or later** to use the full provider surface, including write-only BYOK credentials. Recent live acceptance used Terraform 1.15.3.

After v0.1.0 is published to the Registry, the configuration below can be installed with `terraform init`. Until then, use the [local development override](#develop-locally).

Create a management API key in the Opper platform and provide it through `OPPER_MANAGEMENT_API_KEY`. A runtime key cannot authenticate to the Management API. Managing projects requires `projects:read`, `projects:write` and `projects:delete`; a lookup needs only `projects:read`.

```hcl
terraform {
  required_version = ">= 1.11"

  required_providers {
    opper = {
      source  = "opper-ai/opper"
      version = "~> 0.1.0"
    }
  }
}

provider "opper" {}

resource "opper_project" "app" {
  name        = "checkout-service"
  description = "Checkout model calls"
}

output "project_id" {
  value = opper_project.app.id
}
```

For an existing project, import its UUID before applying:

```sh
terraform import opper_project.app 01234567-89ab-4cde-8012-3456789abcde
```

The provider reads `OPPER_BASE_URL` when `base_url` is omitted, otherwise defaults to `https://api.opper.ai`. Use a separate provider configuration and management key for each organization. See the [provider reference](docs/index.md).

## Develop locally

Install the Go version specified in `go.mod` and a Terraform CLI compatible with the provider. Build the executable into a dedicated directory:

```sh
mkdir -p "$PWD/bin"
go build -o "$PWD/bin/terraform-provider-opper" .
```

Use the resulting **absolute directory path** in a task-specific Terraform CLI configuration file:

```hcl
provider_installation {
  dev_overrides {
    "registry.terraform.io/opper-ai/opper" = "/absolute/path/to/terraform-provider-opper/bin"
  }
  direct {}
}
```

Set `TF_CLI_CONFIG_FILE` to that configuration file. For a configuration containing only this provider, run `terraform plan` and `terraform apply` directly. Do not run `terraform init` to install this unpublished provider: the development override supplies its executable, while initialization would try to locate a Registry release. Terraform's development-override warning is expected.

## Test

```sh
go test ./...
```

The local lifecycle tests use a real Terraform CLI with local HTTP test servers. They cover resources, import, drift, replacement, secret recovery and API refusals without an Opper credential or production changes. To use an installed Terraform executable explicitly, set `TF_ACC_TERRAFORM_PATH` to its absolute path.

Live acceptance tests are opt-in. Provide `OPPER_MANAGEMENT_API_KEY` privately for a confirmed disposable test organization, then run:

```sh
TF_ACC=1 OPPER_TEST_DISPOSABLE_ORG=1 \
  go test ./internal/provider -run '^TestAcc' -v
```

The baseline tests create and delete task-owned projects, runtime keys, rules and route drafts in the credential's organization. Rule tests need a plan supporting Control Plane rules. Do not point them at an organization that holds user data. Live acceptance for all four resource families passed on 2026-11-03, including cleanup verification. See the [live acceptance record](docs/live-acceptance.md) for later resource acceptance, coverage and limits.

The conditional deployment test additionally requires `OPPER_TEST_MODEL_ID` set to an exact current, permitted catalog model. It creates, updates, disables, re-enables and deletes only a task-owned route without running inference. Live acceptance of this surface passed on 2026-11-03 after the backend rollout, including exact-version import and inventory restoration; see the acceptance record.

The webhook acceptance suite creates its own temporary project, configures and updates the default URL, checks import and drift correction, then clears the URL while retaining the project before final cleanup. It sends no webhook test events or model calls. Opper initializes the signing secret on the first URL write; the suite verifies subsequent operations retain that secret.

The member data source suite only reads existing members. Run `go test ./internal/provider -run '^TestAccMemberDataSources$' -count=1 -v` with the same live opt-in flags and a key granting `members:read`. It checks the collection, UUID/email lookup and unchanged plans without inviting, removing or changing anyone. Running all live suites requires the combined scopes of their respective resources and data sources.

Run `make check` for formatting, vet, race-enabled local tests and a provider build. GitHub CI runs the same checks on `main` and pull requests. Live acceptance remains an explicit, separately authorized operation; CI receives no live organization credential.

Custom-model, custom-role and member-role live workflows also passed. Bounded project-access Terraform acceptance passed against `api.opper.ai` on 2026-11-12, including stale-plan refusal, import, retention on destroy and cleanup. Project-access writes remain disabled by default and require organization activation. Evroc Terraform acceptance was not repeated. See the [acceptance record](docs/live-acceptance.md#project-access-lifecycle-2026-11-12) before planning another live run.

## Lifecycle behavior

- Project UUIDs are Terraform resource IDs. Name and description changes update the existing project, sending only the fields that changed.
- Omitting `description` sets it to an empty string, including clearing a previously managed description.
- Refresh detects edits made outside Terraform. A missing project is removed from state, so a subsequent apply can recreate it.
- Destroy deletes the project and permanently revokes **all runtime keys belonging to it**, including keys created outside Terraform. Use Terraform's `prevent_destroy` when appropriate.
- Opper can refuse deletion, including when a signed DPA/model-access lock requires an owner action in the platform. The provider preserves state on API errors; it does not bypass these protections.
- Project creation has no idempotency-key contract. The provider does not blindly retry mutations or adopt projects by name. If a create request may have succeeded but Terraform could not save its response, inspect the project list and import the UUID before retrying.

Do not commit credentials, Terraform state or plan files. Supply the management key through the environment rather than placing it in HCL.

Runtime-key secrets are returned once and retained in Terraform state with the sensitive flag. Import recovers key metadata only. Protect the state backend and review the [key lifecycle](docs/resources/api_key.md) before rotation.

The draft route resource never deploys. Opt into live routing with the separate [deployment resource](docs/resources/dynamic_route_deployment.md). It uses only the new `/deployments` endpoint, verifies exact immutable ownership and refuses to undeploy another writer's version. Destroying the deployment retains the draft and version history.

## Releases and license

See the [release guide](docs/releasing.md) for local package verification and the
signed draft-release workflow, and the [changelog](CHANGELOG.md) for version scope.
Licensed under [Apache 2.0](LICENSE).
