# Management host provisioning

The provider runs PowerShell on a management host and reaches Active Directory
from there. These scripts configure such a host so that a delegated service
account can run Terraform against it while holding **no privilege on the host
itself**.

Run them as a **local administrator** on the management host, from **Windows
PowerShell 5.1**. Domain administrator rights are not needed here; they are
needed in the directory, to create accounts and delegate OUs.

| Script | When | What |
|---|---|---|
| `Initialize-AdProvisioningHost.ps1` | once per host | RSAT-AD, WinRM hardening, built-in endpoints restricted to administrators, firewall, quotas, logon-right denials |
| `New-AdProviderEndpoint.ps1` | once per capability **tier** | a 5.1 session configuration granted to one AD group |
| `New-AdProviderEndpoint.ps1 -Sandbox` | once per capability **tier**, for untrusted teams | a ConstrainedLanguage sandbox tier; requires `winrm.language_mode = "constrained"` |
| `Remove-AdProviderEndpoint.ps1` | to revoke a tier | unregisters it |

Onboarding a team afterwards touches only the directory:

1. create the team's service account
2. delegate its OU to that account (`dsacls "OU=teamx,DC=..." /I:T /G "DOMAIN\svc_teamx:GA"`)
3. add the account to the group the endpoint grants

Two properties make this safe, both established by testing against a real domain:

- **A 5.1 endpoint with no RunAs runs as the connecting account.** A PowerShell 7
  endpoint instead faults for a non-administrator caller unless it runs as a
  virtual account, which is a local administrator — so on 7 the team would gain
  local-administrator code execution on this host. That is why these scripts
  register 5.1 endpoints and set no RunAs identity.
- **Authorization lives in Active Directory.** The provider passes the account's
  own credential to every cmdlet, so a write outside its delegated OU is refused
  by the domain controller, not by this host.

`-RestrictCmdlets` limits the session to the cmdlets a capability needs. It is a
guardrail against accident, not a sandbox: the session is `FullLanguage` because
the provider's scripts require it, and `FullLanguage` permits arbitrary .NET.
For a real sandbox, use `-Sandbox` instead (below).

`-Sandbox` registers a real sandbox: a `ConstrainedLanguage` endpoint, always
with restricted cmdlet/provider visibility regardless of `-RestrictCmdlets`, and
exposing **only stock cmdlets** — no bespoke functions. The library preamble
builds its credential with `[PSCredential]::new` + `ConvertTo-SecureString`
(both visible in the core set); `ConstrainedLanguage` allows both, because
`PSCredential` and `SecureString` are on its "core type" list, so no
credential-builder function is needed. What `ConstrainedLanguage` genuinely
blocks — `[Console]` payload delivery and the ACL cmdlets'
`[DirectoryServices]`/`New-PSDrive` .NET — the provider avoids in constrained
mode (a different delivery path) or the endpoint drops (the ACL capability is
excluded from a sandbox tier, so delegation work needs a separate, non-sandbox
tier). Teams pointed at a sandbox endpoint must set the provider's
`winrm.language_mode = "constrained"`.
