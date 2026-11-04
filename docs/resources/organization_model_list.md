---
page_title: "opper_organization_model_list Resource - Opper"
description: "Manage the organization's advertised model-list preference."
---

# opper_organization_model_list (Resource)

Manages which models the organization's compat model-list endpoints advertise. This preference changes discovery results; model execution permissions remain controlled by rules and entitlements.

Requires the organization model-list Management API to be deployed, and a management key with `controls:read` and `controls:write`.

```hcl
resource "opper_organization_model_list" "models" {
  organization_uuid = "01234567-89ab-4cde-8012-3456789abcde"
  compat_model_list = "recommended"
}
```

## Schema

| Attribute | Type | Behavior | Description |
|---|---|---|---|
| `organization_uuid` | String | Required | Canonical lowercase organization UUID. The provider verifies it matches the management credential's organization before writing. Changing it replaces the Terraform resource, not the organization. |
| `compat_model_list` | String | Required | `all`, `simplified` or `recommended`. |
| `id` | String | Read-only | Organization UUID, also used for import. |

Creation starts managing this existing organization preference and sets the explicit desired value. Use one resource per organization and separate provider aliases for different organizations. A credential/UUID mismatch fails; it never adopts a different organization. An unchanged value causes no PUT.

## Import and drift

```sh
terraform import opper_organization_model_list.models 01234567-89ab-4cde-8012-3456789abcde
```

Import reads the setting and verifies the credential's organization UUID. Review the next plan to align configuration with the existing value. Refresh detects outside edits. Authentication failures, missing/deleted organizations, and other API errors preserve state; this resource never creates or recreates an organization.

The API serializes writes but provides no compare-and-swap contract. Coordinate other writers of this preference. If a PUT result is uncertain, the provider makes one pure GET: it accepts the observed desired value only for the same organization, and never retries the mutation automatically. An unresolved result requires inspection and, if necessary, import to reconcile state.

## Destroy

**Destroy stops managing the preference and leaves its current value unchanged.** It does not restore `all`, delete the organization, or reset other settings. Apply the intended value before removing the resource if a reset is wanted.

This resource does not run inference, change model-access rules, manage BYOK credentials, or change billing, ownership or membership.
