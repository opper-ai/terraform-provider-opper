# Live acceptance: 2026-11-03

The provider's four opt-in acceptance suites passed against the public Opper Management API in a confirmed test organization on Control Plane. Each suite used uniquely named, task-owned resources and ran separately with a freshly executed Terraform workflow (`-count=1`). Credentials were supplied privately through the environment, outside source control.

| Suite | Result | Live workflow exercised |
|---|---|---|
| `TestAccProjectResource` | Pass | Create, unchanged plan, rename, description update/clear, UUID import, API-driven drift correction, external deletion/recreation and destroy. |
| `TestAccAPIKeyResource` | Pass | Project-owned key creation, metadata-only import, unchanged plans, rotation with `create_before_destroy`, API verification that the new key exists and the old key is revoked, and cleanup of key and parent project. |
| `TestAccRuleResource` | Pass | Task-project scope, retention rule creation, disabled/enabled/disabled transitions, retention configuration PATCH, API verification, unchanged plan, semantic JSON import and cleanup of rule and parent project. |
| `TestAccDynamicRouteResource` | Pass | Draft creation, description/graph PUTs, independent label PATCHes, field clearing, precise draft-revision changes, stable identity, unchanged plans, semantic import and deletion. No active version was created. |

Before and after each suite, project, key, rule and route inventories were checked. The original project and runtime key remained; all task-created projects, keys, rules and routes were removed. Management API access was verified against a known project in the selected organization before each run.

No model inference or route deployment was performed. The rule test changed only its own temporary project's policy. The dedicated management credential remains outside the repository for subsequent authorized acceptance runs.

## Test correction found by the live run

The first rule import check failed because the API serialized the same JSON object in a different field order from Terraform's `jsonencode`. Provider semantic behavior was unchanged. The import assertion now compares JSON meaning, with unit cases proving that actual policy changes, additional fields, invalid JSON and null are rejected. The corrected live suite passed.

## Limits and remaining work

These results validate the listed workflows, not every rule configuration, account plan or failure scenario. Local tests separately cover failures, drift, normalization, concurrent edits and one-time-secret recovery. Rule writes still require the appropriate plan and all tests require the documented management scopes.

The complete example validates against the provider binary; it has not been applied as a single combined live configuration. Live deployment of dynamic routes remains unsupported pending conditional, idempotent backend deployment. Membership management and the other API gaps remain in the [coverage plan](coverage.md).

The tested baseline and this validation record are published in the private [GitHub repository](https://github.com/opper-ai/terraform-provider-opper). Terraform Registry releases have not been published.
