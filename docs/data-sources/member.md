---
page_title: "opper_member Data Source - Opper"
description: "Look up an existing organization member."
---

# opper_member (Data Source)

Looks up one existing member of the management key's organization. Requires `members:read`. This data source only reads members; it never invites people, changes roles or removes memberships.

```hcl
data "opper_member" "developer" {
  email = "developer@example.com"
}

resource "opper_rule" "developer_budget" {
  kind    = "comply"
  name    = "Developer monthly budget"
  enabled = true

  scope = {
    type         = "members"
    member_uuids = [data.opper_member.developer.id]
  }

  config_json = jsonencode({
    type                = "budget"
    monthly_limit_cents = 10000
  })
}
```

The example rule additionally requires `controls:read`, `controls:write` and the appropriate Opper plan. Use the lookup's `id` anywhere a rule expects a member UUID.

## Schema

| Attribute | Type | Behavior | Description |
|---|---|---|---|
| `uuid` | String | Optional selector | Exact canonical lowercase user UUID. Set exactly one of `uuid` or `email`. |
| `email` | String | Optional selector | Exact email match, case-insensitive. Leading/trailing whitespace and empty values are rejected. Configured case is preserved. |
| `id` | String | Read-only | Matched member's user UUID. |
| `member` | Object | Read-only | Member fields listed below, as returned by Opper. |

The `member` object contains `uuid`, `email`, `name`, `role`, `is_owner` and `created_at`. Email and name can be empty strings. Role can be a built-in or custom role name, or empty for a membership that grants no role, such as a blocked member. `created_at` is nullable and represents **Opper account creation**, not the date the person joined this organization.

A missing member or multiple matching emails cause an error. The data source never chooses the first matching person. Use a UUID when an email is ambiguous or when identity should survive an email change. Refresh re-reads current metadata; if the member leaves the organization, the lookup fails rather than returning stale details. API failures, including missing `members:read`, remain errors.

This lookup reads the full organization member list through the current public API. Member details are stored in Terraform state. Use separate provider aliases and organization credentials for lookups in different organizations.
