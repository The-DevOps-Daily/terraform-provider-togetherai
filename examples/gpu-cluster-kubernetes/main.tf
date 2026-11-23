# One terraform apply: create an on-demand GPU cluster with a shared volume,
# wait until it is Ready, then use its kubeconfig to run a GPU smoke test
# (nvidia-smi) as a Kubernetes Job. terraform destroy removes everything.
#
# Cost: billed per GPU hour from creation until destroy. 8 x H100 on demand
# is about $32 per hour on the public price list, plus $0.16 per GiB per month
# for the volume. Destroy as soon as you are done.

terraform {
  required_providers {
    togetherai = {
      source = "the-devops-daily/togetherai"
    }
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "~> 2.38"
    }
    local = {
      source  = "hashicorp/local"
      version = "~> 2.5"
    }
  }
}

provider "togetherai" {}

variable "name" {
  type    = string
  default = "tf-gpu-demo"
}

variable "gpu_type" {
  type    = string
  default = "H100_SXM"
}

data "togetherai_gpu_cluster_regions" "all" {}

locals {
  region = [for r in data.togetherai_gpu_cluster_regions.all.regions : r if contains(r.supported_instance_types, var.gpu_type)][0]
}

resource "togetherai_gpu_cluster" "this" {
  name              = var.name
  cluster_type      = "KUBERNETES"
  region            = local.region.name
  gpu_type          = var.gpu_type
  billing_type      = "ON_DEMAND"
  num_gpus          = 8
  nvidia_version_id = local.region.driver_versions[0].id

  shared_volume = {
    name     = "${var.name}-data"
    size_tib = 1
  }

  timeouts = {
    create = "90m"
  }
}

# The kubeconfig is only returned to a project admin's API key.
locals {
  kubeconfig = yamldecode(togetherai_gpu_cluster.this.kube_config)
  k8s        = local.kubeconfig.clusters[0].cluster
  k8s_user   = local.kubeconfig.users[0].user
}

provider "kubernetes" {
  host                   = local.k8s.server
  cluster_ca_certificate = try(base64decode(local.k8s["certificate-authority-data"]), null)
  token                  = try(local.k8s_user.token, null)
  client_certificate     = try(base64decode(local.k8s_user["client-certificate-data"]), null)
  client_key             = try(base64decode(local.k8s_user["client-key-data"]), null)
}

resource "kubernetes_job_v1" "gpu_smoke_test" {
  metadata {
    name = "gpu-smoke-test"
  }
  spec {
    backoff_limit = 0
    template {
      metadata {}
      spec {
        restart_policy = "Never"
        container {
          name    = "nvidia-smi"
          image   = "nvidia/cuda:12.8.0-base-ubuntu22.04"
          command = ["nvidia-smi"]
          resources {
            limits = {
              "nvidia.com/gpu" = "1"
            }
          }
        }
      }
    }
  }
  wait_for_completion = true
  timeouts {
    create = "15m"
  }
}

# Also write the kubeconfig to a file for kubectl.
resource "local_sensitive_file" "kubeconfig" {
  content         = togetherai_gpu_cluster.this.kube_config
  filename        = "${path.module}/kubeconfig"
  file_permission = "0600"
}

output "cluster_id" {
  value = togetherai_gpu_cluster.this.id
}

output "kubectl" {
  value = "KUBECONFIG=${local_sensitive_file.kubeconfig.filename} kubectl get nodes"
}

output "kubeconfig" {
  value     = togetherai_gpu_cluster.this.kube_config
  sensitive = true
}
