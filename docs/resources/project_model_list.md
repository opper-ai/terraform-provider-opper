---
page_title: "opper_project_model_list Resource - Opper"
description: "Give a project its own compat model list."
---

# opper_project_model_list (Resource)

Gives one project its own compat model list. The project's list replaces the organization's ([`opper_organization_model_list`](organization_model_list.md)) for compat model-list responses to the project's API keys, and with `compat_model_list_enforced = true` it also limits which models those keys can call. A project without this resource follows the organization's list, including its enforcement.

The management key needs `controls:read` and `controls:write`. Creating or deleting the parent project separately requires the project scopes.

```hcl
resource "opper_project_model_list" "support" {
  project_uuid               = opper_project.support.id
  compat_model_list          = "recommended"
  compat_model_list_enforced = true
}
```

## Schema

| Attribute | Type | Behavior | Description |
|---|---|---|---|
| `project_uuid` | String | Required | Canonical lowercase UUID of a live project in the management credential's organization. Changing it replaces the resource: the old project follows the organization again and the new one gets the list. |
| `compat_model_list` | String | Required | `all`, `simplified`, `recommended` or `none`. With `none`, only the organization's dynamic routes are listed. |
| `compat_model_list_dynamic_routes` | Bool | Optional, computed | Whether the organization's dynamic routes are listed to the project's keys. Defaults to `true`. Must be `true` when `compat_model_list` is `none`. |
| `compat_model_list_enforced` | Bool | Optional, computed | Whether the project's API keys can only call what its list shows. See [Limiting calls](#limiting-calls). When omitted, the provider never sends it: the project keeps its current value (`false` for a project that followed the organization), the value is reported, and outside changes to it are not reverted. |
| `id` | String | Read-only | Project UUID, also used for import. |

Every create and update sends one PUT with `compat_model_list`, `compat_model_list_dynamic_routes` and, only when configured, `compat_model_list_enforced`. Repeating the current value makes no audit event in Opper.

`compat_model_list = "none"` with `compat_model_list_dynamic_routes = false` fails at plan time: it would list no models at all, and Opper refuses it.

## Limiting calls

With `compat_model_list_enforced = true`, the project's API keys can only call the LLM and embedding models its list shows, after the rules. Any other such model is refused with HTTP 403 and `details.reason` `model_list_blocked`. Guard checks, observe judges, the models a dynamic route calls, and image, video, audio and custom models are not limited.

While the organization enforces its own list, every project is limited whatever this attribute says: the project's keys can only call what both lists show. Opper then refuses a project list wider than the organization's (a broader `compat_model_list`, or dynamic routes the organization does not list) with HTTP 400, and the provider explains this in the error.

## Import and drift

Creation refuses a project that already has its own list, even one that matches the configuration, so Terraform never silently takes over a list set in the platform. Import it instead:

```sh
terraform import opper_project_model_list.support 01234567-89ab-4cde-8012-3456789abcde
```

Import fails for a project that follows the organization. Review the next plan before applying.

Refresh reads the organization's list of projects with their own list. Outside changes to managed values are corrected on the next apply. If the project's list is removed outside Terraform, or the project is deleted, the resource leaves state and the next apply sets the list again (failing with HTTP 404 for a deleted project). Authentication and other API errors preserve state.

The API provides no compare-and-swap contract; coordinate other writers of the project's list. If a PUT result is uncertain, the provider makes one pure read and accepts the result only when it shows the desired values. It never retries the PUT automatically.

## Destroy

**Destroy removes the project's own list**, so the project follows the organization's list, and its enforcement, again. Destroying the list of a project that was deleted meanwhile succeeds. Destroy does not delete the project, its keys or the organization's list.
