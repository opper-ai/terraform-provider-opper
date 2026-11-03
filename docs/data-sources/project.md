---
page_title: "opper_project Data Source - Opper"
description: "Look up an Opper project by UUID."
---

# opper_project (Data Source)

Reads a project in the management key's organization by UUID. It does not manage or delete the project.

```hcl
data "opper_project" "existing" {
  id = "01234567-89ab-4cde-8012-3456789abcde"
}

output "project_name" {
  value = data.opper_project.existing.name
}
```

## Schema

| Attribute | Type | Behavior | Description |
|---|---|---|---|
| `id` | String | Required | Project UUID. |
| `name` | String | Read-only | Project name. |
| `description` | String | Read-only | Project description, or an empty string if unset. |
| `created_at` | String | Read-only | Creation timestamp returned by Opper, when available. |

Requires `projects:read`. A missing project returns an error. To adopt an existing project for Terraform management, use the [project resource's import](../resources/project.md#import) instead.
