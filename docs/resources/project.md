---
page_title: "opper_project Resource - Opper"
description: "Manage an Opper project."
---

# opper_project (Resource)

Creates and manages a project in the management key's organization. Omit `initial_access` for the existing Organization-access creation behavior. To create a project with Restricted access from its first moment, include a creation-only access snapshot:

```hcl
resource "opper_project" "app" {
  name        = "checkout-service"
  description = "Checkout model calls"

  initial_access = {
    mode      = "restricted"
    role_keys = ["viewer"]
  }
}
```

`initial_access` was introduced in v0.2.0. The dedicated backend endpoint deployed in both regions after [backend PR #6181](https://github.com/opper-ai/opper/pull/6181); [live Terraform acceptance](https://github.com/opper-ai/terraform-provider-opper/blob/main/docs/live-acceptance.md#atomic-initial-project-access-2026-11-13) passed on 2026-11-13.

The provider sends the project and policy in one `POST /management/v1/projects/with-access`. This requires `projects:write` and `project_access:write`; the normal project read and delete scopes are still needed for refresh and destroy. A denied, unsupported or uncertain request is never retried as Organization-access creation. No follow-up policy GET or PUT is needed to create the project. Other resources, such as custom roles, have their own transactions.

## Schema

| Attribute | Type | Behavior | Description |
|---|---|---|---|
| `name` | String | Required | Non-empty project name, unique within the organization. Leading and trailing whitespace are rejected. Changing it updates the project in place. |
| `description` | String | Optional | Project description. Defaults to an empty string. Removing it clears the previous description. |
| `initial_access` | Object | Optional, creation-only | Initial access snapshot committed atomically with a new project. Cannot be added or changed on an existing or imported project. Removing it forgets only this creation history. |
| `id` | String | Read-only | Project UUID. |
| `created_at` | String | Read-only | Creation timestamp returned by Opper, when available. |

The `initial_access` object has these fields:

| Attribute | Type | Behavior | Description |
|---|---|---|---|
| `mode` | String | Required | `organization` or `restricted`. Organization mode requires both recipient sets to be empty. |
| `role_keys` | Set of strings | Optional | Existing role keys in the organization. Defaults to `[]`; maximum 500 entries. |
| `user_uuids` | Set of strings | Optional | UUIDs of active organization members. Defaults to `[]`; maximum 500 entries. |

An empty Restricted snapshot grants access only to built-in Owners and Admins. Role and direct user grants are alternatives and retain each member's existing action permissions. Recipient values can resolve during apply, including outputs of a custom role created in the same apply.

## Initial access is creation history

`initial_access` records what Terraform requested at creation; it is **not the current policy**. Refresh and metadata updates do not read, reconcile or overwrite subsequent policy changes. An unchanged configuration remains unchanged even if another writer changes the project's policy.

Changing the snapshot or adding one after creation is rejected before any project mutation and does not replace the project. Removing `initial_access` from configuration removes that history from Terraform state and retains the remote policy. It cannot be added back to the existing project.

For ongoing policy management, explicitly import the project into [`opper_project_access`](project_access.md), then review a separate plan. That resource manages the current policy; it cannot use a newly created project's unknown UUID in the creation plan. The [restricted-project example](https://github.com/opper-ai/terraform-provider-opper/tree/main/examples/restricted-project) creates the project atomically without a second policy operation.

If Terraform also manages a granted custom role, retain the project's dependency on that role when removing its creation snapshot, using `depends_on` if needed. This orders project destruction before role destruction. Live project grants still prevent role deletion.

Retention is controlled by an [opper_rule](rule.md), not by this resource.

## Import

Projects are imported by UUID. Add the resource configuration first, then run:

```sh
terraform import opper_project.app 01234567-89ab-4cde-8012-3456789abcde
```

Omit `initial_access` when importing; the project metadata API cannot reconstruct creation history. Review the next plan to align the configured name and description with the existing project. A project is never automatically adopted because its name matches.

## Deletion and recovery

Destroying a project permanently deletes its runtime keys, including any keys created outside Terraform. To prevent an accidental destroy through Terraform:

```hcl
resource "opper_project" "app" {
  name = "checkout-service"

  lifecycle {
    prevent_destroy = true
  }
}
```

Opper's deletion protections still apply. For example, a signed DPA/model-access lock may require an owner action in the platform. The provider reports the refusal and keeps the resource in state.

A project deleted outside Terraform is detected during refresh and can be recreated on apply. An uncertain create result must be reconciled manually: inspect the organization's projects and import the UUID if creation succeeded. The project API has no idempotency key, so the provider does not automatically repeat creation or adopt a same-named project.
