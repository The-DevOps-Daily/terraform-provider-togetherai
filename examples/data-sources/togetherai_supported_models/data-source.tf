data "togetherai_supported_models" "dedicated" {
  product  = "PRODUCT_DEDICATED"
  modality = "MODALITY_TEXT"
  search   = "qwen"
}

output "profiles" {
  value = {
    for m in data.togetherai_supported_models.dedicated.models :
    m.name => [for p in m.deployment_profiles : "${p.gpu_count}x${p.gpu_type} ${p.quantization}: ${p.config}"]
  }
}
