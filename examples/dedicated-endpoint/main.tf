# A dedicated inference endpoint for a catalog model: endpoint, one
# autoscaled deployment, and a traffic split that routes all requests to it.
#
# Cost: billed per minute for each ready replica (one H100 replica is about
# $5.49 per hour). Set min_replicas = 0 and max_replicas = 0 to stop billing
# without deleting anything.

terraform {
  required_providers {
    togetherai = {
      source = "the-devops-daily/togetherai"
    }
  }
}

provider "togetherai" {}

variable "model" {
  type    = string
  default = "google/gemma-4-E4B-it"
}

variable "min_replicas" {
  type    = number
  default = 1
}

variable "max_replicas" {
  type    = number
  default = 2
}

data "togetherai_supported_models" "catalog" {
  search  = var.model
  product = "PRODUCT_DEDICATED"
}

locals {
  model   = one([for m in data.togetherai_supported_models.catalog.models : m if m.name == var.model])
  profile = local.model.deployment_profiles[0]
}

resource "togetherai_endpoint" "chat" {
  name = "chat"
}

resource "togetherai_deployment" "primary" {
  endpoint_id = togetherai_endpoint.chat.id
  name        = "primary"
  model       = local.profile.model
  config      = local.profile.config

  autoscaling = {
    min_replicas = var.min_replicas
    max_replicas = var.max_replicas
  }

  inactive_timeout = 60
}

resource "togetherai_endpoint_traffic_split" "chat" {
  endpoint_id = togetherai_endpoint.chat.id
  routes = [{
    deployment_id = togetherai_deployment.primary.id
    weight        = 1
  }]
}

output "hardware" {
  value = togetherai_deployment.primary.hardware
}

output "request" {
  value = <<-EOT
    curl -s https://api-inference.together.ai/v1/chat/completions \
      -H "Authorization: Bearer $TOGETHER_API_KEY" -H "Content-Type: application/json" \
      -d '{"model": "${togetherai_endpoint.chat.qualified_name}", "messages": [{"role": "user", "content": "Hello"}]}'
  EOT
}
