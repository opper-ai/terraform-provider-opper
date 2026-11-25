resource "opper_project" "support" {
  name = "Support bot"
}

# The project's API keys see the recommended models and the organization's
# dynamic routes, and can only call what that list shows.
resource "opper_project_model_list" "support" {
  project_uuid               = opper_project.support.id
  compat_model_list          = "recommended"
  compat_model_list_enforced = true
}
