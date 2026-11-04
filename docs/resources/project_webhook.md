---
page_title: "opper_project_webhook Resource - Opper"
description: "Manage a project's default webhook URL."
---

# opper_project_webhook (Resource)

Manages the default completion webhook URL for an existing project. Jobs submitted without an explicit callback URL use this default. Setting the first URL initializes a signing secret in Opper. This resource never retrieves its value, rotates it, sends test events, or verifies delivery.

```hcl
resource "opper_project_webhook" "app" {
  project_uuid = opper_project.app.id
  url          = "https://hooks.example.com/opper"
}
```

The management key needs `apikeys:read` and `apikeys:write`. Creating or deleting the parent project separately requires the project scopes.

## Schema

| Attribute | Type | Behavior | Description |
|---|---|---|---|
| `project_uuid` | String | Required | Project UUID. Changing it replaces the webhook resource. |
| `url` | String, sensitive | Required | Public HTTPS callback URL. Stored in Terraform state; protect the state backend if the URL contains a token. |
| `id` | String | Read-only | Project UUID, also used for import. |
| `url_updated_at` | String | Read-only | Last URL update timestamp, when available. |
| `secret_created_at` | String | Read-only | Current signing secret's creation timestamp, when one exists. Contains no secret value. |
| `previous_secret_valid_until` | String | Read-only | Previous signing secret's rotation overlap deadline, when present. |

Use one webhook resource per project. Changing the URL updates the existing project configuration. The API performs its own URL validation, including public-address checks.

## Import and drift

An existing default URL must be imported before Terraform can manage it. Creation refuses to overwrite an already configured URL, even when it matches the configuration.

```sh
terraform import opper_project_webhook.app 01234567-89ab-4cde-8012-3456789abcde
```

Review the next plan before applying. Refresh reads only the webhook overview and detects external URL changes. If the URL is cleared outside Terraform, the resource leaves state and the next apply can configure it again. A missing project also removes the webhook from state; other API failures preserve state.

## Destroy and recovery

Destroy clears only the default URL. It retains the project and its signing secret. Before clearing, the provider checks that the current URL matches the last URL it successfully wrote or imported. An external edit detected during refresh does not transfer ownership: destroy refuses to clear that changed URL until you reconcile it or explicitly import it. The API offers no conditional write, so a concurrent change between that check and the clear cannot be prevented. Coordinate writers for this project setting. Use `prevent_destroy` when removal must require a configuration change.

After an uncertain write, inspect the project's webhook setting and import its UUID if the write succeeded. If the resource is already in state, remove only its state entry before importing it again. Import explicitly accepts ownership of the current URL. The provider does not automatically retry writes or adopt an existing URL.

Signing-secret retrieval can create a secret on first read. This resource uses only the overview and URL update endpoints; secret retrieval, rotation and test-event delivery remain explicit operations outside its lifecycle.
