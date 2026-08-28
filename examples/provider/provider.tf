terraform {
  required_providers {
    activedirectory = {
      source  = "nemethhh/activedirectory"
      version = "~> 0.9"
    }
  }
  required_version = ">= 1.11"
}

# Two independent axes decide how the AD cmdlets run:
#
#   * transport — where pwsh runs and how bytes reach it: local, ssh or winrm.
#                 Exactly one block is required; there is no implicit default.
#   * mode      — how pwsh is driven once a channel exists. "warm" (default)
#                 keeps a persistent PowerShell 7 runspace so process startup and
#                 Import-Module ActiveDirectory are paid once and amortized;
#                 "cold" runs a fresh pwsh -EncodedCommand for every operation.
#
# The domain block is a third, orthogonal axis: it pins a DC and supplies the
# AD credential, independent of which transport carries the session.

# Terraform runs on a domain-joined Windows host and spawns pwsh there. The
# process inherits that machine's logon token, so Active Directory operations
# authenticate as whoever launched Terraform and there is no hop to delegate.
provider "activedirectory" {
  local {
    pwsh_path       = "pwsh"
    max_concurrency = 4
    mode            = "warm" # default; set "cold" for one pwsh per operation
  }

  domain {
    server = "dc01.corp.local" # omit to discover one at configure time
  }
}

# To run Terraform anywhere and reach a Windows jump box over SSH instead,
# replace the local block with an ssh block. warm (the default) needs
# PowerShell 7 and the `powershell` sshd subsystem on the jump box; set
# mode = "cold" for a Windows PowerShell 5.1 jump box.
#
#   ssh {
#     host             = "jump.corp.local"
#     user             = "svc_tf"
#     private_key_path = "~/.ssh/tf_ad"
#     known_hosts_file = "~/.ssh/known_hosts"
#     mode             = "warm"
#   }
#
# Over SSH, a public-key session gets a network logon token with no delegatable
# credentials, so onward authentication to AD Web Services fails — the classic
# double hop. Add a domain.credential block to work around it.

# Alternatively, reach a domain controller over WinRM with Kerberos. warm (the
# default) keeps a persistent PSRP runspace and needs a registered PSRP session
# configuration (configuration_name). mode = "cold" opens a fresh Windows Remote
# Shell per operation and feeds the script on stdin to powershell -EncodedCommand
# (Windows PowerShell 5.1) — slower, but it needs NO server-side PSRP session
# configuration, so it fits a host where PSRP remoting is disabled but WinRS is
# allowed. For cold, the winrm.user must have WinRS shell access (Remote
# Management Users, or admin).
# provider "activedirectory" {
#   winrm {
#     host = "dc1.corp.local" # an FQDN; SPN defaults to HTTP/dc1.corp.local
#     # mode = "cold"          # no PSRP endpoint needed; user needs WinRS access
#   }
# }
#
# From a member/management host, add domain.credential to cross the double hop:
# provider "activedirectory" {
#   winrm { host = "mgmt.corp.local" }
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
# credential twice: winrm.user/password authenticates to the host, and
# domain.credential is what the domain controller checks against the account's OU
# delegation. ACL delegation is unavailable in constrained mode — a full-language
# endpoint is required for that.
# provider "activedirectory" {
#   winrm {
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
