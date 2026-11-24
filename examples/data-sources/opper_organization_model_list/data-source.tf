data "opper_organization_model_list" "current" {}

output "organization_uuid" {
  value = data.opper_organization_model_list.current.organization_uuid
}

output "dynamic_routes_listed" {
  value = data.opper_organization_model_list.current.compat_model_list_dynamic_routes
}
