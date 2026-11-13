terraform {
  required_version = ">= 1.11"
  required_providers {
    opper = {
      source  = "opper-ai/opper"
      version = "~> 0.2.0"
    }
  }
}

# allowed_ips was introduced in v0.2.0.
# Supply OPPER_MANAGEMENT_API_KEY privately with apikeys:read/write.
provider "opper" {}

variable "project_id" {
  type        = string
  description = "UUID of an existing project."
}

variable "allowed_ips" {
  type        = set(string)
  description = "Actual public egress IPs/CIDRs of runtime clients; at most 100 entries."
}

resource "opper_api_key" "app" {
  project_id  = var.project_id
  name        = "restricted-runtime"
  allowed_ips = var.allowed_ips
}

# List updates preserve the key ID and one-time secret. Omission preserves an
# existing key's list on refresh/update; pass [] to clear it intentionally.
# Keep this list explicit and fully known for every restricted replacement,
# including -replace, taint and recovery. Use a fresh idempotency_key if set.
# Allow up to one minute for runtime authentication caches to pick up updates.
# The runtime secret is stored in Terraform state; protect the state backend.
