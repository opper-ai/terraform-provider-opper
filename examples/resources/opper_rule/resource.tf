resource "opper_project" "app" {
  name = "checkout-service"
}

resource "opper_rule" "retention" {
  kind    = "comply"
  name    = "Checkout trace retention"
  enabled = true

  scope = {
    type          = "projects"
    project_uuids = [opper_project.app.id]
  }

  config_json = jsonencode({
    type           = "retention"
    retention_days = 7
  })
}
