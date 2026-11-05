---
page_title: "opper_custom_models Data Source - Opper"
description: "Read the complete Opper custom-model metadata inventory."
---

# opper_custom_models (Data Source)

Reads all custom-model metadata in the provider credential's organization. It
requires only `custom_models:read` and accepts no selectors or mutation arguments.

```hcl
data "opper_custom_models" "all" {}

output "custom_model_ids" {
  value = [for model in data.opper_custom_models.all.models : model.id]
}
```

`id` is the constant string `custom-models`. `models` is a list sorted by immutable
UUID, including an empty list when the organization has no definitions. The
provider exhausts pagination and refuses incomplete or inconsistent inventory
responses rather than treating a partial list as complete.

Every object contains `id`, `name`, `identifier`, nullable `model_provider`, nullable
`type`, `credential_present`, `created_at` and `updated_at`. These are the same
metadata fields documented by [`opper_custom_model`](custom_model.md).

No credential, raw connection extras or state token is exposed in Terraform
state. Reading metadata does not decrypt or verify credentials, probe providers,
run inference or change definitions. A stored credential's presence does not
establish model availability, funding or execution eligibility.
