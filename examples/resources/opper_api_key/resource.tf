resource "opper_project" "app" {
  name = "checkout-service"
}

resource "opper_api_key" "app" {
  project_id = opper_project.app.id
  name       = "checkout-runtime"
  rotation   = "initial"

  lifecycle {
    create_before_destroy = true
  }
}

output "runtime_api_key" {
  value     = opper_api_key.app.secret
  sensitive = true
}
