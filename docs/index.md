---
page_title: "Opper Provider"
description: "Manage Opper configuration through the Management API."
---

# Opper provider

The Opper provider manages organization configuration through the [Management API](https://docs.opper.ai/control-plane/management-api). The implementation supports projects and access policies, project webhooks and runtime keys, organization model-list preferences, rules, dynamic-route drafts and deployments, BYOK custom models, custom roles and existing member-role assignments. Source is maintained on [GitHub](https://github.com/opper-ai/terraform-provider-opper).

Use Terraform 1.11 or later for the full provider surface, including write-only custom-model credentials. Use the configuration below and run `terraform init` to install the selected Registry version. For local builds, follow the [development override instructions](https://github.com/opper-ai/terraform-provider-opper/blob/main/README.md#develop-locally).

```hcl
terraform {
  required_version = ">= 1.11"

  required_providers {
    opper = {
      source  = "opper-ai/opper"
      version = "~> 0.2.0"
    }
  }
}

provider "opper" {}
```

Supply a management API key using `OPPER_MANAGEMENT_API_KEY`. The key determines the organization; a runtime API key is not accepted. Grant the scopes for the resources in your configuration:

| Surface | Required scopes |
|---|---|
| Custom-role resource | `roles:read`, `roles:write`, `roles:delete`; Enterprise required |
| Role lookup/catalog | `roles:read` |
| Member-role resource | `member_roles:read`, `member_roles:write`, `roles:read` |
| Member-role lookup | `member_roles:read` |
| Custom-model resource | `custom_models:read`, `custom_models:write`, `custom_models:update`, `custom_models:delete`; Terraform 1.11+ |
| Custom-model lookup and inventory | `custom_models:read` |
| Project resource | `projects:read`, `projects:write`, `projects:delete` |
| Project lookup | `projects:read` |
| Existing-project access policy | `project_access:read`, `project_access:write` |
| Member lookup and member list data sources | `members:read` |
| Organization model-list resource / data source | `controls:read`, `controls:write` / `controls:read` |
| Runtime key resource | `apikeys:read`, `apikeys:write` |
| Project webhook resource | `apikeys:read`, `apikeys:write` |
| Rule resource | `controls:read`, `controls:write` |
| Dynamic-route draft and deployment resources | `dynamic_routes:read`, `dynamic_routes:write` |

The baseline APIs required by these resources are deployed. Introduced in v0.2.0, the `opper_project.initial_access` attribute uses the dedicated `POST /management/v1/projects/with-access` endpoint, now deployed in both regions, and additionally requires `project_access:write`. It commits a new project's initial policy atomically and records creation history; use `opper_project_access` for ongoing policy management.

Existing-project policy writes are generally available following [backend PR #6174](https://github.com/opper-ai/opper/pull/6174), deployed on 2026-11-13. Existing policies are not converted automatically. `opper_project_access` retains the policy on destroy. Creating a project without `initial_access` and restricting it afterward does not make it private from creation.

Runtime-key [`allowed_ips`](resources/api_key.md#ip-allowlist), also introduced in v0.2.0, supports in-place restriction updates. Existing-key omission preserves the observed list; restricted replacement requires an explicitly configured list. Runtime changes can take up to one minute to affect requests.

## Schema

| Argument | Type | Required | Description |
|---|---|---|---|
| `management_api_key` | String, sensitive | No | Management credential. Falls back to `OPPER_MANAGEMENT_API_KEY`. A credential must be supplied by one of these methods. |
| `base_url` | String | No | API origin. Falls back to `OPPER_BASE_URL`, then `https://api.opper.ai`. HTTPS is required except for loopback HTTP used in local tests. |

For multiple organizations, use provider aliases with separate management credentials. `base_url` changes the API origin, not the selected organization.

See the [project resource](resources/project.md), [project access policies](resources/project_access.md), [project webhooks](resources/project_webhook.md), [runtime keys](resources/api_key.md), [rules](resources/rule.md), [dynamic-route drafts](resources/dynamic_route.md), [live deployments](resources/dynamic_route_deployment.md), [project lookup](data-sources/project.md), [member lookup](data-sources/member.md), [member list](data-sources/members.md), [local development instructions](https://github.com/opper-ai/terraform-provider-opper/blob/main/README.md#develop-locally) and [coverage plan](https://github.com/opper-ai/terraform-provider-opper/blob/main/docs/coverage.md).
