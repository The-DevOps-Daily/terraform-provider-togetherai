data "togetherai_gpu_cluster_regions" "all" {}

locals {
  # The first region that offers H100s, and its newest driver catalog entry.
  region = [for r in data.togetherai_gpu_cluster_regions.all.regions : r if contains(r.supported_instance_types, "H100_SXM")][0]
}

resource "togetherai_gpu_cluster" "training" {
  name              = "training"
  cluster_type      = "KUBERNETES"
  region            = local.region.name
  gpu_type          = "H100_SXM"
  billing_type      = "ON_DEMAND"
  num_gpus          = 8
  nvidia_version_id = local.region.driver_versions[0].id

  # Create the shared volume with the cluster so it lands in the same datacenter.
  shared_volume = {
    name     = "training-data"
    size_tib = 1
  }

  timeouts = {
    create = "90m"
  }
}

output "kubeconfig" {
  value     = togetherai_gpu_cluster.training.kube_config
  sensitive = true
}
