---
page_title: "opper_project_model_list Data Source - Opper"
description: "Read whether a project has its own compat model list."
---

# opper_project_model_list (Data Source)

Reads whether a project has its own compat model list and, if so, what it is. Requires `controls:read`. It performs no mutation or inference.

```hcl
data "opper_project_model_list" "support" {
  project_uuid = "01234567-89ab-4cde-8012-3456789abcde"
}
```

| Attribute | Type | Behavior | Description |
|---|---|---|---|
| `project_uuid` | String | Required | Canonical lowercase project UUID. |
| `id` | String | Read-only | Project UUID. |
| `follows_organization` | Bool | Read-only | `true` when the project has no list of its own and follows the organization's ([`opper_organization_model_list`](organization_model_list.md)). The attributes below are then null. |
| `compat_model_list` | String | Read-only | The project's own list: `all`, `simplified`, `recommended` or `none` (only the organization's dynamic routes are listed). |
| `compat_model_list_dynamic_routes` | Bool | Read-only | Whether the organization's dynamic routes are listed to the project's API keys. |
| `compat_model_list_enforced` | Bool | Read-only | Whether the project limits its own API keys to what its list shows. While the organization enforces its list, every project is limited whatever this says. |

The Management API does not tell a project that follows the organization apart from a UUID that is not a live project of the credential's organization: both report `follows_organization = true`. Use the [`opper_project`](project.md) data source to check that a project exists. API failures and malformed responses are errors.
