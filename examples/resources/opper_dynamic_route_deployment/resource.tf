variable "model_id" {
  type        = string
  description = "Exact current model ID allowed by your organization."
}

resource "opper_dynamic_route" "support" {
  name = "support-router"

  graph_json = jsonencode({
    elements = [
      {
        id      = "START"
        type    = "start"
        outputs = { next = { elementId = "answer" } }
      },
      {
        id         = "answer"
        type       = "model"
        properties = { model = var.model_id }
        outputs = {
          success  = { elementId = "END" }
          fallback = { elementId = "END" }
        }
      },
      {
        id      = "END"
        type    = "end"
        outputs = {}
      }
    ]
  })
}

# This resource changes live routing. Requires the conditional-deployment
# backend rollout; it does not fall back to the legacy deployment endpoint.
resource "opper_dynamic_route_deployment" "support" {
  route_name     = opper_dynamic_route.support.name
  route_uuid     = opper_dynamic_route.support.uuid
  draft_revision = opper_dynamic_route.support.draft_revision
  note           = "Managed routing release"
}
