data "opper_custom_models" "all" {}

output "custom_model_ids" {
  value = [for model in data.opper_custom_models.all.models : model.id]
}
