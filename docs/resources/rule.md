---
page_title: "opper_rule Resource - Opper"
description: "Manage an Opper Control Plane rule."
---

# opper_rule (Resource)

Manages a rule in the management key's organization through the public Management API. All four rule kinds are supported: `comply`, `guard`, `observe`, and `route`. The API validates the configuration vocabulary, plan entitlement, scope ownership, model access and policy constraints.

The credential needs `controls:read` and `controls:write`. Rule writes require the appropriate Control Plane plan features; plan refusals are reported as API errors. Listing and deleting rules remain available when writes are plan gated.

```hcl
resource "opper_project" "app" {
  name = "checkout-service"
}

resource "opper_rule" "retention" {
  kind    = "comply"
  name    = "Checkout trace retention"
  enabled = true

  scope = {
    type          = "projects"
    project_uuids = [opper_project.app.id]
  }

  config_json = jsonencode({
    type           = "retention"
    retention_days = 7
  })
}
```

## Schema

| Attribute | Type | Behavior | Description |
|---|---|---|---|
| `kind` | String | Required; replacement | `comply`, `guard`, `observe`, or `route`. |
| `scope` | Object | Required; replacement | Scope type and target sets, described below. |
| `config_json` | String | Required | Desired API configuration encoded as a JSON object. A change replaces the full configuration. Prefer `jsonencode` for stable formatting. |
| `name` | String | Optional | Display name; defaults to `""`. Removing it clears the name. An API null name reads as an empty string. |
| `enabled` | Boolean | Optional | Defaults to `false`, matching the API. Set it explicitly to enable enforcement. |
| `schema_version` | Number | Optional | Positive integer, default `1`. The server validates supported versions and the meaning of the configuration at that version. |
| `id` | String | Read-only | Client-generated UUID, also used for import. |
| `effective_config_json` | String | Read-only | Canonical configuration returned by Opper, including defaults, normalization and generated guard handles. |
| `created_at` | String | Read-only | Creation timestamp, when supplied by Opper. |
| `updated_at` | String | Read-only | Latest update timestamp, when supplied by Opper. |

### Scope

`scope.type` is required. The target set matching that type must be nonempty; the other sets default to empty. Target order is ignored. UUID targets must use canonical lowercase hexadecimal so a server round-trip cannot turn an immutable scope into a replacement.

| `scope.type` | Target set | Notes |
|---|---|---|
| `org` | None | Organization-wide rule. |
| `projects` | `project_uuids` | Project UUIDs belonging to this organization. |
| `functions` | `function_uuids` | Function UUIDs. Only API-supported combinations of kind/config and function scope are accepted. |
| `roles` | `role_names` | Only `comply` budget rules; caps each member holding a listed role. |
| `members` | `member_uuids` | Only `comply` budget rules; targets user UUIDs of organization members. |

The provider checks scope structure locally. Opper checks that targets exist, belong to the organization and support the rule. The legacy `member` role cannot be targeted by a budget rule.

Kind and the entire scope are immutable in the API. Changing either replaces the rule with a new UUID. The default destroy-before-create order can cause a temporary enforcement gap. `create_before_destroy` can avoid that gap where overlapping policies are valid; the API may reject overlapping rules or policy violations. Review the plan for these transitions. Guard handles are unique within the organization: using `create_before_destroy` with an explicit unchanged `config_json.handle` can be refused because the old guard still owns that handle. Change the handle for the replacement or use the default destroy-before-create order.

## Configuration and drift

`config_json` is the desired input. `effective_config_json` is the server's canonical result. Opper may add guard defaults/handles or normalize observe sampling parameters, so these values can differ. The provider keeps the desired input when a refreshed canonical result matches the last observed canonical result. A remote change to that result becomes a Terraform difference and is restored on apply. If a metadata-only update returns an unexpectedly changed configuration, the provider saves that observed policy and reports an error so the next plan exposes the drift.

Object key order and whitespace in API responses do not cause repeated writes. Editing only the formatting of a configured JSON string can produce one Terraform state update, with no API mutation; the next unchanged plan is empty. Use `jsonencode` to keep configuration formatting consistent.

Schema versions are policy semantics, not revision counters. Use the version required by the API vocabulary for a given configuration. The provider does not impose a stale maximum from the published request schema; the running server is authoritative. Configuration updates send only changed metadata/configuration fields, and configuration itself is replaced rather than merged.

A deprecated `confirm_file_deletion` parameter is intentionally absent. Current Opper rules do not delete stored files, and the API ignores that parameter.

## Import

Import an existing rule by UUID:

```sh
terraform import opper_rule.retention 01234567-89ab-4cde-8012-3456789abcde
```

The API has no rule-by-ID GET endpoint. Refresh and import list the organization's rules and select the exact UUID. Import recovers the canonical server configuration; it cannot reconstruct the original sparse JSON input. Review the resulting plan and align `config_json` with the desired policy. A successful list that omits this UUID marks the resource missing. Authorization failures, list endpoint 404s, malformed responses and other API failures preserve state.

## Create and recovery

The provider generates a UUID and sends a stable creation receipt derived from it. It does not blindly repeat create requests. When a create response is uncertain, the provider makes one read for that exact UUID and recovers success if the rule is present. If that read cannot establish the result, it retains the UUID in Terraform state and reports an error. Terraform marks failed creates as tainted, so the next plan can propose replacement even if refresh finds the rule. Inspect the exact remote rule before accepting that replacement or using `terraform untaint` to retain it. If a successful list confirms the UUID is absent, Terraform can create a replacement. No recovery path adopts a same-named rule or repeats the original mutation blindly.

Deleting a rule disables its enforcement by removing it. The provider accepts a targeted delete's 404 as already deleted. Other refusals preserve the resource in state.
