terraform {
  required_providers {
    activedirectory = {
      source  = "nemethhh/activedirectory"
      version = "~> 0.2"
    }
  }
  required_version = ">= 1.11"
}

# Terraform runs on a domain-joined Windows host and spawns pwsh there. The
# process inherits that machine's logon token, so Active Directory operations
# authenticate as whoever launched Terraform and there is no hop to delegate.
provider "activedirectory" {
  local {
    pwsh_path       = "pwsh"
    max_concurrency = 4
  }

  domain {
    server = "dc01.corp.local" # omit to discover one at configure time
  }
}

# To run Terraform anywhere and reach a Windows jump box over SSH instead,
# replace the local block with an ssh block. Exactly one of the three
# (local/ssh/psrp) is required, and there is no implicit default:
#
#   ssh {
#     host             = "jump.corp.local"
#     user             = "svc_tf"
#     private_key_path = "~/.ssh/tf_ad"
#     known_hosts_file = "~/.ssh/known_hosts"
#   }
#
# Over SSH, a public-key session gets a network logon token with no delegatable
# credentials, so onward authentication to AD Web Services fails — the classic
# double hop. Add a domain.credential block to work around it.

# Alternatively, reach a domain controller over PSRP/WinRM with Kerberos:
# provider "activedirectory" {
#   psrp {
#     host = "dc1.corp.local" # an FQDN; SPN defaults to HTTP/dc1.corp.local
#     # language_mode = "constrained"  # sandbox endpoint from New-AdProviderEndpoint.ps1 -Sandbox; ACL ops need "full"
#   }
# }
#
# From a member/management host, add domain.credential to cross the double hop:
# provider "activedirectory" {
#   psrp { host = "mgmt.corp.local" }
#   domain {
#     credential {
#       username = "CORP\\svc_tf"
#       password = var.svc_password
#     }
#   }
# }
