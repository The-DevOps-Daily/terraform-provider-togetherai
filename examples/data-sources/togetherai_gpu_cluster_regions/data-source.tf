data "togetherai_gpu_cluster_regions" "all" {}

output "h100_regions" {
  value = [for r in data.togetherai_gpu_cluster_regions.all.regions : r.name if contains(r.supported_instance_types, "H100_SXM")]
}
