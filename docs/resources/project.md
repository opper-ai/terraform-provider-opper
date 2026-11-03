---
page_title: "opper_project Resource - Opper"
description: "Manage an Opper project."
---

# opper_project (Resource)

Creates and manages a project in the management key's organization.

```hcl
resource "opper_project" "app" {
  name        = "checkout-service"
  description = "Checkout model calls"
}
```

## Schema

| Attribute | Type | Behavior | Description |
|---|---|---|---|
| `name` | String | Required | Non-empty project name, unique within the organization. Leading and trailing whitespace are rejected. Changing it updates the project in place. |
| `description` | String | Optional | Project description. Defaults to an empty string. Removing it clears the previous description. |
| `id` | String | Read-only | Project UUID. |
| `created_at` | String | Read-only | Creation timestamp returned by Opper, when available. |

Retention is controlled by an [opper_rule](rule.md), not by this resource.

## Import

Projects are imported by UUID. Add the resource configuration first, then run:

```sh
terraform import opper_project.app 01234567-89ab-4cde-8012-3456789abcde
```

Review the next plan to align the configured name and description with the existing project. A project is never automatically adopted because its name matches.

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
