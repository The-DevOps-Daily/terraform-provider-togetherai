terraform {
  required_providers {
    togetherai = {
      source = "the-devops-daily/togetherai"
    }
  }
}

# Reads TOGETHER_API_KEY (and optional TOGETHER_PROJECT_ID) from the
# environment. Never put the key in a .tf file.
provider "togetherai" {}
