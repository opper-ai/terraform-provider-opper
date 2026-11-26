# Changelog

## Unreleased

- Fix `opper_api_key` creation with a nonempty `allowed_ips` list when the Management API advertises the field as optional on updates.

## v0.3.0

See the [v0.3.0 release notes](docs/releases/v0.3.0.md) for upgrade guidance and verification limits.

- Add the `opper_project_model_list` resource and data source. The resource gives one project its own compat model list, which replaces the organization's for the project's API keys: `compat_model_list`, `compat_model_list_dynamic_routes` (default `true`) and `compat_model_list_enforced`. It imports by project UUID, refuses to create over a list set elsewhere, and on destroy makes the project follow the organization again. The data source reports whether a project follows the organization (`follows_organization`) and its own list when it has one.
- Add `compat_model_list_enforced` to the `opper_organization_model_list` resource (optional, computed) and data source (read-only): whether the list also limits which models the organization's API keys can call. When the resource omits it, the provider never sends it and the current value is kept and reported. An Opper API that predates the field leaves it out of responses; the provider reads that as `false` instead of failing.
- Fix `opper_organization_model_list` failing every read, refresh, plan and import with "returned an invalid organization model-list response" for an organization whose compat model list is set to `none` on the platform. `compat_model_list` now accepts `none` (list only the organization's dynamic routes) in the resource and data source.
- Add `compat_model_list_dynamic_routes` to the `opper_organization_model_list` resource (optional, computed) and data source (read-only): whether the organization's dynamic routes are listed. When the resource omits it, the provider never sends it and the current value is kept and reported. `compat_model_list = "none"` with `compat_model_list_dynamic_routes = false` is refused at plan time, since it would list nothing.
- The organization model-list resource and data source now require the Management API to return `compat_model_list_dynamic_routes`, which the hosted Opper API does since it added `none`. A response without it is an error.

## v0.2.1

See the [v0.2.1 release notes](docs/releases/v0.2.1.md) for upgrade guidance and verification limits.

- Normalize newly imported rule JSON object ordering and whitespace so matching `jsonencode` configuration imports without a formatting-only update. Preserve existing desired JSON while the remote policy is unchanged, the effective API snapshot, array ordering and numeric precision.

## v0.2.0

See the [v0.2.0 release notes](docs/releases/v0.2.0.md) for upgrade guidance and verification limits.

- Manage runtime-key public IP/CIDR allowlists with `opper_api_key.allowed_ips`, including atomic creation, refresh/import and in-place updates without secret rotation. Omission preserves existing-key restrictions; explicit `[]` clears them. Restricted replacement requires an explicit policy.
- Create projects with an atomic initial access policy using `opper_project.initial_access`; requires the new `/management/v1/projects/with-access` backend endpoint.
- Preserve reviewed release notes in draft builds and verify the release path locally.
- Fix GitHub-only documentation links when viewed through the Terraform Registry or GitHub releases.
- Build with Go 1.27.4 and update Go dependencies to patched versions.

## v0.1.0

Initial Terraform provider for the Opper Management API. See the [v0.1.0 release notes](docs/releases/v0.1.0.md) for installation, availability and lifecycle notes.

- Manage projects, default project webhook URLs and project-owned runtime API keys.
- Manage organization model-list preferences and compliance, guard, observe and routing rules.
- Manage dynamic-route drafts and conditional live deployments as separate resources.
- Manage default OpenAI BYOK custom-model definitions with write-only credentials and explicit credential versions. Requires Terraform 1.11 or later.
- Manage Enterprise custom roles and existing member-role assignments, with conditional updates and explicit assignment retention or fallback on destroy.
- Manage existing-project access policies using saved-plan preconditions. Writes require dedicated management-key scopes; destroy retains the policy.
- Look up projects, members, model-list preferences, custom models, role definitions/catalogs and member-role assignments through nine data sources.

The initial release includes 11 resources. Invitations, membership removal, ownership transfer, billing and other organization settings are outside its scope. Project creation followed by an access-policy update is not atomic Restricted creation. Runtime-key secrets are retained in Terraform state; protect the state backend.

Local Terraform/API tests and the documented live acceptance workflows cover lifecycle behavior and failure handling. See [live acceptance](docs/live-acceptance.md) for the tested environments and limits.
