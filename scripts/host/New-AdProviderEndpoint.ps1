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
    Endpoint name; the value teams put in winrm.configuration_name. Name it for the
    capability tier, e.g. AdObjects or AdReadOnly.

.PARAMETER GrantTo
    The AD group this endpoint is granted to. Use a group: adding a team's
    service account to it is then the entire onboarding step, with no change
    to this host. Kerberos carries group membership in the ticket, so an
    account added to the group needs a FRESH ticket -- which a CI agent gets
    anyway, since it runs kinit per build.

    A single account is also accepted -- Translate() resolves either a group
    or a user SID -- but a per-account grant forfeits that property: onboard
    the next team and you are back here re-running this script instead of
    just adding an account to a group.

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

.PARAMETER Sandbox
    Register a real sandbox: the endpoint runs ConstrainedLanguage, not
    FullLanguage. Cmdlet and provider visibility are always restricted in this
    mode (regardless of -RestrictCmdlets).

    The endpoint exposes only stock cmdlets -- no bespoke functions, with one
    exception: ACL delegation is available in a sandbox endpoint registered with
    -Capability acl. The library preamble builds its PSCredential with
    [PSCredential]::new + ConvertTo-SecureString (in the visible core set);
    ConstrainedLanguage allows both, because PSCredential and SecureString are on
    its "core type" list, so no credential-builder function is needed. What
    ConstrainedLanguage does block -- [Console] payload delivery and the ACL
    cmdlets' [DirectoryServices]/New-PSDrive .NET -- the provider avoids in
    constrained mode (a different delivery path) or, for ACL, works around: when
    -Capability acl is requested, this script installs Set-AdAce/Remove-AdAce/
    Get-AdAce as -FunctionDefinitions synced from go-adpwsh's
    adpwsh.ACLEndpointHelpers(), which run FullLanguage inside the CLM session and
    do the .NET ACL work the CLM caller itself cannot.

    Teams pointed at this endpoint must set the provider's
    winrm.language_mode = "constrained".

.EXAMPLE
    .\New-AdProviderEndpoint.ps1 -TierName AdObjects -GrantTo 'CORP\AD-Terraform-Objects' -Capability ou,group,user

.EXAMPLE
    .\New-AdProviderEndpoint.ps1 -TierName AdSandbox -GrantTo 'CORP\AD-Terraform-Sandbox' -Capability ou,group,user -Sandbox
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)][ValidatePattern('^[A-Za-z0-9._-]{1,64}$')][string] $TierName,
    [Parameter(Mandatory)][string] $GrantTo,

    [ValidateSet('ou','group','user','computer','gmsa','acl','replication','all')]
    [string[]] $Capability = @('all'),

    [switch] $RestrictCmdlets,
    [switch] $Sandbox,
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
          'Exit-PSSession','Measure-Object','Get-Command','Get-Module')
$base = @('Get-ADRootDSE','Get-ADDomain','Get-ADDomainController','Get-ADForest',
          'Get-ADObject','Set-ADObject','Move-ADObject','Rename-ADObject')
$byCapability = @{
    ou          = @('Get-ADOrganizationalUnit','New-ADOrganizationalUnit','Set-ADOrganizationalUnit','Remove-ADOrganizationalUnit')
    group       = @('Get-ADGroup','New-ADGroup','Set-ADGroup','Remove-ADGroup','Get-ADGroupMember','Add-ADGroupMember','Remove-ADGroupMember')
    user        = @('Get-ADUser','New-ADUser','Set-ADUser','Remove-ADUser','Set-ADAccountPassword')
    computer    = @('Get-ADComputer','New-ADComputer','Set-ADComputer','Remove-ADComputer')
    gmsa        = @('Get-ADServiceAccount','New-ADServiceAccount','Set-ADServiceAccount','Remove-ADServiceAccount')
    acl         = @('Get-Acl','Set-Acl','New-PSDrive','Remove-PSDrive')
    replication = @('Sync-ADObject')
}
$caps    = if ($Capability -contains 'all') { $byCapability.Keys } else { $Capability }
$visible = ($core + $base + ($caps | ForEach-Object { $byCapability[$_] })) | Sort-Object -Unique
$providers = @('FileSystem','Function','Variable')
if ($caps -contains 'acl') { $providers += 'ActiveDirectory' }

