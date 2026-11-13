# initial_access was introduced in v0.2.0.
terraform {
  required_version = ">= 1.11"
  required_providers {
    opper = {
      source  = "opper-ai/opper"
      version = "~> 0.2.0"
    }
  }
}

# Supply OPPER_MANAGEMENT_API_KEY privately with projects:read/write/delete and
# project_access:write. Requires the /management/v1/projects/with-access API.
provider "opper" {}

resource "opper_project" "reporting" {
  name        = "restricted-reporting"
  description = "Restricted from creation"

  initial_access = {
    mode      = "restricted"
    role_keys = ["viewer"]
    # user_uuids defaults to []; use active member UUIDs for direct grants.
  }
}

# initial_access is creation history, not the current policy. Metadata updates
# ignore subsequent policy edits; removing this snapshot retains that policy.
# For ongoing policy management, explicitly import the created UUID into a
# separate opper_project_access resource and review another plan.
output "project_uuid" {
  value = opper_project.reporting.id
}
