# This resource saves a draft; it never deploys a live route.
resource "opper_dynamic_route" "support" {
  name         = "support-router"
  display_name = "Support router"
  description  = "Routing draft maintained in Terraform"

  # START -> END is a draft scaffold. Add an answering path before deploying
  # through the Opper platform or Management API outside Terraform.
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
