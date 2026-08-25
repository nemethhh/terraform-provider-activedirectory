<#
.SYNOPSIS
    Create a PSRP endpoint on a management host for terraform-provider-activedirectory.

.DESCRIPTION
    Run on the management host as a LOCAL administrator (not a domain admin),
    FROM WINDOWS POWERSHELL 5.1, once per capability tier -- NOT once per team.

    Access is granted to an Active Directory GROUP. Onboarding a team afterwards
    is pure directory work with no change to this host:

        1. create the team's service account
        2. delegate its OU to that account
        3. add the account to the group this endpoint grants

    Verified end to end against the published provider (registry v0.7.0) from a
    Linux CI agent: a delegated account in the granted group created and destroyed
    an OU and a group, and was refused by the domain controller outside its OU.

.PARAMETER TierName
    Endpoint name; the value teams put in psrp.configuration_name. Name it for the
    capability tier, e.g. AdObjects or AdReadOnly.

.PARAMETER GrantTo
    The AD group (recommended) or single account allowed to open this endpoint.
    A group means new teams need no change here. Kerberos carries group membership
    in the ticket, so an account added to the group needs a FRESH ticket -- which
    a CI agent gets anyway, since it runs kinit per build.

.PARAMETER RestrictCmdlets
    Limit the session to the cmdlets these capabilities need.

    Understand what this is and is not. It is a guardrail against accident, not a
    sandbox. The session must be FullLanguage because the provider's scripts need
    it, and FullLanguage means arbitrary .NET -- both of these were verified
    working from inside a cmdlet-restricted session:

        [System.IO.File]::ReadAllText('C:\Windows\win.ini')
        ([adsi]'LDAP://dc.example.com/DC=example,DC=com').distinguishedName

    It also needs a go-adpwsh change that is not released: the script preamble
    calls Import-Module, and PowerShell refuses to make Import-Module visible in a
    restricted session. Until that lands, a restricted endpoint fails immediately
    with "The term 'Import-Module' is not recognized".

    The boundary you can rely on is the OU delegation, enforced by the domain
    controller against the account's own credential. Size the blast radius on this
    host accordingly: keep nothing else of value on it, and give mutually
    untrusting teams separate management hosts.

.EXAMPLE
    .\New-AdProviderEndpoint.ps1 -TierName AdObjects -GrantTo 'CORP\AD-Terraform-Objects' -Capability ou,group,user
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)][ValidatePattern('^[A-Za-z0-9._-]{1,64}$')][string] $TierName,
    [Parameter(Mandatory)][string] $GrantTo,

    [ValidateSet('ou','group','user','computer','gmsa','acl','replication','all')]
    [string[]] $Capability = @('all'),

    [switch] $RestrictCmdlets,
    [switch] $WhatIfOnly
)

$ErrorActionPreference = 'Stop'
function Step($m) { Write-Host "==> $m" -ForegroundColor Cyan }
function Note($m) { Write-Host "    $m" }
function Warn($m) { Write-Host "!!  $m" -ForegroundColor Yellow }

