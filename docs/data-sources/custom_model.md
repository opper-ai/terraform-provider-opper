---
page_title: "opper_custom_model Data Source - Opper"
description: "Read one Opper custom model's stored metadata by UUID."
---

# opper_custom_model (Data Source)

Reads the eight public metadata fields for an existing custom-model definition in
the provider credential's organization. Requires only `custom_models:read`.

```hcl
data "opper_custom_model" "support" {
  id = "11111111-1111-4111-8111-111111111111"
}

output "support_model_name" {
  value = data.opper_custom_model.support.name
}
```

`id` is required and must be the exact canonical nonzero lowercase UUID. Names are
not selectors. A missing or foreign UUID returns an error; this data source never
adopts a same-named replacement or creates a definition.

| Attribute | Type | Behavior |
| --- | --- | --- |
| `id` | String | Required immutable custom-model UUID selector. |
| `name` | String | Read-only stored name. |
| `identifier` | String | Read-only stored provider/model identifier. |
| `model_provider` | String | Read-only declared provider; null on some legacy definitions. |
| `type` | String | Read-only declared model type; null on some legacy definitions. |
| `credential_present` | Boolean | Read-only indication that a nonempty credential is stored. |
| `created_at` | String | Read-only creation timestamp. |
| `updated_at` | String | Read-only latest stored update timestamp. |

The lookup does not return credentials, connection extras or opaque change tokens.
It does not decrypt or test a credential, run inference or verify that the model
can execute. `credential_present` is not a credential-validity verdict. Metadata
can describe legacy configurations beyond the subset accepted by management
writes; reading them does not normalize or alter their configuration.

Use [`opper_custom_models`](custom_models.md) to read the complete metadata inventory.
