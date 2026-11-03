data "opper_project" "existing" {
  id = "01234567-89ab-4cde-8012-3456789abcde"
}

output "project_name" {
  value = data.opper_project.existing.name
}
