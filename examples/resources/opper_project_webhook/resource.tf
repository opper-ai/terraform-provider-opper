resource "opper_project" "app" {
  name = "webhook-example-service"
}

resource "opper_project_webhook" "app" {
  project_uuid = opper_project.app.id
  url          = "https://hooks.example.com/opper"
}
