data "togetherai_projects" "all" {}

variable "project_slug" {
  type    = string
  default = "production"
}

locals {
  # Fails at plan time if no project has this slug.
  project_id = one([for p in data.togetherai_projects.all.projects : p.id if p.slug == var.project_slug])
}

resource "togetherai_endpoint" "chat" {
  project_id = local.project_id
  name       = "chat"
}
