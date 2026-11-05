# opper_role

Read one immutable role name in the expected organization. Requires
`roles:read`. Built-in `admin`, `developer` and `viewer` roles and custom role
keys are supported; owner is transferred separately and is not assignable.

```hcl
data "opper_role" "viewer" {
  organization_uuid = var.organization_uuid
  name              = "viewer"
}
```

Returns `display_name`, declared `permissions`, `effective_permissions`,
`priority`, `custom` and `change_token`. Use the token to pin a reviewed
assignment or explicit fallback definition. No grants or membership are changed.
