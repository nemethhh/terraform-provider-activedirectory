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
#
# ConstrainedLanguage sandbox endpoint. The admin registers a locked-down
# management-host endpoint with scripts/host/New-AdProviderEndpoint.ps1 -Sandbox,
# which confines a delegated team account to AD-management cmdlets with no host
# access. The team's block sets language_mode = "constrained" and passes its own
# credential twice: psrp.user/password authenticates to the host, and
# domain.credential is what the domain controller checks against the account's OU
# delegation. ACL delegation is unavailable in constrained mode — a full-language
# endpoint is required for that.
# provider "activedirectory" {
#   psrp {
#     host               = "mgmt.corp.local"
#     configuration_name = "AdSandbox" # the -Sandbox tier name
#     language_mode      = "constrained"
#     user               = "CORP\\svc_teamx"
#     password           = var.svc_password
#   }
#   domain {
#     credential {
#       username = "CORP\\svc_teamx"
#       password = var.svc_password
#     }
#   }
# }
