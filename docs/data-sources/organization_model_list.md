---
page_title: "opper_organization_model_list Data Source - Opper"
description: "Read the organization's advertised model-list preference."
---

# opper_organization_model_list (Data Source)

Reads the current management credential's organization UUID and compat model-list preference. Requires `controls:read` and the organization model-list Management API rollout. It performs no mutation or inference.

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
| `compat_model_list` | String | Read-only | Current preference: `all`, `simplified` or `recommended`. |

The data source takes no arguments. Use provider aliases for different organizations. API failures and malformed responses are errors. The preference controls advertised models, not whether a model may execute.
