<#
.SYNOPSIS
    Register a PowerShell 7 PSRP endpoint, the twin of the 5.1 one
    New-AdProviderEndpoint.ps1 creates.

.DESCRIPTION
    Run on the management host as a LOCAL administrator, FROM POWERSHELL 7.

    New-AdProviderEndpoint.ps1 cannot make this endpoint, and refuses to try.

    An endpoint takes the engine of the shell that registers it, and that script
    is 5.1-only on purpose: a 5.1 endpoint with no RunAs admits a
    non-administrator caller and runs AS that caller, so a team account needs no
    privilege on the host. A PowerShell 7 endpoint does not. Without a RunAs
    identity it faults a non-admin caller with an opaque pwrshplugin HTTP 500 —
    which is also why pointing a caller at the built-in PowerShell.7 endpoint
    does not work. So this one runs under a virtual account (a local
    administrator on this host) and is registered FROM pwsh 7.

    That is a real difference in blast radius, and it is confined to this host:
    every directory call the provider makes still authenticates as the caller's
    own domain.credential, so Active Directory checks it against that account's
    OU delegation exactly as it does for the 5.1 endpoint. Size this host
    accordingly.

    PREREQUISITE: PowerShell 7 remoting must be enabled, or registration fails
    with "The WinRM plugin DLL pwrshplugin.dll is missing for PowerShell". Run
    this once, from pwsh 7:

        Enable-PSRemoting -Force

    It registers the built-in PowerShell.7 endpoint and installs the plugin this
    one needs. The 5.1 endpoints already present are left alone.

.PARAMETER GrantTo
    The AD group this endpoint is granted to. Use a group for the same reason
    New-AdProviderEndpoint.ps1 does: adding an account to it is then the whole
    onboarding step. Kerberos carries group membership in the ticket, so an
    account added to the group needs a FRESH ticket.

.EXAMPLE
    .\New-AdProviderEndpoint7.ps1 -GrantTo 'CORP\AD-Terraform-Objects'
#>
param([Parameter(Mandatory)][string]$GrantTo)
$ErrorActionPreference = 'Stop'

if ($PSVersionTable.PSVersion.Major -lt 7) { throw "Run this from pwsh 7: the endpoint takes this shell's engine." }
$me = [Security.Principal.WindowsPrincipal]::new([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $me.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw 'Run as a local administrator.' }

$sid = ([Security.Principal.NTAccount]$GrantTo).Translate([Security.Principal.SecurityIdentifier]).Value
Write-Output "==> $GrantTo -> $sid"

$name = 'AdObjects7'
$pssc = Join-Path $env:TEMP "$name.pssc"
Remove-Item $pssc -Force -ErrorAction SilentlyContinue

New-PSSessionConfigurationFile -Path $pssc `
    -SessionType Default -LanguageMode FullLanguage `
    -ModulesToImport ActiveDirectory `
    -RunAsVirtualAccount

try { Unregister-PSSessionConfiguration -Name $name -Force -ErrorAction Stop | Out-Null } catch { }
Register-PSSessionConfiguration -Name $name -Path $pssc -Force | Out-Null

# The same SDDL AdObjects51 gets: local administrators plus the granted group,
# and nothing else. No Remote Management Users, no Interactive Users -- a member
# of the group can open THIS endpoint and nothing else on the host.
$sddl = "O:NSG:BAD:P(A;;GA;;;BA)(A;;GA;;;$sid)S:P(AU;FA;GA;;;WD)"
Set-PSSessionConfiguration -Name $name -SecurityDescriptorSddl $sddl -Force -NoServiceRestart -WarningAction SilentlyContinue | Out-Null
Restart-Service WinRM -Force
Remove-Item $pssc -Force -ErrorAction SilentlyContinue

$c = Get-PSSessionConfiguration -Name $name
Write-Output ("    name={0} psVersion={1} runAsVirtualAccount={2}" -f $c.Name, $c.PSVersion, $c.RunAsVirtualAccount)
if ([version]$c.PSVersion -lt [version]'7.0') {
    throw "Endpoint $name registered as PowerShell $($c.PSVersion), not 7. It was registered from the wrong shell; the whole point of this endpoint is the 7 engine."
}
Write-Output 'OK'
