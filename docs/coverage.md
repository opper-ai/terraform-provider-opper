# Provider scope and API coverage

The target is declarative management of Opper's customer-facing configuration. The provider calls the public Management API directly. Management MCP derives its tools from those same REST operations; its catalog is a useful coverage reference, while Terraform resources need persistent identity, refresh, import and predictable reconciliation.

v0.1.0 includes 11 resources and nine data sources covering the configuration listed below. Source and acceptance records are maintained in the [GitHub repository](https://github.com/opper-ai/terraform-provider-opper). The v0.1.0 backend APIs are deployed; the later atomic-creation addition requires the dedicated endpoint rollout described below. Deferred coverage is listed separately from the supported resources.

| Area | Current public Management API | Terraform scope and sequence |
|---|---|---|
| Projects | List, create, fetch, patch, delete; dedicated atomic creation endpoint | `opper_project` resource and data source, UUID import, metadata drift and deletion detection. Optional `initial_access` seeds the policy through `/management/v1/projects/with-access`; it is creation history, not ongoing policy management. The dedicated endpoint must be deployed before using this option. |
| Runtime API keys | List, create, delete within a project | `opper_api_key`: project ownership, one-time secret retained in state, metadata-only import, explicit replacement and rotation. |
| Project webhooks | Read/update default URL; retrieve/rotate signing secret; send test event | `opper_project_webhook` manages only the default URL, with UUID import, drift detection and clearing on destroy. Signing-secret retrieval/rotation and test deliveries remain explicit operations. |
| Rules | List, create, patch, delete | `opper_rule`: all four kinds, typed scope targets and JSON config. Typed configuration helpers remain a possible later addition. |
| Dynamic routes | List, create, fetch, metadata/draft update, deploy, versions, simulation, restore, rollback, delete | `opper_dynamic_route` manages metadata/drafts; `opper_dynamic_route_deployment` manages conditional live activation and undeployment. |
| Members and invitations | List/remove members; list/create/revoke invitations | `opper_member` looks up one existing member by UUID or unambiguous email; `opper_members` lists members in stable UUID order. Resolve acceptance, expiration, resend and membership ownership semantics before writable resources. |
| Usage | Read-only aggregates | Optional data source if a configuration use case justifies it. Usage changes are not managed infrastructure. |
| Management keys | Self-disable only | Key creation is a platform bootstrap step, outside provider support. |
| BYOK custom-model definitions | Metadata, creation/recovery with initial-state proof, state and conditional update/deletion | `opper_custom_model` adds write-only credentials and explicit versions, guarded import/drift/recovery; `opper_custom_model` / `opper_custom_models` data sources provide metadata. Terraform live acceptance passed on 2026-11-05 after backend rollout. |
| Custom roles and member-role changes | Conditional CRUD, catalog, assignment, and receipt recovery deployed | `opper_custom_role`, `opper_member_role`, `opper_role`, `opper_roles` and member-role lookup. Destination definitions and saved assignments are pinned. Destroy explicitly retains the assignment or sets a built-in fallback. Live custom-role and member assignment acceptance passed. |
| Restricted project access | Conditional policy GET/PUT and Management MCP deployed in [#6119](https://github.com/opper-ai/opper/pull/6119), with dedicated scopes. Writes became generally available with [backend PR #6174](https://github.com/opper-ai/opper/pull/6174), deployed on 2026-11-13. | [`opper_project_access`](resources/project_access.md) manages existing projects, pins saved plans, requires import of existing Restricted policies and retains access on destroy. Bounded Terraform acceptance passed on 2026-11-12 against `api.opper.ai`, including a native Owner edit that invalidated a saved plan. New projects can use `opper_project.initial_access` for atomic initial policy creation. Ongoing `opper_project_access` ownership still requires explicit import and a separate plan after creation. |
| Organization model-list preference | GET/PUT API deployed | `opper_organization_model_list` resource and data source; organization UUID guard, import, drift and destroy retaining the remote preference. |
| Other organization settings/creation and billing | No public management CRUD | Public API extension and lifecycle definition required before provider support. |

Runtime model calls, traces, generated media and agent conversations are outside the managed-resource scope. The user OAuth MCP has additional discovery/runtime operations and personal keys; those do not imply equivalent management-key API support.

Atomic project creation with `initial_access` has local Terraform/client verification; live production acceptance remains pending the dedicated endpoint rollout. The dated existing-project policy acceptance below does not cover atomic creation.

## First-slice contract

- Stable identity is the project UUID. Name changes are updates, and name conflicts remain API errors.
- The resource manages both name and description. Description defaults to `""`, matching the API's representation of an unset description.
- PATCH includes only changed fields, so description updates do not attempt to rename a protected default project.
- Import uses the UUID and refreshes the existing object. It never creates or adopts by name.
- A refresh `404` removes a managed object from state. A data-source `404` is an error. Authentication and other API errors do not imply deletion.
- Project deletion removes its runtime keys. API deletion safeguards are preserved.
- Project creation has no idempotency-key contract. An uncertain create must be reconciled through inspection/import, not a blind retry or automatic name adoption.

## Decisions before expanding

**Keys:** the implemented resource retains the one-time secret in state, marks it sensitive and preserves it through refresh. Import is metadata-only. Known-ID missing-secret responses retain identity for recovery; unknown create outcomes need inspection/import before retrying. The resource explicitly requests project ownership. Optional idempotency tokens identify one create operation and must change for replacements.

**Rules:** kind and scope are immutable and trigger replacement. Replacement can briefly alter policy enforcement. The provider generates rule UUIDs and compares desired config with an effective server snapshot to handle normalization without perpetual drift. Current Opper retention rules do not delete explicitly stored files; the old confirm_file_deletion flag is deprecated and is not exposed. Per-kind config semantics and supported schema versions remain server-validated.

**Routes:** draft saves increment revisions and deploys create immutable versions. The draft resource avoids unchanged saves and preserves UI layout. The separate deployment resource uses the new mandatory-precondition `/deployments` endpoint, matching route UUID, draft revision and observed active version. It resolves uncertain deploy responses with pure receipt lookup and undeploy responses with pure route reads, retains immutable ownership, and refuses to stop another writer. Failed initial Create recovery may require state removal and exact-version import to avoid Terraform taint replacement. The provider uses the deployed conditional backend API; legacy `/deploy` is never a fallback.

**Invitations:** acceptance, expiration and resend are transitions with effects outside Terraform. Deleting an accepted invitation does not remove the member. Do not promise a managed membership lifecycle until the API can support that contract.

**Members:** the read-only data sources require `members:read` and never invite, remove or change roles. Lookup by email must match exactly one member (case-insensitive); UUID selection provides stable identity across email changes. Empty email/name/role and an absent account-creation timestamp are valid API values. A missing or ambiguous lookup and API failures remain errors. The current public API returns the complete member collection; the provider checks completeness and sorts by UUID.

**Webhooks:** creation requires an unconfigured default URL; existing settings require project UUID import. Destroy clears the URL and preserves the project and signing secret. Reads use the overview endpoint only. Setting the first URL initializes a signing secret in Opper; the provider never fetches its value or rotates it. Fetching a signing secret separately can also create one. URL writes have no conditional-update contract, so callers must coordinate concurrent writers. No test delivery or inference is part of this resource's lifecycle.

## Verification and delivery

The organization model-list increment has local client/Terraform CLI tests and a cross-repository test against actual management-key authentication, handlers and PostgreSQL. Live Jctest acceptance passed on 2026-11-04 after backend rollout: adoption, no-op, update, import and destroy retaining the setting. Public API/OpenAPI and actual Management MCP read/set calls passed, and runtime compat catalogs matched all three listing modes. The original preference and existing inventory were restored, with the temporary catalog-test project/key removed and no inference. See the [acceptance record](live-acceptance.md) for deployment and verification evidence.

The local test suite exercises the actual Terraform CLI against task-owned HTTP test servers: create, unchanged plans, updates, enable/disable, import, lookup, replacements, secret recovery, external drift/deletion and destroy. HTTP client tests cover API error paths separately. Opt-in acceptance tests target a confirmed disposable Opper organization and verify task-created resources are deleted. Live acceptance for projects, keys, rules and route drafts passed on 2026-11-03, with pre-existing inventory preserved. See the [acceptance record](live-acceptance.md).

The live acceptance gate and local complete-example validation are complete for the baseline four resources (projects, keys, rules and drafts). The conditional deployment increment has local HTTP-fixture and real PostgreSQL/backend/Terraform CLI verification, including stale plans and lost responses. Its live acceptance passed on 2026-11-03 after the backend rollout, covering activation, unchanged plans, draft updates, disable/re-enable, exact-version import and cleanup with the original inventory restored. See the [release guide](releasing.md) for packaging and publication requirements.

Project webhook live acceptance passed on 2026-11-04: configure, no-op, update, UUID import, external drift correction, URL-only destroy and project cleanup. The original inventory and webhook overview were restored. The initial URL write created the signing secret; later operations retained it without retrieving its value or requesting a delivery. Local tests cover API failures, import recovery and destroy ownership checks.

Member data source live acceptance passed on 2026-11-04 with a dedicated `members:read` key: list, UUID lookup, case-insensitive email lookup and unchanged plan. Membership and existing resource inventory stayed unchanged. Local tests cover ordering, metadata changes, missing/ambiguous results, deferred selectors, incomplete responses and API failures. Writable membership and invitation lifecycles remain deferred.

Role lifecycle verification passed locally on 2026-11-05 through the actual
Terraform CLI and provider, real Management Surface, stored management-key
authentication and disposable PostgreSQL. Thirty CLI steps covered creation,
same-apply assignment, grant updates, no-op plans, lookups, import, stale saved
plans, membership edit/revert, explicit retain and pinned fallback destruction.
The backend recorded 202 authenticated management requests. Effective inference
permission and restricted-project eligibility changed with the assignment;
no inference request was sent. All synthetic resources were removed and the
original seed data matched byte-for-byte. Backend integration tests separately
cover role dependencies, including budget bindings, and transaction rollback.
Live acceptance passed on 2026-11-06 after the backend rollout. Terraform verified
the [custom-role lifecycle](live-acceptance.md#custom-role-lifecycle-2026-11-06)
and [member assignment with a pinned Viewer fallback](live-acceptance.md#member-role-assignment-2026-11-06).

Project-access local and bounded live Terraform acceptance are recorded in the [acceptance record](live-acceptance.md#project-access-lifecycle-2026-11-12). The temporary write gate and credential were removed after that run. The organization activation gate was subsequently removed by backend PR #6174, deployed on 2026-11-13. Evroc Terraform acceptance was deliberately omitted; native regional checks are separate evidence.

## Release boundary

v0.1.0 packages the implemented resources and data sources. Installation does not change any project policy. Invitations and membership ownership need a defined lifecycle before writable resources are added. Other organization settings and billing need additional public management APIs. Usage is a possible read-only addition, while runtime calls, traces and generated content remain outside declarative management. Release packaging and publication are documented in the [release guide](releasing.md).

## Owning references

- [Management API guide](https://docs.opper.ai/control-plane/management-api)
- [Live OpenAPI schema](https://api.opper.ai/v3/openapi.json)
- Opper source: `task-api/internal/handler/management/surface.go` (public routes and scopes), `task-api/internal/handler/management/schema/schema.go` (request and response bodies), `task-api/internal/handler/management/projects.go` (project lifecycle), and `task-api/internal/mcp/tools.go` / `user_tools.go` (management versus OAuth MCP catalogs).
