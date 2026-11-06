# opper_member_role

Manage the role of an **existing** organization member. Requires
`member_roles:read`, `member_roles:write` and `roles:read`. Built-in assignments
require Control Plane or Enterprise; custom roles require Enterprise.
The resource never invites/removes members or transfers ownership.

```hcl
data "opper_role" "viewer" {
  organization_uuid = var.organization_uuid
  name              = "viewer"
}

resource "opper_member_role" "reporter" {
  organization_uuid         = var.organization_uuid
  member_uuid               = var.member_uuid
  role                      = opper_custom_role.reporting.name
  role_change_token         = opper_custom_role.reporting.change_token
  destroy_behavior          = "assign_role"
  destroy_role              = "viewer"
  destroy_role_change_token = data.opper_role.viewer.change_token
}
```

The organization/member identity must be known during planning. The initial
plan pins the existing assignment; the destination definition is explicitly
pinned through `role_change_token`. Reference a managed role or lookup token so
the permission definition reviewed in the plan is the one assigned. The provider
never substitutes a just-in-time definition token. A custom-role update and its
assignment can compose in one apply; updating the destination definition token
for an already held role confirms the new definition without changing membership.

Choose a destroy disposition for every resource:

- `retain` relinquishes Terraform management and sends no assignment mutation.
  Omit both fallback attributes. A retained custom role can still prevent its
  separate role resource from being deleted.
- `assign_role` assigns the explicitly configured built-in `destroy_role`
  (`admin`, `developer` or `viewer`) using its pinned
  `destroy_role_change_token` and the confirmed assignment validator. The
  fallback may increase or decrease access; review it explicitly. It is never
  an inferred historical role. Referencing another custom role as fallback is
  forbidden, allowing a managed custom role to be cleaned up independently.

The provider never clears an assignment: legacy null assignments can confer
admin access. Owner, blocked and directory-managed assignments are protected.
Outside changes, removal/rejoin and stale saved plans cannot become fresh
authority for reassignment or fallback; inspect/import before further writes.
Retain can safely relinquish management without changing the member.

```sh
terraform import opper_member_role.reporter ORGANIZATION_UUID/MEMBER_UUID
```

Import verifies the organization and observes the current assignment. It leaves
the destroy disposition unknown. Configure the explicit disposition and apply
before attempting destroy; that configuration-only step does not change a role.
Import grants no authority to restore an unknown previous assignment.

Uncertain updates/fallbacks retain the original receipt identity and use pure
recovery reads; no PUT retry or fresh validator is used. Uncertain initial
assignment requires explicit inspection/import. `operation_pending` blocks
further mutations. `reviewed_assignment_token` records the initial plan's
source validator; `change_token` describes the observed complete assignment.
These are validators, not credentials. One resource should own each membership.

Local implementation requires the new conditional roles backend. No live role
change, invitation, inference or provider release is implied by these examples.
