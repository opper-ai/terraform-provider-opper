---
page_title: "opper_dynamic_route_deployment Resource - Opper"
description: "Deploy and undeploy exact Opper dynamic-route drafts with concurrency protection."
---

# opper_dynamic_route_deployment (Resource)

Manages the active lifecycle of a routing graph separately from the [`opper_dynamic_route` draft resource](dynamic_route.md). Adding this resource publishes a live version. Changing the referenced draft revision publishes one new version; unchanged applies make no deployment writes.

```hcl
resource "opper_dynamic_route_deployment" "support" {
  route_name     = opper_dynamic_route.support.name
  route_uuid     = opper_dynamic_route.support.uuid
  draft_revision = opper_dynamic_route.support.draft_revision
  note           = "Support routing configuration"
}
```

The draft must be deployable, with a reachable answering path. Defining a deployment does not run inference, but it immediately changes which graph subsequent calls to `dynamic/<name>` execute. Deployments are organization-scoped; project policies still apply to each caller.

## Backend requirement

Deploy the conditional-deployment backend API before using this resource. The provider exclusively uses `POST /management/v1/dynamic-routes/{name}/deployments`, with mandatory route UUID, draft revision, active-version preconditions and an idempotency key. It never falls back to the legacy `/deploy` endpoint, which older servers can accept without enforcing those preconditions. An old replica returns an error on the new path without changing routing.

The backend must also provide conditional `/undeploy` and pure `GET .../deployments/{idempotency_key}` receipt lookup. Complete the backend rollout on all replicas before applying; mixed revisions can cause receipt lookup or mutation failures. Live acceptance for this resource passed on 2026-11-03 after the backend rollout; see the [acceptance record](../live-acceptance.md). Local tests include actual Terraform CLI workflows against both HTTP fixtures and the real backend with PostgreSQL.

## Schema

| Attribute | Type | Behavior | Description |
|---|---|---|---|
| `route_name` | String | Required, replacement | Bare route name, normally the draft resource's `name`. |
| `route_uuid` | String | Required, replacement | Exact route UUID. Protects a replacement route that reuses the same name. |
| `draft_revision` | Number | Required | Exact positive draft revision to publish. Reference the draft resource's computed `draft_revision`. |
| `deployed` | Boolean | Optional | Defaults to `true`. Whether this resource's owned immutable version should be active. |
| `note` | String | Optional | Release note, default empty. Changing it while deployed publishes a new version. |
| `id` | String | Read-only | Route name. |
| `operation_id` | String | Read-only | Apply-time nonce used for a deployment receipt. It is not a credential. |
| `operation_pending` | Boolean | Read-only | An uncertain API outcome still needs recovery. Further mutations are blocked. |
| `version_number` | Number | Read-only | Last immutable version owned by this resource, retained while disabled. Null before the first deployment. |
| `version_uuid` | String | Read-only | Exact UUID of the owned immutable version. |
| `deployed_draft_revision` | Number | Read-only | Draft revision captured by that immutable version. |
| `deployed_graph_json` | String (JSON) | Read-only | Exact graph captured by the owned version, distinct from the current editable draft. |
| `observed_active_version_number` | Number | Read-only | Current active version, including another writer's version. Zero means inactive. |
| `observed_active_version_uuid` | String | Read-only | Current active version UUID, or null when inactive. |
| `version_created_at` | String | Read-only | Owned immutable version's creation timestamp. |

Requires `dynamic_routes:read` and `dynamic_routes:write`. One deployment resource should manage a route's active pointer; do not declare competing resources for the same route.

## Activation, disabling and drift

Initial creation requires the route to be inactive. To manage an existing active route, explicitly import its exact active version. A `deployed = false` resource can initially be created for an inactive route without publishing anything.

Updating an active resource atomically checks the configured route UUID and draft revision, plus the active version observed during planning. A concurrently edited draft or changed active pointer causes a conflict instead of publishing an unintended graph. Review a new plan before applying again.

Setting `deployed = false` undeploys only this resource's owned version. Re-enabling publishes the selected draft as a new immutable version; it does not reactivate or mutate historical versions. While disabled, changing `note` or `draft_revision` only changes desired configuration for the next activation.

Refresh compares the exact owned version UUID with the active pointer. An outside deployment is detected even when it publishes the same draft revision and graph. `deployed` becomes false, while `observed_active_version_number` identifies the other version. A new plan with configured `deployed = true` explicitly reconciles that drift by publishing a new version using the observed active version as a concurrency precondition. A stale saved plan fails instead of replacing a later writer's version.

`deployed = false` means the **owned version** is inactive. Another writer can still activate a different version; inspect the observed active fields when multiple operators use a route.

## Destroy

Destroy undeploys the owned version and leaves the route, draft and immutable version history intact. If another writer owns the active pointer, destroy refuses to stop that version. Reconcile or explicitly import the intended active version before proceeding. If the route is already inactive, destroy needs no undeploy mutation.

If both the draft and deployment resources are removed from configuration, Terraform undeploys first and then deletes the route through the draft resource. Route deletion removes the invocation itself, including access to versions deployed elsewhere. Review dependencies and use `prevent_destroy` where appropriate.

## Import

Import requires the exact currently active version number:

```sh
terraform import opper_dynamic_route_deployment.support support-router/3
```

The import reads that immutable version and its UUID, graph, source draft revision and note. It does not publish a version. Align configuration with the imported source revision and note before applying, or review the proposed new deployment.

## Idempotency and uncertain outcomes

Each mutation generates one operation nonce at apply time and sends one POST. An uncertain deploy response is recovered through a pure receipt GET; a lost undeploy response is checked with a pure route GET. A receipt identifies the original immutable version even if the active pointer later changed. The provider never treats historical receipt success as proof that its version is still active, and never sends a POST during refresh.

When a confirmed receipt is immediately available, apply completes without creating a duplicate version. If the outcome remains unresolved, the provider retains identity and nonce with `operation_pending = true` and blocks further mutations.

Terraform taints a failed initial Create. Automatically replaying an apply after a later receipt lookup could otherwise destroy and recreate the recovered live version. Once refresh identifies that original version, it requires explicit recovery. After confirming the exact active version and protecting a state backup, remove only this deployment address from state and import it again:

```sh
terraform state rm opper_dynamic_route_deployment.support
terraform import opper_dynamic_route_deployment.support support-router/3
terraform plan
```

These recovery commands change Terraform state; the import and refresh are read-only API operations. Do not reapply before completing recovery. A hard process loss before Terraform records the nonce can also require inventory inspection and import. The provider does not claim to persist state synchronously in the middle of an HTTP request.
