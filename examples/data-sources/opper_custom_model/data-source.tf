data "opper_custom_model" "support" {
  id = "11111111-1111-4111-8111-111111111111"
}

output "support_model_name" {
  value = data.opper_custom_model.support.name
}
