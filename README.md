# Terraform provider for Opper

Manage Opper configuration through the [Management API](https://docs.opper.ai/control-plane/management-api).
This local implementation provides projects, project-owned runtime keys, rules and dynamic-route drafts. It is unreleased and has not been published to GitHub or the Terraform Registry.

The [coverage plan](docs/coverage.md) tracks the remaining management surface and backend prerequisites. Route deployment and member management are not implemented yet.

| Resource | Manages |
|---|---|
| [`opper_project`](docs/resources/project.md) | Projects, UUID import, name and description updates |
| [`opper_api_key`](docs/resources/api_key.md) | Project-owned runtime keys, replacement and rotation |
| [`opper_rule`](docs/resources/rule.md) | Compliance, guard, observe and routing rules through typed scopes and JSON configuration |
| [`opper_dynamic_route`](docs/resources/dynamic_route.md) | Route metadata and drafts; active deployment is observed only |

The [`opper_project` data source](docs/data-sources/project.md) looks up an existing project by UUID.

## Configure

Create a management API key in the Opper platform and provide it through `OPPER_MANAGEMENT_API_KEY`. A runtime key cannot authenticate to the Management API. Managing projects requires `projects:read`, `projects:write` and `projects:delete`; a lookup needs only `projects:read`.

```hcl
terraform {
  required_providers {
    opper = {
      source = "opper-ai/opper"
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

These tests create and delete task-owned projects, runtime keys, rules and route drafts in the credential's organization. Rule tests need a plan supporting Control Plane rules. Do not point them at an organization that holds user data. Live Opper acceptance has not yet been verified.

Run `make check` for formatting, vet, race-enabled local tests and a provider build. GitHub CI uses the same command once the repository is published. Until then, feature work remains in local commits.

## Lifecycle behavior

- Project UUIDs are Terraform resource IDs. Name and description changes update the existing project, sending only the fields that changed.
- Omitting `description` sets it to an empty string, including clearing a previously managed description.
- Refresh detects edits made outside Terraform. A missing project is removed from state, so a subsequent apply can recreate it.
- Destroy deletes the project and permanently revokes **all runtime keys belonging to it**, including keys created outside Terraform. Use Terraform's `prevent_destroy` when appropriate.
- Opper can refuse deletion, including when a signed DPA/model-access lock requires an owner action in the platform. The provider preserves state on API errors; it does not bypass these protections.
- Project creation has no idempotency-key contract. The provider does not blindly retry mutations or adopt projects by name. If a create request may have succeeded but Terraform could not save its response, inspect the project list and import the UUID before retrying.

Do not commit credentials, Terraform state or plan files. Supply the management key through the environment rather than placing it in HCL.

Runtime-key secrets are returned once and retained in Terraform state with the sensitive flag. Import recovers key metadata only. Protect the state backend and review the [key lifecycle](docs/resources/api_key.md) before rotation.

The route resource manages drafts only. Current deployment endpoints lack revision preconditions and idempotent deployment receipts; Terraform cannot guarantee it would deploy the graph it planned in the presence of concurrent edits. Deployment remains a documented API prerequisite rather than an implicit apply side effect.
