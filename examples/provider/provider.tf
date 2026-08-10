terraform {
  required_providers {
    activedirectory = {
      source  = "nemethhh/activedirectory"
      version = "~> 0.1"
    }
  }
  required_version = ">= 1.11"
}

provider "activedirectory" {
  ssh {
    host             = "jump.corp.local"
    user             = "svc_tf"
    private_key_path = "~/.ssh/tf_ad"
    known_hosts_file = "~/.ssh/known_hosts"
    max_concurrency  = 4
  }

  domain {
    server = "dc01.corp.local"
  }
}