# >>> go-adpwsh ACL endpoint helpers (synced from adpwsh.ACLEndpointHelpers; do not edit by hand) >>>
$aclFunctionDefinitions = @(
    # go-adpwsh ACL endpoint helpers. A ConstrainedLanguage endpoint installs these
    # as -FunctionDefinitions, so each runs FullLanguage inside the CLM session and
    # can construct the .NET ACL types the CLM caller cannot. Mirrors
    # ops/acl_{grant,read,revoke}.ps1. SINGLE SOURCE OF TRUTH: the provider's
    # New-AdProviderEndpoint.ps1 embeds a verbatim copy, guarded by a drift test.
    @{ Name = 'Set-AdAce'; ScriptBlock = {
        param([Parameter(Mandatory)]$Target,[Parameter(Mandatory)]$Server,[pscredential]$Credential,[Parameter(Mandatory)]$Aces)
        $credOnly = @{}; if ($Credential) { $credOnly['Credential'] = $Credential }
        $dn = (Get-ADObject -Identity $Target -Properties distinguishedName -Server $Server @credOnly).DistinguishedName
        $drive = "TFAD$PID"
        $null = New-PSDrive -Name $drive -PSProvider ActiveDirectory -Root '' -Server $Server @credOnly -ErrorAction Stop
        try {
            $path = "$($drive):$dn"
            $acl = Get-Acl -Path $path
            foreach ($ace in $Aces) {
                $sid     = [System.Security.Principal.SecurityIdentifier]::new([string]$ace.trustee)
                $rights  = [System.DirectoryServices.ActiveDirectoryRights](@($ace.rights) -join ', ')
                $type    = [System.Security.AccessControl.AccessControlType][string]$ace.type
                $inh     = [System.DirectoryServices.ActiveDirectorySecurityInheritance][string]$ace.inheritance
                $objType = if ($ace.objectType) { [Guid][string]$ace.objectType } else { [Guid]::Empty }
                $inhType = if ($ace.inheritedObjectType) { [Guid][string]$ace.inheritedObjectType } else { [Guid]::Empty }
                $rule = [System.DirectoryServices.ActiveDirectoryAccessRule]::new($sid,$rights,$type,$objType,$inh,$inhType)
                $acl.AddAccessRule($rule)
            }
            Set-Acl -Path $path -AclObject $acl
            $obj = Get-ADObject -Identity $dn -Properties objectGUID -Server $Server @credOnly
            [ordered]@{ granted = $true; guid = $obj.ObjectGUID.ToString() }
        } finally { Remove-PSDrive -Name $drive -ErrorAction SilentlyContinue }
    }}
    @{ Name = 'Remove-AdAce'; ScriptBlock = {
        param([Parameter(Mandatory)]$Target,[Parameter(Mandatory)]$Server,[pscredential]$Credential,[Parameter(Mandatory)]$Aces)
        $credOnly = @{}; if ($Credential) { $credOnly['Credential'] = $Credential }
        $dn = (Get-ADObject -Identity $Target -Properties distinguishedName -Server $Server @credOnly).DistinguishedName
        $drive = "TFAD$PID"
        $null = New-PSDrive -Name $drive -PSProvider ActiveDirectory -Root '' -Server $Server @credOnly -ErrorAction Stop
        try {
            $path = "$($drive):$dn"
            $acl = Get-Acl -Path $path
            foreach ($ace in $Aces) {
                $sid     = [System.Security.Principal.SecurityIdentifier]::new([string]$ace.trustee)
                $rights  = [System.DirectoryServices.ActiveDirectoryRights](@($ace.rights) -join ', ')
                $type    = [System.Security.AccessControl.AccessControlType][string]$ace.type
                $inh     = [System.DirectoryServices.ActiveDirectorySecurityInheritance][string]$ace.inheritance
                $objType = if ($ace.objectType) { [Guid][string]$ace.objectType } else { [Guid]::Empty }
                $inhType = if ($ace.inheritedObjectType) { [Guid][string]$ace.inheritedObjectType } else { [Guid]::Empty }
                $rule = [System.DirectoryServices.ActiveDirectoryAccessRule]::new($sid,$rights,$type,$objType,$inh,$inhType)
                $null = $acl.RemoveAccessRule($rule)
            }
            Set-Acl -Path $path -AclObject $acl
            $obj = Get-ADObject -Identity $dn -Properties objectGUID -Server $Server @credOnly
            [ordered]@{ revoked = $true; guid = $obj.ObjectGUID.ToString() }
        } finally { Remove-PSDrive -Name $drive -ErrorAction SilentlyContinue }
    }}
    @{ Name = 'Get-AdAce'; ScriptBlock = {
        param([Parameter(Mandatory)]$Target,[Parameter(Mandatory)]$Server,[pscredential]$Credential)
        $credOnly = @{}; if ($Credential) { $credOnly['Credential'] = $Credential }
        $dn = (Get-ADObject -Identity $Target -Properties distinguishedName -Server $Server @credOnly).DistinguishedName
        $drive = "TFAD$PID"
        $null = New-PSDrive -Name $drive -PSProvider ActiveDirectory -Root '' -Server $Server @credOnly -ErrorAction Stop
        try {
            $acl = Get-Acl -Path "$($drive):$dn"
            $aces = foreach ($a in $acl.Access) {
                $sid = try { $a.IdentityReference.Translate([System.Security.Principal.SecurityIdentifier]).Value } catch { $a.IdentityReference.Value }
                [ordered]@{
                    trustee             = $sid
                    type                = $a.AccessControlType.ToString()
                    rights              = @($a.ActiveDirectoryRights.ToString() -split ',\s*')
                    objectType          = $a.ObjectType.ToString()
                    inheritedObjectType = $a.InheritedObjectType.ToString()
                    inheritance         = $a.InheritanceType.ToString()
                    inherited           = [bool]$a.IsInherited
                }
            }
            [ordered]@{ aces = @($aces) }
        } finally { Remove-PSDrive -Name $drive -ErrorAction SilentlyContinue }
    }}
)
# <<< go-adpwsh ACL endpoint helpers <<<

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
    Note ("language mode  : " + $(if ($Sandbox) { 'ConstrainedLanguage (sandbox)' } else { 'FullLanguage' }))
    Note 'run as         : nobody -- the session runs as the connecting account itself'
    Note "capabilities   : $($caps -join ', ')"
    Note ("visible cmdlets: " + $(if ($RestrictCmdlets -or $Sandbox) { "$($visible.Count): $($visible -join ', ')" } else { 'unrestricted' }))
    return
}

