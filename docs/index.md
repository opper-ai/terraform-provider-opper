---
page_title: "Opper Provider"
description: "Manage Opper configuration through the Management API."
---

# Opper provider

The Opper provider manages organization configuration through the [Management API](https://docs.opper.ai/control-plane/management-api). The implementation supports projects, project-owned runtime keys, rules and dynamic-route drafts. Source is maintained on [GitHub](https://github.com/opper-ai/terraform-provider-opper); Terraform Registry releases are not yet published.

```hcl
terraform {
  required_providers {
    opper = {
      source = "opper-ai/opper"
    }
  }
}

provider "opper" {}
```

Supply a management API key using `OPPER_MANAGEMENT_API_KEY`. The key determines the organization; a runtime API key is not accepted. Grant the scopes for the resources in your configuration:

| Surface | Required scopes |
|---|---|
| Project resource | `projects:read`, `projects:write`, `projects:delete` |
| Project lookup | `projects:read` |
| Runtime key resource | `apikeys:read`, `apikeys:write` |
| Rule resource | `controls:read`, `controls:write` |
| Dynamic-route draft resource | `dynamic_routes:read`, `dynamic_routes:write` |

## Schema

| Argument | Type | Required | Description |
|---|---|---|---|
| `management_api_key` | String, sensitive | No | Management credential. Falls back to `OPPER_MANAGEMENT_API_KEY`. A credential must be supplied by one of these methods. |
| `base_url` | String | No | API origin. Falls back to `OPPER_BASE_URL`, then `https://api.opper.ai`. HTTPS is required except for loopback HTTP used in local tests. |

For multiple organizations, use provider aliases with separate management credentials. `base_url` changes the API origin, not the selected organization.

See the [project resource](resources/project.md), [runtime keys](resources/api_key.md), [rules](resources/rule.md), [dynamic-route drafts](resources/dynamic_route.md), [project data source](data-sources/project.md), [local development instructions](../README.md#develop-locally) and [coverage plan](coverage.md).
