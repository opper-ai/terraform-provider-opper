resource "opper_organization_model_list" "models" {
  organization_uuid = "01234567-89ab-4cde-8012-3456789abcde"
  compat_model_list = "recommended"
}

# List only the organization's dynamic routes.
resource "opper_organization_model_list" "routes_only" {
  organization_uuid                = "01234567-89ab-4cde-8012-3456789abcde"
  compat_model_list                = "none"
  compat_model_list_dynamic_routes = true
}
