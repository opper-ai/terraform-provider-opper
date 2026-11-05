terraform {
  required_version = ">= 1.11"
}

variable "openai_credential" {
  type      = string
  sensitive = true
  ephemeral = true
}

resource "opper_custom_model" "assistant" {
  name                     = "team-assistant"
  identifier               = "openai/gpt-example"
  creation_idempotency_key = "team-assistant-create-1"
  credential_wo            = var.openai_credential
  credential_version       = 1
}
