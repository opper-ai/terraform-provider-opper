# opper_member_role

Read an existing membership's raw/effective role and admission flags. Requires
`member_roles:read` and verifies the configured organization identity.

```hcl
data "opper_member_role" "person" {
  organization_uuid = var.organization_uuid
  member_uuid       = var.member_uuid
}
```

Returns `role`, nullable `stored_role`, `owner`, `blocked`, `directory_managed`
and `change_token`. An empty effective role does not mean the stored assignment
is absent. The lookup never invites, removes, assigns or transfers ownership.
