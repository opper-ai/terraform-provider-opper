terraform {
  required_providers {
    opper = { source = "opper-ai/opper" }
  }
}

# Supply OPPER_MANAGEMENT_API_KEY privately with project_access:read and
# project_access:write. The organization must have policy writes enabled.
provider "opper" {}

# Replace these UUIDs with an existing organization and project. Import first
# if its current policy is Restricted. No project is created by this example.
resource "opper_project_access" "reporting" {
  organization_uuid = "01234567-89ab-4cde-8012-3456789abcde"
  project_uuid      = "12345678-9abc-4def-8123-456789abcdef"
  mode              = "restricted"
  role_keys         = ["viewer"]
  user_uuids        = []
}

# Destroy relinquishes Terraform ownership and retains the policy. To reopen
# access, apply mode = "organization" with both recipient sets empty instead.
# These UUIDs cannot change after adoption. For another existing project, use
# a new resource address and retain this block until the new apply succeeds.
