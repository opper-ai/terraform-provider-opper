# Provider scope and API coverage

The target is declarative management of Opper's customer-facing configuration. The provider calls the public Management API directly. Management MCP derives its tools from those same REST operations; its catalog is a useful coverage reference, while Terraform resources need persistent identity, refresh, import and predictable reconciliation.

This initial slice implements a project resource and a UUID lookup data source. The remaining rows are a roadmap, not supported Terraform resources.

| Area | Current public Management API | Terraform scope and sequence |
|---|---|---|
| Projects | List, create, fetch, patch, delete | First slice: `opper_project` resource and data source, UUID import, drift and deletion detection. |
| Runtime API keys | List, create, delete within a project | Next: project-owned key resource; define one-time-secret, import and rotation behavior before implementation. |
| Rules | List, create, patch, delete | Next: typed policy resources for spend limits, retention, model access, checks and routing. |
| Dynamic routes | List, create, fetch, metadata/draft update, deploy, versions, simulation, restore, rollback, delete | Next: desired routing graph and explicit deployment semantics; avoid versions on unchanged applies. |
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

**Keys:** the secret is returned once and is unavailable on refresh, import or idempotency replay. Choose and document state handling and recovery before exposing it. Marking an output sensitive does not exclude it from Terraform state. Explicitly create project-owned keys for infrastructure rather than relying on the API's personal-key default.

**Rules:** kind and scope are immutable. Replacement can briefly alter policy enforcement; design updates and dependency ordering accordingly. Rule IDs are client-chosen UUIDs, and retention changes can require explicit confirmation for stored-file deletion.

**Routes:** draft saves increment revisions and deploys create immutable versions. Compare configured graphs with both draft and active state, avoid repeat writes on unchanged applies, and define partial-failure/concurrent-edit recovery. A successful route create followed by a failed deployment must remain recoverable.

**Invitations:** acceptance, expiration and resend are transitions with effects outside Terraform. Deleting an accepted invitation does not remove the member. Do not promise a managed membership lifecycle until the API can support that contract.

## Verification and delivery

The local test suite exercises the actual Terraform CLI against a task-owned HTTP test server: create, unchanged plan, update and clear description, import, data-source lookup, external drift, external deletion and destroy. HTTP client tests cover API error paths separately. Opt-in acceptance tests target a confirmed disposable Opper organization and must verify that task-created projects are deleted. Live Opper acceptance is still pending for the first slice.

Keep delivery in focused PRs: projects first, then keys, rules and routes with their own tests. Repository publication, Registry releases and subsequent PR scopes remain separate steps.

## Owning references

- [Management API guide](https://docs.opper.ai/control-plane/management-api)
- [Live OpenAPI schema](https://api.opper.ai/v3/openapi.json)
- Opper source: `task-api/internal/handler/management/surface.go` (public routes and scopes), `task-api/internal/handler/management/schema/schema.go` (request and response bodies), `task-api/internal/handler/management/projects.go` (project lifecycle), and `task-api/internal/mcp/tools.go` / `user_tools.go` (management versus OAuth MCP catalogs).
