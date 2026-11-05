terraform {
  required_providers {
    opper = { source = "opper-ai/opper" }
  }
}

variable "organization_uuid" { type = string }
variable "member_uuid" { type = string }

provider "opper" {}

data "opper_role" "viewer" {
  organization_uuid = var.organization_uuid
  name              = "viewer"
}

data "opper_roles" "catalog" {
  organization_uuid = var.organization_uuid
}

resource "opper_custom_role" "reporting" {
  organization_uuid        = var.organization_uuid
  display_name             = "Reporting"
  permissions              = ["usage:read"]
  creation_idempotency_key = "reporting-20261105-1"
}

resource "opper_member_role" "reporter" {
  organization_uuid         = var.organization_uuid
  member_uuid               = var.member_uuid
  role                      = opper_custom_role.reporting.name
  role_change_token         = opper_custom_role.reporting.change_token
  destroy_behavior          = "assign_role"
  destroy_role              = "viewer"
  destroy_role_change_token = data.opper_role.viewer.change_token
}

data "opper_member_role" "reporter" {
  organization_uuid = var.organization_uuid
  member_uuid       = var.member_uuid
  depends_on        = [opper_member_role.reporter]
}
