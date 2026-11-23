data "togetherai_supported_models" "gemma" {
  search  = "gemma-4-E4B-it"
  product = "PRODUCT_DEDICATED"
}

locals {
  profile = data.togetherai_supported_models.gemma.models[0].deployment_profiles[0]
}

resource "togetherai_endpoint" "chat" {
  name = "chat"
}

resource "togetherai_deployment" "stable" {
  endpoint_id = togetherai_endpoint.chat.id
  name        = "gemma-stable"
  model       = local.profile.model
  config      = local.profile.config

  autoscaling = {
    min_replicas = 1
    max_replicas = 1
  }
}

resource "togetherai_deployment" "canary" {
  endpoint_id = togetherai_endpoint.chat.id
  name        = "gemma-canary"
  model       = local.profile.model
  config      = local.profile.config

  autoscaling = {
    min_replicas = 1
    max_replicas = 1
  }
}

# Canary: 90% of requests to the stable deployment, 10% to the new one.
resource "togetherai_endpoint_traffic_split" "chat" {
  endpoint_id = togetherai_endpoint.chat.id

  routes = [
    {
      deployment_id = togetherai_deployment.stable.id
      weight        = 9
    },
    {
      deployment_id = togetherai_deployment.canary.id
      weight        = 1
    },
  ]
}