# --- register ---------------------------------------------------------------

Step "Registering endpoint $TierName"
$pssc = Join-Path $env:TEMP "$TierName.pssc"
Remove-Item $pssc -Force -ErrorAction SilentlyContinue

$psscArgs = @{
    Path            = $pssc
    LanguageMode    = if ($Sandbox) { 'ConstrainedLanguage' } else { 'FullLanguage' }
    ModulesToImport = 'ActiveDirectory'
}
if ($Sandbox) {
    # [string[]] casts are load-bearing on Windows PowerShell 5.1: $visible comes
    # from Sort-Object, so it is an Object[], and New-PSSessionConfigurationFile
    # 5.1 rejects an Object[] here with the *misleading* error "The member
    # 'ModulesToImport' must be an array consisting of either string or hashtable
    # elements." Casting to string[] is what actually silences it (lab-verified).
    $psscArgs['VisibleCmdlets']      = [string[]]$visible   # always restricted in a sandbox
    $psscArgs['VisibleProviders']    = [string[]]$providers
    # No custom functions for the base credential path: the provider builds its
    # PSCredential with the stock [PSCredential] constructor + ConvertTo-SecureString
    # (in $core, so visible), both of which ConstrainedLanguage allows (PSCredential/
    # SecureString are core types). The one exception is ACL delegation: the ACL
    # cmdlets need .NET ConstrainedLanguage blocks, so when acl is requested the
    # synced FunctionDefinitions below install FullLanguage-trusted helper functions
    # (Set-AdAce/Remove-AdAce/Get-AdAce) that do that work on the CLM caller's behalf.
    if ($caps -contains 'acl') { $psscArgs['FunctionDefinitions'] = $aclFunctionDefinitions }
} elseif ($RestrictCmdlets) {
    # See the [string[]] note above -- same 5.1 Object[] pitfall.
    $psscArgs['VisibleCmdlets']   = [string[]]$visible
    $psscArgs['VisibleProviders'] = [string[]]$providers
    # Warned about prominently in the closing banner below, not just here --
    # a warning this early in the output is easy to scroll past.
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
if ([version]$c.PSVersion -ne [version]'5.1') {
    throw "Endpoint $TierName registered as PowerShell $($c.PSVersion), not 5.1. A non-5.1 endpoint changes the whole security story this script exists for (see the PowerShell-7-RunAs warning at the top of this file), so this is not a warning to note and move past -- re-run this script from Windows PowerShell 5.1 (powershell.exe)."
}

# --- what the teams need ----------------------------------------------------

$dnsDomain = (Get-CimInstance Win32_ComputerSystem).Domain
$realm     = $dnsDomain.ToUpper()
$fqdn      = "$env:COMPUTERNAME.$dnsDomain".ToLower()
# A -Sandbox endpoint is ConstrainedLanguage, so the team's provider block MUST
# set language_mode = "constrained" or the provider sends a full-language wrapper
# the endpoint rejects. Rendered into the example block and its explanation below.
$langModeLine = if ($Sandbox) { "`n      language_mode      = ""constrained""" } else { "" }
$aclSandboxNote = if ($caps -contains 'acl') { "ACL delegation is available here via the synced Set-AdAce/Remove-AdAce/Get-AdAce FunctionDefinitions (registered with -Capability acl)." } else { "ACL delegation is not enabled on this endpoint; re-register with -Capability acl to add it." }
$langModeNote = if ($Sandbox) { "`n`nlanguage_mode = ""constrained"" is required against a -Sandbox endpoint: it runs in`nConstrainedLanguage, so the provider delivers each operation without the`nfull-language constructs a normal (full) endpoint allows. $aclSandboxNote" } else { "" }

Write-Host ''
if ($Sandbox) {
    Write-Host ("Sandbox endpoint {0} registered (ConstrainedLanguage, runs as the connecting account, stock cmdlets only). Teams must set the provider's winrm.language_mode = `"constrained`". {1}" -f $TierName, $aclSandboxNote) -ForegroundColor Green
} elseif ($RestrictCmdlets) {
    Write-Host ("Endpoint {0} registered with -RestrictCmdlets (FullLanguage -- a guardrail, not a sandbox; use -Sandbox for a real ConstrainedLanguage sandbox). Requires the go-adpwsh release that guards Import-Module in the preamble." -f $TierName) -ForegroundColor Yellow
} else {
    Write-Host "Endpoint $TierName ready. Onboard a team without touching this host:" -ForegroundColor Green
}
Write-Host @"

  New-ADUser        -Name svc_teamx -AccountPassword ... -Enabled `$true
  dsacls "OU=teamx,$(((Get-CimInstance Win32_ComputerSystem).Domain -split '\.' | ForEach-Object { "DC=$_" }) -join ',')" /I:T /G "DOMAIN\svc_teamx:GA"
  Add-ADGroupMember -Identity '$GrantTo' -Members svc_teamx

Their provider block (the credential appears twice on purpose: once to authenticate
to this host, once for the directory calls -- the second is what Active Directory
checks against their OU delegation):

  provider "activedirectory" {
    winrm {
      host               = "$fqdn"
      configuration_name = "$TierName"$langModeLine
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
PowerShell.7 endpoint, which will refuse this account.$langModeNote

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

If $fqdn does not resolve from the agent, set winrm.host to the IP and add
spn = "HTTP/$fqdn".
"@
