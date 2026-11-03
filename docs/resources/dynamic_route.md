---
page_title: "opper_dynamic_route Resource - Opper"
description: "Manage an Opper dynamic route draft and its metadata."
---

# opper_dynamic_route (Resource)

Manages a dynamic route's **draft and metadata**. It never deploys a graph. Creating this resource produces a draft-only route; an existing active version remains active while Terraform edits its draft. Use the separate [deployment resource](dynamic_route_deployment.md) to opt into live routing after the conditional backend API is deployed.

```hcl
resource "opper_dynamic_route" "support" {
  name         = "support-router"
  display_name = "Support router"
  description  = "Routing draft maintained in Terraform"

  graph_json = jsonencode({
    elements = [
      {
        id      = "START"
        type    = "start"
        outputs = { next = { elementId = "END" } }
      },
      {
        id      = "END"
        type    = "end"
        outputs = {}
      }
    ]
  })
}
```

This example is a schema-valid draft scaffold. It does not answer model calls and cannot be deployed until an answering path is added. Author the desired graph using the [Management API graph contract](https://api.opper.ai/v3/openapi.json). Opper validates node properties and connectivity when saving the draft.

## Schema

| Attribute | Type | Behavior | Description |
|---|---|---|---|
| `name` | String | Required | Unique organization-scoped identifier matching `^[a-z0-9][a-z0-9._-]{0,127}$`. A name change replaces the route. |
| `graph_json` | String (JSON) | Required | Desired draft graph object with a nonempty `elements` array. JSON formatting and key-order changes make no API writes; Terraform may record a one-time state update. Unknown graph/element/output fields are rejected before sending them to Opper. |
| `display_name` | String | Optional | Human label, at most 128 characters without control characters or surrounding whitespace. Defaults to empty, which uses the route identifier in the platform. Changing it does not save or deploy the draft. |
| `description` | String | Optional | Description. Defaults to empty; removing it clears the description. Changing it saves the draft. |
| `id` | String | Read-only | Route name, used for import. |
| `uuid` | String | Read-only | Route UUID. |
| `invocation_model` | String | Read-only | `dynamic/<name>`. This invocation is usable only when the route has an active version. |
| `effective_graph_json` | String (JSON) | Read-only | Server-normalized draft graph, including defaults added by Opper. |
| `draft_revision` | Number | Read-only | Current saved draft revision. |
| `active_version_number` | Number | Read-only | Active version, if any. This is an observation, not a Terraform deployment setting. |
| `created_at` | String | Read-only | Creation timestamp. |

Requires `dynamic_routes:read` and `dynamic_routes:write`. Editor canvas layout is unmanaged and is preserved when updating an existing draft.

## Draft lifecycle and drift

An unchanged apply makes no draft, metadata or deployment writes. A formatting-only graph change can produce a one-time Terraform state update, with no API mutation; subsequent plans are empty. Graph/description changes save a new draft revision; a display-name-only change uses the metadata endpoint. Neither operation changes the active graph.

`graph_json` preserves the configured graph while `effective_graph_json` records the normalized API result. If the remote draft still matches that result, refresh preserves the desired configuration. A draft edited outside Terraform is exposed as drift and the next apply restores the configured draft. An outside deployment updates `active_version_number`; The draft resource does not deploy a replacement version or roll it back.

The API has no conditional draft-write precondition. Avoid editing the same draft concurrently in the platform and Terraform: apply can overwrite a draft edit made after its refresh. Layout is preserved, but graph ownership should have one writer.

Label and draft updates are separate HTTP operations. If the label succeeds but saving the draft fails, the completed label update is retained in state. A later apply retries the outstanding draft change.

If the label response reveals that the graph or description changed after Terraform's refresh, apply stops before saving any draft. It retains the completed label and observed draft in state so you can review the concurrent edit in a new plan. A label-only plan never expands into a draft write because of that concurrent edit.

## Import

Import an existing route by its bare name, without `dynamic/`:

```sh
terraform import opper_dynamic_route.support support-router
```

Import reads the current draft into `graph_json`; the original author's pre-normalization JSON is unavailable. Review the plan and align configuration with the imported graph. Importing a deployed route does not change its active version.

Creation has no idempotency-key contract. If the create response is lost or fails after the API stored the route, inspect the organization and import the route by name before retrying. Terraform does not adopt a same-named route automatically.

## Deletion

Destroy deletes the entire route, including access to its active version. Model calls to `dynamic/<name>` then stop working. This applies even when the active version was deployed outside Terraform. Use Terraform's `prevent_destroy` for routes serving production traffic. API errors leave the managed route in state.

## Separate live deployment

This draft resource never deploys or undeploys. The optional [`opper_dynamic_route_deployment` resource](dynamic_route_deployment.md) publishes the exact saved draft revision and owns the resulting immutable version. It requires the conditional backend API, which must be fully rolled out first. It does not use the legacy endpoint that can accept a deployment without concurrency preconditions.

Use separate resources so saving a draft is distinct from changing live routing. Without a deployment resource, deploy through the platform or Management API as before.
