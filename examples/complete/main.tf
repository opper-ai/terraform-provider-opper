terraform {
  required_providers {
    opper = {
      source = "opper-ai/opper"
    }
  }
}

# Set OPPER_MANAGEMENT_API_KEY privately in the environment.
provider "opper" {}

variable "model_id" {
  type        = string
  description = "Exact currently available model ID from the Opper catalog, permitted by your organization's policy."

  validation {
    condition     = length(trimspace(var.model_id)) > 0
    error_message = "Provide a currently available model ID from the Opper catalog."
  }
}

variable "enable_budget" {
  type        = bool
  description = "Enable the project-scoped USD 100 monthly spend limit. Creating even a disabled rule requires the relevant Opper plan."
  default     = false
}

resource "opper_project" "app" {
  name        = "terraform-example-service"
  description = "Example service managed with Terraform"
}

resource "opper_api_key" "app" {
  project_id = opper_project.app.id
  name       = "terraform-example-service"

  # Protect this runtime credential in an encrypted, access-controlled state
  # backend. The sensitive output below still exists in Terraform state.
}

resource "opper_rule" "app_budget" {
  kind    = "comply"
  name    = "Example project monthly budget"
  enabled = var.enable_budget

  scope = {
    type          = "projects"
    project_uuids = [opper_project.app.id]
  }

  config_json = jsonencode({
    type                = "budget"
    monthly_limit_cents = 10000
  })
}

resource "opper_dynamic_route" "app" {
  name         = "terraform-example-router"
  display_name = "Example service router"
  description  = "Draft route; Terraform does not deploy it"

  graph_json = jsonencode({
    elements = [
      {
        id      = "START"
        type    = "start"
        outputs = { next = { elementId = "pool" } }
      },
      {
        id   = "pool"
        type = "pool"
        properties = {
          order  = "cost"
          models = [{ model = var.model_id }]
        }
        outputs = {
          success  = { elementId = "END" }
          fallback = { elementId = "END" }
        }
      },
      {
        id      = "END"
        type    = "end"
        outputs = {}
      }
    ]
  })
}

output "project_id" {
  value = opper_project.app.id
}

output "runtime_api_key" {
  value       = opper_api_key.app.secret
  sensitive   = true
  description = "One-time runtime secret stored in Terraform state. Do not print it in logs."
}

output "route_invocation_model" {
  value       = opper_dynamic_route.app.invocation_model
  description = "Route invocation name. It serves calls only after deployment outside Terraform."
}
