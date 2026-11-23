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
    min_replicas      = 1
    max_replicas      = 3
    scale_down_window = "600s"
  }

  # Stop by itself after an hour without requests.
  inactive_timeout = 60
}
