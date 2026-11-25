data "opper_project_model_list" "support" {
  project_uuid = "01234567-89ab-4cde-8012-3456789abcde"
}

output "support_follows_organization" {
  value = data.opper_project_model_list.support.follows_organization
}

output "support_model_list" {
  value = data.opper_project_model_list.support.compat_model_list
}
