---
page_title: "opper_members Data Source - Opper"
description: "Read an organization's existing members."
---

# opper_members (Data Source)

Reads the members of the management key's organization. Requires `members:read`. It performs no invitation, removal or role changes.

```hcl
data "opper_members" "team" {}

output "member_ids" {
  value = [for member in data.opper_members.team.members : member.uuid]
}
```

## Schema

| Attribute | Type | Behavior | Description |
|---|---|---|---|
| `id` | String | Read-only | Constant `members`, scoped by the provider's organization credential. |
| `members` | List of objects | Read-only | All members, sorted by user UUID for stable ordering. An empty organization returns an empty list. |

Each member object contains `uuid`, `email`, `name`, `role`, `is_owner` and nullable `created_at`, with the same meanings as [`opper_member`](member.md). The timestamp is account creation, not organization join time. Member details are stored in Terraform state.

Refresh includes current membership and metadata changes. API errors and malformed or incomplete member lists are reported as errors, not empty results. Use separate provider aliases and credentials for different organizations. For one person, prefer [`opper_member`](member.md), which requires exactly one unambiguous match.