if ($PSVersionTable.PSVersion.Major -ne 5) {
    throw "Run this from Windows PowerShell 5.1 (powershell.exe). An endpoint takes the engine of the shell that registers it, and the 5.1 engine is the point: a 5.1 endpoint with no RunAs admits a non-administrator caller and runs as that caller, so team accounts need no privilege on this host. A PowerShell 7 endpoint faults for a non-admin caller unless it runs as a local-administrator virtual account."
}
$me = [Security.Principal.WindowsPrincipal]::new([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $me.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw 'Run as a local administrator.' }

# --- cmdlets per capability, taken from the library's script inventory -------

$core = @('ConvertFrom-Json','ConvertTo-Json','ConvertTo-SecureString','ForEach-Object',
          'Select-Object','Where-Object','Write-Output','Out-Default','Get-FormatData',
          'Exit-PSSession','Measure-Object','Get-Command')
$base = @('Get-ADRootDSE','Get-ADDomain','Get-ADDomainController','Get-ADForest',
          'Get-ADObject','Set-ADObject','Move-ADObject','Rename-ADObject')
$byCapability = @{
    ou          = @('Get-ADOrganizationalUnit','New-ADOrganizationalUnit','Set-ADOrganizationalUnit','Remove-ADOrganizationalUnit')
    group       = @('Get-ADGroup','New-ADGroup','Set-ADGroup','Remove-ADGroup','Get-ADGroupMember','Add-ADGroupMember','Remove-ADGroupMember')
    user        = @('Get-ADUser','New-ADUser','Set-ADUser','Remove-ADUser','Set-ADAccountPassword')
    computer    = @('Get-ADComputer','New-ADComputer','Set-ADComputer','Remove-ADComputer')
    gmsa        = @('Get-ADServiceAccount','New-ADServiceAccount','Set-ADServiceAccount','Remove-ADServiceAccount')
    acl         = @('Get-Acl','Set-Acl','New-PSDrive')
    replication = @('Sync-ADObject')
}
$caps    = if ($Capability -contains 'all') { $byCapability.Keys } else { $Capability }
$visible = ($core + $base + ($caps | ForEach-Object { $byCapability[$_] })) | Sort-Object -Unique
$providers = @('FileSystem','Function','Variable')
if ($caps -contains 'acl') { $providers += 'ActiveDirectory' }

# --- resolve the grantee to a SID -------------------------------------------
# By SID, so the descriptor never depends on name resolution again.

Step "Resolving $GrantTo"
$sid = ([Security.Principal.NTAccount]$GrantTo).Translate([Security.Principal.SecurityIdentifier]).Value
Note "SID $sid"
if ($sid -notmatch '^S-1-5-21-.*-\d+$') { Warn 'Grantee is not a domain principal; check the name.' }

if ($WhatIfOnly) {
    Write-Host ''
    Note "endpoint       : $TierName"
    Note "granted to     : $GrantTo ($sid)"
    Note 'language mode  : FullLanguage (required by the provider)'
    Note 'run as         : nobody -- the session runs as the connecting account itself'
    Note "capabilities   : $($caps -join ', ')"
    Note ("visible cmdlets: " + $(if ($RestrictCmdlets) { "$($visible.Count): $($visible -join ', ')" } else { 'unrestricted' }))
    return
}

# --- register ---------------------------------------------------------------

Step "Registering endpoint $TierName"
$pssc = Join-Path $env:TEMP "$TierName.pssc"
Remove-Item $pssc -Force -ErrorAction SilentlyContinue

$psscArgs = @{
    Path                = $pssc
    LanguageMode        = 'FullLanguage'
    ModulesToImport     = 'ActiveDirectory'
}
if ($RestrictCmdlets) {
    $psscArgs['VisibleCmdlets']   = $visible
    $psscArgs['VisibleProviders'] = $providers
    Warn 'Cmdlet restriction needs an unreleased go-adpwsh change. See the help in this file.'
}
New-PSSessionConfigurationFile @psscArgs

try { Unregister-PSSessionConfiguration -Name $TierName -Force -ErrorAction Stop | Out-Null } catch { }
Register-PSSessionConfiguration -Name $TierName -Path $pssc -Force | Out-Null

# Local administrators, plus the granted group. No Remote Management Users and no
# Interactive Users: members can open THIS endpoint and nothing else on the host.
$sddl = "O:NSG:BAD:P(A;;GA;;;BA)(A;;GA;;;$sid)S:P(AU;FA;GA;;;WD)"
Set-PSSessionConfiguration -Name $TierName -SecurityDescriptorSddl $sddl -Force -NoServiceRestart -WarningAction SilentlyContinue | Out-Null
Restart-Service WinRM -Force
Remove-Item $pssc -Force -ErrorAction SilentlyContinue

$c = Get-PSSessionConfiguration -Name $TierName
Note ("name={0} psVersion={1} language={2} virtualAccount={3}" -f $c.Name, $c.PSVersion, $c.LanguageMode, $c.RunAsVirtualAccount)
if ([version]$c.PSVersion -ne [version]'5.1') { Warn 'Not a PowerShell 5.1 endpoint. Re-run from Windows PowerShell 5.1 (powershell.exe).' }

# --- what the teams need ----------------------------------------------------

$dnsDomain = (Get-CimInstance Win32_ComputerSystem).Domain
$realm     = $dnsDomain.ToUpper()
$fqdn      = "$env:COMPUTERNAME.$dnsDomain".ToLower()

Write-Host ''
Write-Host "Endpoint $TierName ready. Onboard a team without touching this host:" -ForegroundColor Green
Write-Host @"

  New-ADUser        -Name svc_teamx -AccountPassword ... -Enabled `$true
  dsacls "OU=teamx,$(((Get-CimInstance Win32_ComputerSystem).Domain -split '\.' | ForEach-Object { "DC=$_" }) -join ',')" /I:T /G "DOMAIN\svc_teamx:GA"
  Add-ADGroupMember -Identity '$GrantTo' -Members svc_teamx

Their provider block (the credential appears twice on purpose: once to authenticate
to this host, once for the directory calls -- the second is what Active Directory
checks against their OU delegation):

  provider "activedirectory" {
    psrp {
      host               = "$fqdn"
      configuration_name = "$TierName"
      user               = "DOMAIN\\svc_teamx"
      password           = var.ad_password
      max_concurrency    = 4
    }

    domain {
      credential {
        username = "DOMAIN\\svc_teamx"
        password = var.ad_password
      }
    }
  }

configuration_name is required, not optional: the provider defaults to the
PowerShell.7 endpoint, which will refuse this account.

On the Linux CI agent, before terraform runs:

  cat > `$WORKSPACE/krb5.conf <<'EOF'
  [libdefaults]
    default_realm = $realm
    dns_lookup_kdc = false
    dns_lookup_realm = false
  [realms]
    $realm = { kdc = <dc-ip> }
  [domain_realm]
    .$dnsDomain = $realm
    $dnsDomain = $realm
  EOF
  export KRB5_CONFIG=`$WORKSPACE/krb5.conf KRB5CCNAME=FILE:`$WORKSPACE/ccache
  printf '%s' "`$AD_PASSWORD" | kinit svc_teamx@$realm

If $fqdn does not resolve from the agent, set psrp.host to the IP and add
spn = "HTTP/$fqdn".
"@
