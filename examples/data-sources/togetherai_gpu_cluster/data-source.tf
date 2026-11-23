variable "cluster_id" {
  type        = string
  description = "ID of a cluster created in another workspace."
}

data "togetherai_gpu_cluster" "training" {
  id = var.cluster_id
}

output "training_cluster_status" {
  value = data.togetherai_gpu_cluster.training.status
}

# Pass the kubeconfig to the kubernetes or helm provider, or write it to a
# file with local_sensitive_file.
output "training_kubeconfig" {
  value     = data.togetherai_gpu_cluster.training.kube_config
  sensitive = true
}
