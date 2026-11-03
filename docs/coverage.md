# Provider scope and API coverage

The target is declarative management of Opper's customer-facing configuration. The provider calls the public Management API directly. Management MCP derives its tools from those same REST operations; its catalog is a useful coverage reference, while Terraform resources need persistent identity, refresh, import and predictable reconciliation.

The local implementation manages projects, project-owned runtime keys, rules and route drafts. Publication is deferred until the provider is ready to ship. Remaining gaps are listed explicitly below.

| Area | Current public Management API | Terraform scope and sequence |
|---|---|---|
| Projects | List, create, fetch, patch, delete | First slice: `opper_project` resource and data source, UUID import, drift and deletion detection. |
| Runtime API keys | List, create, delete within a project | `opper_api_key`: project ownership, one-time secret retained in state, metadata-only import, explicit replacement and rotation. |
| Rules | List, create, patch, delete | `opper_rule`: all four kinds, typed scope targets and JSON config. Typed configuration helpers remain a possible later addition. |
| Dynamic routes | List, create, fetch, metadata/draft update, deploy, versions, simulation, restore, rollback, delete | `opper_dynamic_route`: metadata and drafts only. Live deployment needs backend preconditions and idempotency support. |
| Members and invitations | List/remove members; list/create/revoke invitations | Later: member lookup first. Resolve acceptance, expiration, resend and membership ownership semantics before writable resources. |
| Usage | Read-only aggregates | Optional data source if a configuration use case justifies it. Usage changes are not managed infrastructure. |
| Management keys | Self-disable only | Key creation is a platform bootstrap step, outside provider support. |
| BYOK credentials | No public management CRUD | Public API extension required before provider support. |
| Custom roles and member-role changes | No public management CRUD/update | Public API extension required before provider support. |
| Organization creation/settings and billing | No public management CRUD | Public API extension and lifecycle definition required before provider support. |

Runtime model calls, traces, generated media and agent conversations are outside the managed-resource scope. The user OAuth MCP has additional discovery/runtime operations and personal keys; those do not imply equivalent management-key API support.

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

**Routes:** draft saves increment revisions and deploys create immutable versions. The implemented draft resource avoids unchanged saves, preserves UI layout, handles partial metadata/draft failure and observes active versions without changing them. Deployment currently snapshots the current draft without a caller-supplied revision/graph precondition and always creates another version. Add atomic expected-revision/graph validation and an idempotent deployment receipt before Terraform manages live deployment.

**Invitations:** acceptance, expiration and resend are transitions with effects outside Terraform. Deleting an accepted invitation does not remove the member. Do not promise a managed membership lifecycle until the API can support that contract.

## Verification and delivery

The local test suite exercises the actual Terraform CLI against task-owned HTTP test servers: create, unchanged plans, updates, enable/disable, import, lookup, replacements, secret recovery, external drift/deletion and destroy. HTTP client tests cover API error paths separately. Opt-in acceptance tests target a confirmed disposable Opper organization and verify task-created resources are deleted. Live acceptance for projects, keys, rules and route drafts passed on 2026-11-03, with pre-existing inventory preserved. See the [acceptance record](live-acceptance.md).

Keep the resource slices in focused local commits. Do not create the GitHub repository, push branches or open PRs until there is a useful release ready to ship and publication is authorized. The live acceptance gate and local complete-example validation are complete for the current resource scope. Registry release preparation and publication remain deferred until agreed; live route deployment and the remaining API gaps are separate scope.

## Owning references

- [Management API guide](https://docs.opper.ai/control-plane/management-api)
- [Live OpenAPI schema](https://api.opper.ai/v3/openapi.json)
- Opper source: `task-api/internal/handler/management/surface.go` (public routes and scopes), `task-api/internal/handler/management/schema/schema.go` (request and response bodies), `task-api/internal/handler/management/projects.go` (project lifecycle), and `task-api/internal/mcp/tools.go` / `user_tools.go` (management versus OAuth MCP catalogs).
