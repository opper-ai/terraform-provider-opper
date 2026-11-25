---
page_title: "opper_organization_model_list Data Source - Opper"
description: "Read the organization's advertised model-list preference."
---

# opper_organization_model_list (Data Source)

Reads the current management credential's organization UUID, compat model-list preference, whether its dynamic routes are listed and whether the list also limits calls. Requires `controls:read`. The Management API is deployed and [live acceptance](https://github.com/opper-ai/terraform-provider-opper/blob/v0.1.0/docs/live-acceptance.md#organization-model-list-2026-11-04) passed. It performs no mutation or inference.

```hcl
data "opper_organization_model_list" "current" {}

output "organization_uuid" {
  value = data.opper_organization_model_list.current.organization_uuid
}
```

| Attribute | Type | Behavior | Description |
|---|---|---|---|
| `id` | String | Read-only | Organization UUID. |
| `organization_uuid` | String | Read-only | UUID resolved from the management credential. |
| `compat_model_list` | String | Read-only | Current preference: `all`, `simplified`, `recommended` or `none` (only the organization's dynamic routes are listed). |
| `compat_model_list_dynamic_routes` | Bool | Read-only | Whether the organization's dynamic routes are listed alongside the models `compat_model_list` chooses. |
| `compat_model_list_enforced` | Bool | Read-only | Whether the organization's API keys, and those of every project that follows it, can only call what the list shows. `false` when the Opper API predates the field. |

The data source takes no arguments. Use provider aliases for different organizations. API failures and malformed responses are errors. The preference controls advertised models, and whether a model may execute only when `compat_model_list_enforced` is `true`.
