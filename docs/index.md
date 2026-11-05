---
page_title: "Opper Provider"
description: "Manage Opper configuration through the Management API."
---

# Opper provider

The Opper provider manages organization configuration through the [Management API](https://docs.opper.ai/control-plane/management-api). The implementation supports projects, project webhook URLs, organization model-list preferences (after backend rollout), project-owned runtime keys, rules, dynamic-route drafts and conditional live deployments. Source is maintained on [GitHub](https://github.com/opper-ai/terraform-provider-opper); Terraform Registry releases are not yet published.

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
| Custom-role resource | `roles:read`, `roles:write`, `roles:delete`; Enterprise and conditional roles backend rollout |
| Role lookup/catalog | `roles:read` |
| Member-role resource | `member_roles:read`, `member_roles:write`, `roles:read`; conditional roles backend rollout |
| Member-role lookup | `member_roles:read` |
| Custom-model resource | `custom_models:read`, `custom_models:write`, `custom_models:update`, `custom_models:delete`; Terraform 1.11+ and creation-state backend rollout |
| Custom-model lookup and inventory | `custom_models:read` |
| Project resource | `projects:read`, `projects:write`, `projects:delete` |
| Project lookup | `projects:read` |
| Member lookup and member list data sources | `members:read` |
| Organization model-list resource / data source | `controls:read`, `controls:write` / `controls:read`; requires backend rollout |
| Runtime key resource | `apikeys:read`, `apikeys:write` |
| Project webhook resource | `apikeys:read`, `apikeys:write` |
| Rule resource | `controls:read`, `controls:write` |
| Dynamic-route draft and deployment resources | `dynamic_routes:read`, `dynamic_routes:write` |

## Schema

| Argument | Type | Required | Description |
|---|---|---|---|
| `management_api_key` | String, sensitive | No | Management credential. Falls back to `OPPER_MANAGEMENT_API_KEY`. A credential must be supplied by one of these methods. |
| `base_url` | String | No | API origin. Falls back to `OPPER_BASE_URL`, then `https://api.opper.ai`. HTTPS is required except for loopback HTTP used in local tests. |

For multiple organizations, use provider aliases with separate management credentials. `base_url` changes the API origin, not the selected organization.

See the [project resource](resources/project.md), [project webhooks](resources/project_webhook.md), [runtime keys](resources/api_key.md), [rules](resources/rule.md), [dynamic-route drafts](resources/dynamic_route.md), [live deployments](resources/dynamic_route_deployment.md), [project lookup](data-sources/project.md), [member lookup](data-sources/member.md), [member list](data-sources/members.md), [local development instructions](../README.md#develop-locally) and [coverage plan](coverage.md).
