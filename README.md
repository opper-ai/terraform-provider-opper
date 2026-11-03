# Terraform provider for Opper

Manage Opper projects through the [Management API](https://docs.opper.ai/control-plane/management-api).
This first implementation provides the `opper_project` resource and a project lookup data source. It is unreleased and has not been published to the Terraform Registry.

The [coverage plan](docs/coverage.md) tracks the remaining management surface. Keys, rules, routes and members are not implemented yet.

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

The local lifecycle tests use a real Terraform CLI with a local HTTP test server. They exercise provider behavior without an Opper credential or production changes. To use an installed Terraform executable explicitly, set `TF_ACC_TERRAFORM_PATH` to its absolute path.

Live acceptance tests are opt-in. Provide `OPPER_MANAGEMENT_API_KEY` privately for a confirmed disposable test organization, then run:

```sh
TF_ACC=1 OPPER_TEST_DISPOSABLE_ORG=1 \
  go test ./internal/provider -run '^TestAccProjectResource$' -v
```

This creates and deletes a uniquely named project in the credential's organization. Do not point it at an organization that holds user data. Live Opper acceptance has not yet been verified for this first slice.

## Lifecycle behavior

- Project UUIDs are Terraform resource IDs. Name and description changes update the existing project, sending only the fields that changed.
- Omitting `description` sets it to an empty string, including clearing a previously managed description.
- Refresh detects edits made outside Terraform. A missing project is removed from state, so a subsequent apply can recreate it.
- Destroy deletes the project and permanently revokes **all runtime keys belonging to it**, including keys created outside Terraform. Use Terraform's `prevent_destroy` when appropriate.
- Opper can refuse deletion, including when a signed DPA/model-access lock requires an owner action in the platform. The provider preserves state on API errors; it does not bypass these protections.
- Project creation has no idempotency-key contract. The provider does not blindly retry mutations or adopt projects by name. If a create request may have succeeded but Terraform could not save its response, inspect the project list and import the UUID before retrying.

Do not commit credentials, Terraform state or plan files. Supply the management key through the environment rather than placing it in HCL.
