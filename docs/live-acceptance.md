# Live acceptance

## Organization model list: pending backend rollout

`TestAccOrganizationModelListResource` is gated and has not run against production because the new Management API is not deployed. Local real-Terraform tests cover the resource/data source, and a cross-repository integration test exercises actual management-key authentication, HTTP handlers and PostgreSQL: adoption, no-op plan, update, import, external drift, scope refusal/recovery, and destroy retaining the setting. These are local results, not evidence of production availability.

After rollout, live acceptance must save the Jctest baseline preference and restore it explicitly after Terraform destroy, since destroy intentionally leaves the remote setting unchanged. Verify the setting API, Management MCP and runtime compat model-list responses without inference, then confirm original inventory is preserved.

## Baseline: 2026-11-03

The provider's five opt-in acceptance suites passed against the public Opper Management API in a confirmed test organization on Control Plane. Each suite used uniquely named, task-owned resources and ran separately with a freshly executed Terraform workflow (`-count=1`). Credentials were supplied privately through the environment, outside source control.

| Suite | Result | Live workflow exercised |
|---|---|---|
| `TestAccProjectResource` | Pass | Create, unchanged plan, rename, description update/clear, UUID import, API-driven drift correction, external deletion/recreation and destroy. |
| `TestAccAPIKeyResource` | Pass | Project-owned key creation, metadata-only import, unchanged plans, rotation with `create_before_destroy`, API verification that the new key exists and the old key is revoked, and cleanup of key and parent project. |
| `TestAccRuleResource` | Pass | Task-project scope, retention rule creation, disabled/enabled/disabled transitions, retention configuration PATCH, API verification, unchanged plan, semantic JSON import and cleanup of rule and parent project. |
| `TestAccDynamicRouteResource` | Pass | Draft creation, description/graph PUTs, independent label PATCHes, field clearing, precise draft-revision changes, stable identity, unchanged plans, semantic import and deletion. No active version was created. |
| `TestAccDynamicRouteDeploymentResource` | Pass | Create and activate v1, unchanged plan, draft revision update to v2, disable with no active version, re-enable as v3, exact active-version import and destroy. Original inventory restored. |

Before and after each suite, project, key, rule and route inventories were checked. The original project and runtime key remained; all task-created projects, keys, rules and routes were removed. Management API access was verified against a known project in the selected organization before each run.

No model inference was performed. The baseline four suites created no active route versions; the conditional deployment suite activated only its uniquely named temporary route and removed that route afterward. The rule test changed only its own temporary project's policy. The dedicated management credential remains outside the repository for subsequent authorized acceptance runs.

## Project webhooks: 2026-11-04

`TestAccProjectWebhookResource` passed against the public Management API in Jctest in 4.88 seconds. It created a fresh project, verified its initial webhook overview had no URL or signing secret, set the first URL, checked an unchanged plan, updated the URL, imported by project UUID, corrected an API-driven external edit, and removed only the webhook resource before deleting the parent project.

The first URL write initialized the signing secret, as the API specifies. Subsequent reads, URL updates, import and URL clearing retained that same secret creation timestamp and created no rotation overlap. The test never retrieved a secret value or invoked the secret-rotation or test-delivery endpoints. No inference or webhook delivery was requested.

Before and after inventory remained one original project, one original runtime key, zero rules and zero routes. Original runtime-key metadata and the original project's complete webhook overview were unchanged. All task-created resources were removed. Credentials, Terraform state and raw logs stayed outside the repository.

Local Terraform CLI tests additionally cover refusing pre-existing URLs (including an exact match), invalid imports, project replacement/deletion, API refusals, interrupted initial writes with explicit import recovery, and refusing destroy after both ordinary external URL drift and an edit between refresh and deletion. The live run does not inject transport or concurrency faults. URL writes have no atomic conditional-update API.

## Member data sources: 2026-11-04

`TestAccMemberDataSources` passed against Jctest's public Management API in 1.31 seconds using a dedicated management key with only `members:read`. The Terraform configuration contained only data sources: organization member listing, lookup of an existing member by UUID, case-insensitive exact email lookup, and an unchanged plan. The live organization had one existing member; its UUID and personal details were kept out of this record.

Complete member metadata before and after the test matched. Existing projects, runtime keys, rules, routes and webhook overviews also matched. No invitations, member removals, role changes, inference or webhook deliveries were requested. The newly authorized read-only test credential was saved privately and retained for future acceptance runs; the original acceptance key's permissions were unchanged.

Local Terraform CLI tests additionally cover missing/ambiguous members, empty collections, metadata changes, deterministic ordering, selectors deferred until apply, API failures and membership disappearance. These transitions were not induced in the live organization. Client tests reject malformed or incomplete member lists before a lookup can select anyone.

## Deployment rollout verification

The conditional deployment suite ran after [backend PR #5814](https://github.com/opper-ai/opper/pull/5814) was merged as `bfdfd7a358d0f756c85f40805fd60d8de1769dc9` and that exact image completed rollout for the API and workers in both EKS and evroc.

Authenticated checks against the public API verified healthy service, all three new OpenAPI paths, required deployment preconditions, and the pure receipt endpoint's typed `deployment_receipt_not_found` response. The live management MCP catalog advertised `create_dynamic_route_deployment`, `undeploy_dynamic_route` and `get_dynamic_route_deployment`, including the mandatory deployment arguments.

The deployment acceptance suite passed in 4.38 seconds using the current catalog model `cerebras/gpt-oss-120b`. It made no inference calls. Before and after inventory was one project, one original runtime key, zero rules and zero routes; the original key metadata remained unchanged. Credentials, raw logs and Terraform state remain private and outside Git.

## Test correction found by the live run

The first rule import check failed because the API serialized the same JSON object in a different field order from Terraform's `jsonencode`. Provider semantic behavior was unchanged. The import assertion now compares JSON meaning, with unit cases proving that actual policy changes, additional fields, invalid JSON and null are rejected. The corrected live suite passed.

## Limits and remaining work

These results validate the listed workflows, not every rule configuration, account plan or failure scenario. Local tests separately cover failures, drift, normalization, concurrent edits and one-time-secret recovery. Rule writes still require the appropriate plan and all tests require the documented management scopes.

The complete example validates against the provider binary; it has not been applied as a single combined live configuration. The new conditional deployment resource has local HTTP-fixture and real PostgreSQL/backend/Terraform CLI verification. Its live acceptance passed after the conditional backend API rollout, as recorded above. The live run does not exercise transport-loss or concurrency fault injection; those paths are verified locally against HTTP fixtures and real PostgreSQL. Membership management and the other API gaps remain in the [coverage plan](coverage.md).

The tested baseline and this validation record are published in the private [GitHub repository](https://github.com/opper-ai/terraform-provider-opper). Terraform Registry releases have not been published.
