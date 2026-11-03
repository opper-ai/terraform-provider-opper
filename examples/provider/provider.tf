terraform {
  required_providers {
    opper = {
      source = "opper-ai/opper"
    }
  }
}

# Set OPPER_MANAGEMENT_API_KEY privately in the environment.
provider "opper" {}
