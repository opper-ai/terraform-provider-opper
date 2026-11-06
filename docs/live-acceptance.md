# Live acceptance

## Organization model list: 2026-11-04

`TestAccOrganizationModelListResource` passed against Jctest's public Management API in 2.30 seconds after [backend PR #5834](https://github.com/opper-ai/opper/pull/5834) merged as `904ddb691539711355446f19b802a9e2bb827dbc`. The deployed descendant image `1876ea4a8b725bd7699cef3af9519b079f00d9f8` contains that merge; its only additional change is the evroc CNPG infrastructure manifest. API and worker rollout jobs completed successfully in both EKS and evroc in [the production workflow](https://github.com/opper-ai/opper/actions/runs/41816442686).

The live Terraform workflow adopted the existing setting, checked an unchanged plan, updated to `simplified`, imported by organization UUID, and destroyed the resource while confirming the remote preference remained `simplified`. Acceptance cleanup then explicitly restored the original `all` preference. The data source read the same organization setting. Local real-backend/CLI tests separately cover drift correction, scope refusal/recovery and transactional audit.

Public health and OpenAPI checks passed. The Management MCP catalog contained both new tools; actual read and set calls returned the verified test organization and requested preference. A temporary project-owned runtime key read the compat catalog in all three modes, and each default listing matched its corresponding explicit `view` response:

| Preference | Advertised entries |
|---|---:|
| `all` | 1,260 |
| `simplified` | 326 |
| `recommended` | 22 |

The temporary project and key were removed. Original project/key/rule/route inventory and webhook overviews were unchanged, and the organization preference was restored to `all`. No inference or webhook deliveries were requested. This live check verifies model discovery behavior; execution eligibility remains covered separately by backend tests. Credentials and raw logs stayed private and outside Git.

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
## Custom-model lifecycle: 2026-11-05

`TestAccCustomModelResource` passed against Jctest's public Management API at `api.opper.ai` in 5.38 seconds, with race detection enabled, on provider revision `ce6fa9c`. The separately approved key has only `custom_models:read`, `custom_models:write`, `custom_models:update`, and `custom_models:delete`. Its organization and exact grants were independently verified in the platform UI; the credential remains private and outside the repository.

The subsequent input-validation fixes are verified locally with real Terraform applies: oversized ASCII/multibyte credentials and Unicode-only blank names send no mutation, retain no pending operation, and accept corrected inputs without import or state repair. Credential checks also cover saved-plan applies and valid boundary-sized inputs. The client explicitly identifies validation failures before an HTTP request, keeping them separate from uncertain writes. The production lifecycle run was not repeated for these validation-before-HTTP changes.

The real Terraform workflow created one dummy default-endpoint OpenAI definition, checked an unchanged plan, renamed it, rotated a dummy credential by increasing the explicit version, imported by UUID, checked another unchanged plan, and destroyed the model. Both custom-model data sources ran in the same configuration. Saved states omitted the write-only credential, and the separate import state started at credential version zero.

Complete model metadata and the existing project, runtime-key, rule, route and webhook inventories matched before and after. Custom-model inventory remained empty, and an explicit post-destroy state read returned 404 for the test UUID. Pure recovery retained one creation receipt with the original initial token and two update receipts; all reported `resource_exists:false`, and both update receipts reported `result_matches_current_state:false`. The approved test key, receipts and normal audit history remain. Audit-event counts were not independently queried.

The prerequisite [backend PR #5902](https://github.com/opper-ai/opper/pull/5902) merged as `ed2590333291657b1683f2bf5094d226902fb8e9`. That exact API/worker image completed rollout in EKS and evroc in [the production workflow](https://github.com/opper-ai/opper/actions/runs/42030949580). Public health and the published creation-state contract passed on both endpoints. Authenticated pure recovery and MCP tool/scope checks passed on the primary endpoint; the existing Jctest key returned 401 on evroc, so authenticated evroc behavior and Terraform acceptance were not verified there.

No provider probe or inference was requested. Live acceptance did not inject transport loss, concurrent edits or signing-key rotation. Failure handling has separate local fixture and backend coverage, including a real Management API/PostgreSQL end-to-end workflow for stale-plan refusal and hidden edits. API/MCP acceptance of earlier surfaces is separate evidence from this Terraform lifecycle run. To repeat the suite, obtain authorization and supply the disposable organization's key privately:

```sh
TF_ACC=1 OPPER_TEST_DISPOSABLE_ORG=1 OPPER_TEST_CUSTOM_MODEL_ACCEPTANCE=1 \
  go test ./internal/provider -run '^TestAccCustomModelResource$' -count=1 -v
```

`OPPER_TEST_ORGANIZATION_UUID` records the independently verified organization identity. A broad legacy `TestAcc` run does not enable this suite without its custom-model opt-in. Existing API/MCP creation/update/deletion acceptance does not replace this Terraform workflow.

## Custom-role lifecycle: 2026-11-06

Terraform on provider revision `d334e0d` passed the custom-role lifecycle against the public Management API in a disposable Enterprise organization. The backend migration and rollout completed in EKS and Evroc after [backend PR #5919](https://github.com/opper-ai/opper/pull/5919) merged as `bacd30714`. [Production workflow](https://github.com/opper-ai/opper/actions/runs/42194679596)

The run created one unassigned custom role, verified an unchanged plan, and updated its grants. A conditional API edit to that same fixture made an earlier saved plan stale. Terraform received HTTP `409`, and a read confirmed the edit was preserved. Import restored the reviewed definition to Terraform state, the next plan was unchanged, and destroy removed the role. The original catalog matched before and after. Three operation receipts retained their original proofs and reported the resource absent after deletion.

The temporary key had only `roles:read`, `roles:write`, and `roles:delete`. After cleanup, self-disable returned `204` and the catalog rejected the disabled token with `401`. Live member assignment was outside this run because the sandbox had only its owner. The assignment lifecycle, including fallback and effective permission changes, passed locally through the real Management API and PostgreSQL.
