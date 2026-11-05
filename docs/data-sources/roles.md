# opper_roles

Read the organization's role catalog and admin grant ceiling with `roles:read`.

```hcl
data "opper_roles" "all" {
  organization_uuid = var.organization_uuid
}
```

`roles` is ordered by immutable name. Each entry includes organization UUID,
name, nullable display name, declared/effective permission sets, priority,
custom status and its definition validator. `permissions` is the admin ceiling
accepted for custom definitions. Organization identity is verified; a failed
or malformed read is an error, never an empty catalog or deletion instruction.
