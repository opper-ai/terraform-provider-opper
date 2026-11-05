---
page_title: "opper_custom_model Resource - Opper"
description: "Manage an OpenAI BYOK definition with write-only credentials and guarded recovery."
---

# opper_custom_model (Resource)

Manages a default-endpoint OpenAI LLM definition. Creation and rotation perform no provider probe or inference. The credential argument is write-only; the provider never writes its value or a credential hash to public or private state. Supply it through an ephemeral variable to keep its source value out of saved plans and configuration artifacts.

Requires Terraform **1.11 or later** and `custom_models:read`, `custom_models:write`, `custom_models:update`, and `custom_models:delete`. The backend must support the opt-in `POST /management/v1/custom-models/creations` and its pure receipt GET, as well as state reads, guarded PATCH/DELETE and update recovery. This resource never falls back to legacy creation. Complete that backend rollout before using it.

```hcl
terraform {
  required_version = ">= 1.11"
}

variable "openai_credential" {
  type      = string
  sensitive = true
  ephemeral = true
}

resource "opper_custom_model" "assistant" {
  name                     = "team-assistant"
  identifier               = "openai/gpt-example"
  creation_idempotency_key  = "team-assistant-create-1"
  credential_wo            = var.openai_credential
  credential_version       = 1
}
```

Replace the illustrative identifier with the intended provider model. Creation does not verify its availability, credential validity, funding or execution eligibility.

## Credentials and versions

`credential_version` is Terraform's acknowledged command sequence. It is **not an observed remote credential version**. Creation requires a positive version and nonblank `credential_wo`. Increase the version to rotate; an unchanged version ignores the credential value, including a changed ephemeral value. Metadata-only updates can omit the credential and preserve its exact stored bytes.

Omitting the version on an existing resource preserves its acknowledged sequence. An explicitly configured version must be known during planning and cannot decrease. Import starts at zero, meaning Terraform has not acknowledged any credential command for that imported definition.

No credential hashes are persisted to detect input changes. The version is the explicit trigger. A remote credential or hidden configuration edit cannot be identified from the public metadata alone.

## Creation identity and replacement

`creation_idempotency_key` is required and known before creating a new definition. Its receipt is bound to the organization and management-key identity. Configure a fresh key for every replacement or recreation, including recreation after external deletion. A completed operation key never recreates its deleted UUID. Plan-time receipt inspection refuses a reused key before replacement can delete the live model.

Changing the creation key requests resource replacement. Name and identifier changes are in-place updates. Imported definitions can omit the creation key. A creation receipt is not permission to adopt a same-named object.

The opt-in creation receipt retains the complete initial state token captured inside the creation transaction. The provider compares this original token with the first observed state before acknowledging the new definition. Matching public metadata alone cannot pass that check after a hidden edit.

## Planning, drift and destroy

Refresh and import use metadata/state GETs only. The provider retains a confirmed complete-state baseline separately from observed state, and pins that confirmed token for saved-plan updates and deletion. An intervening edit returns a conflict without refreshing the token and repeating the mutation.

`definition_changed = true` means the complete observed definition differs from the confirmed baseline. It can reflect metadata, a hidden credential/configuration edit, signing-key rotation, or an interrupted operation. It does not say which field changed. Unexplained changes require inspection and explicit import before further writes, including metadata reconciliation and deletion. This first resource detects drift and refuses unsafe reconciliation; it does not automatically restore an unknown credential.

Destroy removes only the exact reviewed UUID. It does not revoke the upstream provider credential, repair references, or immediately evict every runtime cache. Existing runtime lookups retain their 60-second propagation contract. Review references before renaming or deleting a definition.

## Interrupted operations

An update uses a deterministic operation key bound to UUID, the confirmed token, intended metadata and target credential version. It contains no credential or credential hash. A known receipt is inspected before any PATCH; a historical result is never reapplied over a later edit or rotation.

After an uncertain update, pure receipt recovery must confirm that the original result is still current before advancing the acknowledged version. A false or null result match blocks further writes. Receipt 404 cannot prove that an in-flight request will never commit. The provider retains `operation_pending`, original identity and non-secret recovery metadata; refresh never sends a PATCH. Saved plans keep their original precondition.

A deterministic key also supports recovery when a process dies before Terraform saves the update response. If the exact original receipt cannot explain the observed state, the provider stops for explicit inspection/import.

An uncertain Create always needs explicit import recovery because Terraform can taint a failed initial creation. The provider retains the UUID, or a `pending-create:` marker when it has not recovered one, and blocks replacement/deletion. Recover the original creation receipt, inspect the exact object, back up state, then remove **only that resource address** from state and import its UUID. Do not reapply with a new creation key while the original outcome is uncertain.

## Import and explicit review

```sh
terraform import opper_custom_model.assistant 11111111-1111-4111-8111-111111111111
```

Import reads the exact UUID and establishes a reviewed baseline without reading or changing the credential. Configure the imported name and identifier, omit `credential_wo`, and omit the version or set it to zero to preserve the remote credential. An explicit later positive version with a credential requests rotation.

Import cannot certify the default endpoint or empty extras from the public metadata. The backend rechecks supported configuration under its model lock and refuses unsupported updates. Importing a legacy definition does not normalize or drop its hidden configuration.

To adopt an inspected changed definition, back up state, remove only its resource address, and import the same UUID again. This resets Terraform's acknowledged command sequence to zero; it does not prove or restore a prior upstream credential.

## Computed observations

`id`, `model_provider`, `type`, `credential_present`, `created_at`, `updated_at`, and `change_token` describe stored state. The opaque change token is not an access credential or credential-validity verdict. `operation_id`, `operation_kind`, and `operation_pending` identify recovery work. Do not configure competing resources for the same model UUID.

Local tests exercise actual Terraform plans/applies and interrupted workflows. [Production Terraform acceptance](../live-acceptance.md#custom-model-lifecycle-2026-11-05) passed in the disposable Jctest organization after the creation-state backend rollout, covering create, no-op, rename, dummy credential rotation, lookups, UUID import and guarded destroy. The live run does not inject transport or concurrency faults; those paths retain separate local verification.
