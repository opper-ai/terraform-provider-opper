resource "opper_organization_model_list" "models" {
  organization_uuid = "01234567-89ab-4cde-8012-3456789abcde"
  compat_model_list = "recommended"
}

# Also limit the organization's API keys to the listed models.
resource "opper_organization_model_list" "limited" {
  organization_uuid          = "01234567-89ab-4cde-8012-3456789abcde"
  compat_model_list          = "recommended"
  compat_model_list_enforced = true
}

# List only the organization's dynamic routes.
resource "opper_organization_model_list" "routes_only" {
  organization_uuid                = "01234567-89ab-4cde-8012-3456789abcde"
  compat_model_list                = "none"
  compat_model_list_dynamic_routes = true
}
