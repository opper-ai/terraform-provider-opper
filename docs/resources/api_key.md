---
page_title: "opper_api_key Resource - Opper"
description: "Manage a project-owned Opper runtime API key."
---

# opper_api_key (Resource)

Creates and manages a project-owned runtime API key in the management key's organization. Project keys survive removal of the member who created them. The provider always requests `kind = "project"`; personal runtime keys are not managed by this resource.

```hcl
resource "opper_project" "app" {
  name = "checkout-service"
}

resource "opper_api_key" "app" {
  project_id = opper_project.app.id
  name       = "checkout-runtime"
}

output "runtime_api_key" {
  value     = opper_api_key.app.secret
  sensitive = true
}
```

The management credential needs `apikeys:read` and `apikeys:write`. Managing the parent project separately also requires its project scopes.

## Schema

| Attribute | Type | Behavior | Description |
|---|---|---|---|
| `project_id` | String | Required, replacement | UUID of the project that owns the key. |
| `name` | String | Required, replacement | Name identifying the runtime key. Renaming creates a new key and revokes the old one. |
| `idempotency_key` | String | Optional, replacement | Stable identifier for one create operation: 1–128 letters, digits, dots, underscores, colons or hyphens. Change it for every replacement. |
| `rotation` | String | Optional, replacement | User-managed rotation marker. Changing it replaces the key. This does not schedule rotation. |
| `allowed_ips` | Set of strings | Optional, computed | Public source IPs or CIDRs allowed to use the runtime key. Updates in place without rotating its ID or secret. Omit or set null to preserve an existing key's observed list; replacement requires an explicit list. Set `[]` to clear restrictions. |
| `id` | String | Read-only | Decimal runtime API key ID. |
| `kind` | String | Read-only | Always `project`. |
| `prefix` | String | Read-only | Non-secret key prefix returned by Opper. |
| `created_at` | String | Read-only | Creation timestamp returned by Opper, when available. |
| `secret` | String | Read-only, sensitive | Full runtime credential, returned once during creation. Stored in Terraform state. |
| `secret_available` | Boolean | Read-only | Whether the provider's state has the one-time secret. False after import or a creation response without a recoverable secret. |

## Secret handling

**The computed `secret` is stored in Terraform state.** Marking it sensitive hides it in ordinary Terraform output; it does not encrypt state or omit the value from saved plans. Use an access-controlled encrypted state backend, exclude state files from Git, and pass the secret only to destinations that need it. Commands such as `terraform output -raw` or `terraform show -json` can expose sensitive values.

The API never returns a key's full secret when listing keys. Refresh preserves a secret already captured in state. Import cannot recover it and sets `secret_available = false`.

## IP allowlist

