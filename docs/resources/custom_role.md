# opper_custom_role

Manage one Enterprise custom role through the Management API. Requires
`roles:read`, `roles:write` and `roles:delete`. Built-in roles are lookup-only.

```hcl
resource "opper_custom_role" "reporting" {
  organization_uuid        = var.organization_uuid
  display_name             = "Reporting"
  permissions              = ["usage:read"]
  priority                 = 150
  creation_idempotency_key = "reporting-20261105-1"
}
```

`name` is the immutable generated `custom_<32 lowercase hex>` identity. Renaming
`display_name` does not replace the role. Labels must be trimmed, nonblank,
1–64 characters, distinct from built-in labels and unique in the organization.
`permissions` is a set of 1–50 declared grants within the admin ceiling; owner-only
and unknown grants are refused. `effective_permissions` includes implied grants.
Priority is 1–299, default 150. Changing priority invalidates directory role
freshness and schedules reconciliation, so directory access can temporarily wait
for a refresh.

Supply a fresh, stable `creation_idempotency_key` for every creation/replacement.
An original receipt never recreates a deleted role. Replacement with an already
completed key is refused before deletion. Imported definitions can omit the key.
`organization_uuid` must be known at plan time. The provider verifies it against
the management key's organization before allowing a replacement.

Updates and deletion use the confirmed complete-definition validator. Outside
edits set `definition_changed`; inspect and explicitly import before further
mutation. Saved plans never fetch a fresh validator to force a write. Deletion
refuses members, SSO mappings, project policies and other live role references;
it never cascades assignments or replaces them with implicit defaults.

`operation_id`, `operation_kind` and `operation_pending` identify interrupted
work. An uncertain update recovers by pure receipt reads and confirms the exact
original result. A later edit, unknown result or missing receipt requires review;
the provider never repeats a mutation automatically. Uncertain creation requires
inspection and explicit import, avoiding Terraform's failed-create replacement
from deleting a recovered role. Back up state, remove only the affected resource
address from state, then import the confirmed original role name.

```sh
terraform import opper_custom_role.reporting custom_0123456789abcdef0123456789abcdef
```

Import reads the calling credential's organization and does not modify grants.
Configure that same `organization_uuid`; a mismatch prevents adoption/writes.
Do not configure multiple resources for the same role. This resource requires
the conditional roles backend; local implementation is not a published release
or production acceptance result.
