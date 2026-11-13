# Changelog

## Unreleased

- Preserve reviewed release notes in draft builds and verify the release path locally.
- Fix GitHub-only documentation links when viewed through the Terraform Registry or GitHub releases.

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
