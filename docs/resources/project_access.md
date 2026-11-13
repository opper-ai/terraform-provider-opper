---
page_title: "opper_project_access Resource - Opper"
description: "Manage the access policy of an existing project."
---

# opper_project_access (Resource)

Manages one existing project's complete access policy. Planning, refresh and import require `project_access:read`. Policy changes also require `project_access:write`. Ordinary `projects:*` scopes do not grant policy authority.

```hcl
resource "opper_project_access" "reporting" {
  organization_uuid = "01234567-89ab-4cde-8012-3456789abcde"
  project_uuid      = "12345678-9abc-4def-8123-456789abcdef"
  mode              = "restricted"
  role_keys         = ["viewer"]
  user_uuids        = []
}
```

Use the UUIDs of an existing organization and project. Both must be known during planning. This resource does not create projects. Creating an Organization project and restricting it in a later request leaves a visibility window; this example does not provide Restricted access from creation.

## Schema

| Attribute | Type | Behavior | Description |
|---|---|---|---|
| `organization_uuid` | String | Required | Canonical lowercase organization UUID. Must match the policy read using the management credential. Cannot change after adoption. |
| `project_uuid` | String | Required | Canonical lowercase UUID of an existing project. Cannot change after adoption. |
| `mode` | String | Required | `organization` or `restricted`. Organization mode requires both recipient sets to be empty. |
| `role_keys` | Set of strings | Required | Existing role keys in the project's organization, such as `viewer`, `developer`, `admin`, or a custom role key. Specify `[]` for none. Maximum 500 entries. |
| `user_uuids` | Set of strings | Required | UUIDs of active members in the project's organization. Specify `[]` for none. Maximum 500 entries. |
| `id` | String | Read-only | Project UUID, also used for import. |
| `revision` | Number | Read-only | Stored policy revision. Membership removal can change the policy without incrementing this value. |
| `change_token` | String | Read-only | Opaque validator covering the policy and recipient identities. |
| `reviewed_policy_token` | String | Read-only | Validator pinned by the first adoption plan or import. Preserved afterward; do not configure it. |

Role and individual grants are alternatives: a member with either grant can access the project subject to their existing action permissions. A Viewer granted project access remains a Viewer. Built-in Owners and Admins retain access automatically; an empty Restricted policy allows only those administrators.

Use one resource per project. Managing this policy from multiple Terraform states or other writers can produce conflicts.

The complete policy request must fit the API's 32 KiB body limit, even when each recipient set is within its entry limit.

## Adoption, import and drift

A first apply can adopt an Organization policy after the provider reads and pins its snapshot during planning. An already Restricted project must be imported before changes are planned:

```sh
terraform import opper_project_access.reporting 12345678-9abc-4def-8123-456789abcdef
```

Import reads the complete policy and its organization identity. Configure that organization and project, then review the next plan before applying. Unknown project identities cannot be deferred to the same apply; plan again after the project exists.

An adopted resource cannot be redirected to another organization or project. To manage another existing project, add a new resource address and import it first if it is already Restricted. Keep the original resource until the new policy apply succeeds, then remove the original block to relinquish its ownership while retaining its policy. Identity changes do not trigger automatic replacement.

Refresh detects changes to the mode and both recipient sets. A newly reviewed plan can reconcile drift. Apply uses the snapshot pinned in its saved plan; it does not replace a stale token with a fresh one. A conflict leaves the remote policy unchanged and requires a new plan after reviewing the current state. Removing a member can invalidate a plan even when the stored revision is unchanged.

Authentication, authorization, identity and read failures preserve Terraform state and stop reconciliation, including a missing or deleted project's `404`. This resource never recreates a project. Inspect the project before explicitly removing obsolete ownership with `terraform state rm`.

A failed or lost mutation response is not treated as proof of success. The provider does not retry the mutation or claim that a matching later read proves which writer changed the policy. Inspect current state before replanning; import the exact project if a first apply changed the policy but did not establish Terraform ownership.

## Destroy

**Destroy retains the remote policy and only removes Terraform ownership.** It sends no policy write and does not delete the project. To reopen access, explicitly apply `mode = "organization"` with both sets empty before removing the resource.

Retained grants can block deletion of referenced custom roles. Remove those references in a reviewed policy update before retiring a role. A soft-deleted project can also retain its role references; forgetting Terraform ownership does not clean them up.

Revoking access does not recall data, download URLs or shared secrets previously obtained by a member. Use the existing key-revocation and rotation controls where needed.

## Availability and acceptance

The policy Management API and Management MCP shipped in [Opper PR #6119](https://github.com/opper-ai/opper/pull/6119). Policy writes became generally available with [backend PR #6174](https://github.com/opper-ai/opper/pull/6174), deployed to EKS and Evroc on 2026-11-13. The rollout does not convert existing policies automatically; native policy editing retains its built-in Owner/Admin requirement. Bounded existing-project Terraform acceptance passed in EKS on 2026-11-12, including stale-plan refusal, retain-on-destroy and explicit Organization restoration. See the [acceptance record](https://github.com/opper-ai/terraform-provider-opper/blob/v0.1.0/docs/live-acceptance.md#project-access-lifecycle-2026-11-12) for the tested scope and cleanup. Native atomic Restricted creation is available separately. This Terraform resource manages existing projects only; plan again after project creation before adopting its policy. Registry publication remains separate.
