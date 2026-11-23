resource "togetherai_shared_volume" "datasets" {
  name     = "datasets"
  size_tib = 2
  region   = "us-central-8"
}

# Attach it to a cluster in the same region.
resource "togetherai_gpu_cluster" "batch" {
  name                  = "batch"
  region                = togetherai_shared_volume.datasets.region
  gpu_type              = "H100_SXM"
  billing_type          = "ON_DEMAND"
  num_gpus              = 8
  cuda_version          = "12.8"
  nvidia_driver_version = "570"
  volume_id             = togetherai_shared_volume.datasets.id
}