`allowed_ips` is an unreleased provider feature and is not included in v0.1.0. The backend contract shipped in [Opper PR #6120](https://github.com/opper-ai/opper/pull/6120) and is advertised by both production hosts. A provider build containing this attribute is required.

Supply the public egress addresses used by your application:

```hcl
variable "runtime_egress_ips" {
  type        = set(string)
  description = "Public runtime-client source IPs or CIDRs."
}

resource "opper_api_key" "restricted" {
  project_id  = opper_project.app.id
  name        = "restricted-runtime"
  allowed_ips = var.runtime_egress_ips
}
```

See the [standalone allowlist example](https://github.com/opper-ai/terraform-provider-opper/tree/main/examples/api-key-allowlist) for an existing project. Use the actual public egress addresses of your runtime clients. At most 100 entries are allowed. IPv4 prefixes must be `/8` or narrower; IPv6 prefixes must be `/16` or narrower.

Creation stores the requested list atomically with the key. Later list changes use an in-place update and preserve the key ID, prefix and existing one-time secret. They do not require a new `idempotency_key`. The same `apikeys:read` and `apikeys:write` scopes apply.

Omitting `allowed_ips`, or setting it to null, preserves an existing or imported key's list during refresh and in-place updates. It does not carry that computed list into a replacement. For a new key with no prior state, omission uses the server default. To remove all IP restrictions, explicitly configure `allowed_ips = []`. Removing the argument is not a request to clear it.

The provider trims whitespace, masks CIDR host bits, deduplicates equivalent entries, and normalizes host prefixes, IPv4-mapped addresses and IPv6 spellings. Configured spellings remain in state when semantically equivalent to the server list, avoiding formatting-only drift. Import uses canonical server values. Refresh detects real list changes; an explicitly configured list reconciles them on apply.

Before creating a restricted key, the provider verifies advertised OpenAPI support. An unsupported server is refused before the creation POST. Responses must confirm the requested restriction; a refusal or unconfirmed response is an error. A failed allowlist update does not trigger replacement or an unrestricted fallback. Inspect the remote key after an uncertain creation outcome before retrying.

Runtime authentication caches can delay enforcement of allowlist changes by up to one minute. An accepted update is not a promise of immediate enforcement on every request.

Live allowlist acceptance has not run. The baseline runtime-key acceptance record predates this feature and does not verify allowlist creation, updates or runtime enforcement.

## Replacement and rotation

**Keep `allowed_ips` explicitly configured when replacing a restricted key.** Supply a fully known list containing the current restrictions or the new desired policy; use `[]` only to intentionally remove all restrictions. This applies to rotation, project/name/idempotency changes, manual `-replace`, taint replacement and recovery after a failed creation.

Terraform discards computed values and private state when applying a replacement, so an omitted/null list is not a preservation guarantee. For normal project, name, rotation or idempotency changes on a key with a nonempty list, the provider rejects an omitted or unknown replacement policy during planning before any mutation. Terraform Core's manual replacement and taint decisions are outside that field-change guard; configure the replacement list explicitly before those operations too.

Only the IP allowlist supports in-place updates. Changes to `name`, `project_id`, `idempotency_key` or `rotation` replace the key. By default Terraform revokes the old key before creating its replacement. Where overlapping credentials are appropriate, use:

```hcl
resource "opper_api_key" "app" {
  project_id = opper_project.app.id
  name       = "checkout-runtime"
  rotation   = "2026-11"

  lifecycle {
    create_before_destroy = true
  }
}
```

This creates the replacement before revocation. Applications must also receive and begin using the replacement secret; Terraform cannot verify an application's rollout automatically. Review downstream dependencies to ensure the old key is revoked at the intended point.

If `idempotency_key` is configured, **change it for every replacement**, including replacements caused by `rotation`, a name change, an explicit `-replace`, or an external deletion. Reusing the old token can replay the previous key's metadata without its secret, or return a conflict after that key has been deleted. An idempotency token identifies one create operation, not a key name forever.

For planned project, name or rotation changes, the provider rejects an unchanged receipt before Terraform can revoke the existing key. The new receipt must be known during planning, or the optional argument can be removed. An explicit `-replace` or an externally deleted key has no corresponding configuration change for this guard to detect: change the receipt yourself before requesting recreation.

## Import

Add matching resource configuration, then import by `<project_uuid>/<key_id>`:

```sh
terraform import opper_api_key.app 01234567-89ab-4cde-8012-3456789abcde/42
```

Only project-owned keys may be imported. The imported key's metadata, including its canonical allowlist, is refreshed; its secret remains unavailable, and `secret_available` is false. The provider never automatically rotates an imported key to retrieve a secret. Existing consumers can continue using the credential they already have.

## Errors and recovery

The provider never automatically retries key creation or revokes a key merely because its one-time secret is unavailable.

If the API returns known key metadata without a secret, the provider keeps the ID in state and reports an error explaining recovery. Explicitly configure the intended `allowed_ips` for a restricted replacement and choose a new `idempotency_key`, if configured, before replacing the key to capture a new secret. Keeping the ID allows Terraform to revoke the unusable credential during replacement or destroy.

A creation response that does not confirm the requested allowlist is an error, and Terraform may mark the retained resource tainted. Inspect the remote key before retrying. Configure the replacement allowlist explicitly and use a fresh `idempotency_key` if one is configured; taint does not make an old receipt safe to reuse.

After an uncertain transport failure, a key may have been minted even though Terraform did not receive its ID or secret. Inspect the project's runtime keys before retrying. Reconcile a key that was created by importing its metadata or explicitly revoking it, then create a replacement with a new operation token. A blind retry without an idempotency token can mint an additional unmanaged key; replay with the same token cannot return the original secret.

A key revoked outside Terraform is detected during refresh and recreated on apply. Authorization and server errors are reported without removing the key from state. Destroy revokes only the managed key; deleting its parent project separately revokes every key in that project.
