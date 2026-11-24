---
page_title: "opper_organization_model_list Resource - Opper"
description: "Manage the organization's advertised model-list preference."
---

# opper_organization_model_list (Resource)

Manages which models the organization's compat model-list endpoints advertise. By default this changes discovery results only, and model execution permissions remain controlled by rules and entitlements. With `compat_model_list_enforced = true` the list also limits which models the organization's API keys can call.

Requires the organization model-list Management API to be deployed, and a management key with `controls:read` and `controls:write`.

```hcl
resource "opper_organization_model_list" "models" {
  organization_uuid = "01234567-89ab-4cde-8012-3456789abcde"
  compat_model_list = "recommended"
}
```

To also limit calls to the listed models:

```hcl
resource "opper_organization_model_list" "limited" {
  organization_uuid          = "01234567-89ab-4cde-8012-3456789abcde"
  compat_model_list          = "recommended"
  compat_model_list_enforced = true
}
```

To list only the organization's dynamic routes:

```hcl
resource "opper_organization_model_list" "routes_only" {
  organization_uuid                = "01234567-89ab-4cde-8012-3456789abcde"
  compat_model_list                = "none"
  compat_model_list_dynamic_routes = true
}
```

## Schema

| Attribute | Type | Behavior | Description |
|---|---|---|---|
| `organization_uuid` | String | Required | Canonical lowercase organization UUID. The provider verifies it matches the management credential's organization before writing. Changing it replaces the Terraform resource, not the organization. |
| `compat_model_list` | String | Required | `all`, `simplified`, `recommended` or `none`. With `none`, only the organization's dynamic routes are listed. |
| `compat_model_list_dynamic_routes` | Bool | Optional, computed | Whether the organization's dynamic routes are listed alongside the models `compat_model_list` chooses. Opper defaults it to `true`. When omitted, the provider never sends it: the current value is kept and reported, and outside changes to it are not reverted. Must be `true` when `compat_model_list` is `none`. |
| `compat_model_list_enforced` | Bool | Optional, computed | Whether the list also limits calls. See [Limiting calls](#limiting-calls). Opper defaults it to `false`. When omitted, the provider never sends it: the current value is kept and reported, and outside changes to it are not reverted. |
| `id` | String | Read-only | Organization UUID, also used for import. |

Creation starts managing this existing organization preference and sets the explicit desired value. Use one resource per organization and separate provider aliases for different organizations. A credential/UUID mismatch fails; it never adopts a different organization. An unchanged value causes no PUT. A PUT sends `compat_model_list` and, only when configured, `compat_model_list_dynamic_routes` and `compat_model_list_enforced`, in one request.

`compat_model_list = "none"` with `compat_model_list_dynamic_routes = false` fails at plan time: it would list no models at all, and Opper refuses it. Setting `none` while the attribute is omitted and the organization has its dynamic routes off also fails, before any write; set `compat_model_list_dynamic_routes = true` in the same configuration.

## Limiting calls

With `compat_model_list_enforced = true`, the organization's API keys, and those of every project that follows the organization, can only call the LLM and embedding models the list shows, after the rules. Any other such model is refused with HTTP 403 and `details.reason` `model_list_blocked`. Guard checks, observe judges, the models a dynamic route calls, and image, video, audio and custom models are not limited. While a `recommended` list is enforced, it is frozen at the models Opper recommended when it was set, so a model Opper later stops recommending stays callable.

While the organization enforces its list, every project is limited, including projects with their own list ([`opper_project_model_list`](project_model_list.md)): their keys can only call what both lists show, and Opper refuses a project list wider than the organization's.

An Opper API from before this switch leaves `compat_model_list_enforced` out of its responses. The provider then reports `false` (that API never limits calls) and only fails when the configuration sets the attribute.

## Import and drift

```sh
terraform import opper_organization_model_list.models 01234567-89ab-4cde-8012-3456789abcde
```

Import reads the setting and verifies the credential's organization UUID. Review the next plan to align configuration with the existing value. Refresh detects outside edits. Authentication failures, missing/deleted organizations, and other API errors preserve state; this resource never creates or recreates an organization.

The API serializes writes but provides no compare-and-swap contract. Coordinate other writers of this preference. If a PUT result is uncertain, the provider makes one pure GET: it accepts the observed desired values only for the same organization, and never retries the mutation automatically. An unresolved result requires inspection and, if necessary, import to reconcile state.

## Destroy

**Destroy stops managing the preference and leaves its current value unchanged.** It does not restore `all`, delete the organization, or reset other settings. Apply the intended value before removing the resource if a reset is wanted.

This resource does not run inference, change model-access rules, manage BYOK credentials, or change billing, ownership or membership.
