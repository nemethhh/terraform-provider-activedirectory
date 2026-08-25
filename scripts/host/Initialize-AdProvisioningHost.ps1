<#
.SYNOPSIS
    One-time configuration of a management host that terraform-provider-activedirectory
    reaches over PSRP/WinRM.

.DESCRIPTION
    Run once per management host, as a local administrator, FROM WINDOWS
    POWERSHELL 5.1 (an endpoint takes the engine of the shell that registers
    it; a 5.1 endpoint with no RunAs identity admits a non-administrator
    caller and runs as that caller, which is the point -- see
    New-AdProviderEndpoint.ps1).

    What this does NOT do: grant any team account anything. Per-capability
    access is New-AdProviderEndpoint.ps1, one endpoint per capability tier.

.NOTES
    Verified on Windows Server 2025 against corp.local.

    Two facts this script is shaped around, both established by testing:

      * A PowerShell 7 endpoint fails for a non-administrator caller unless the
        endpoint supplies a RunAs identity, and that identity is a local
        administrator (a virtual account) -- so the caller would gain
        local-administrator code execution on this host. The WinRM plugin
        faults (pwrshplugin.dll, HTTP 500) with no useful message otherwise.
        A Windows PowerShell 5.1 endpoint with no RunAs identity does NOT have
        this problem: it admits the caller and runs as that caller. That is
        why New-AdProviderEndpoint.ps1 registers 5.1 endpoints and sets no
        RunAs identity.

      * A per-endpoint SDDL is sufficient on its own. Team accounts do NOT need
        to be members of Remote Management Users, so they cannot reach any other
        endpoint on the host.
#>
[CmdletBinding()]
param(
    # CIDR(s) allowed to reach WinRM. Your Jenkins agent subnet(s). The default
    # blocks everything -- pass the real ranges.
    [string[]] $AllowedClientCidr = @(),

    # Domain group holding every Terraform service account. Interactive and RDP
    # logon are denied to it, so the credential is useless for anything but WinRM.
    [string] $ServiceAccountGroup,

    # Shells per user. The psrp transport does not release its own shells: it
    # asks WinRM for a 2-minute lease per shell instead of the 30-minute
    # default, so a shell it opened is only reclaimed when that lease expires,
    # not when the operation using it finishes. A Terraform process therefore
    # parks up to psrp.max_concurrency shells for the full 2 minutes, so this
    # must be >= max_concurrency times however many Terraform processes (plan,
    # apply, ...) can start for the same account within any 2-minute window --
    # not just the largest max_concurrency any one team sets. Default matches
    # Windows' own WinRM default (30) rather than lowering it.
    [int] $MaxShellsPerUser = 30,

    # Distinct accounts that may hold shells at once.
    [int] $MaxConcurrentUsers = 20,

    [switch] $SkipFeatureInstall
)

$ErrorActionPreference = 'Stop'
function Step($m) { Write-Host "==> $m" -ForegroundColor Cyan }
function Note($m) { Write-Host "    $m" }
function Warn($m) { Write-Host "!!  $m" -ForegroundColor Yellow }

# --- preconditions ----------------------------------------------------------

if ($PSVersionTable.PSVersion.Major -ne 5) {
    throw "Run this from Windows PowerShell 5.1 (powershell.exe). An endpoint takes the engine of the shell that registers it, and the 5.1 engine is the point: a 5.1 endpoint with no RunAs admits a non-administrator caller and runs as that caller, so team accounts need no privilege on this host. A PowerShell 7 endpoint faults for a non-admin caller unless it runs as a local-administrator virtual account."
}
$me = [Security.Principal.WindowsPrincipal]::new([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $me.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw 'Run as an administrator.'
}

# --- 1. RSAT-AD, the module the provider drives ------------------------------

Step 'RSAT-AD-PowerShell'
if (-not $SkipFeatureInstall) {
    $f = Get-WindowsFeature -Name RSAT-AD-PowerShell -ErrorAction SilentlyContinue
    if ($f -and -not $f.Installed) {
        Install-WindowsFeature -Name RSAT-AD-PowerShell | Out-Null
        Note 'installed'
    } else { Note 'already present' }
}
if (-not (Get-Module -ListAvailable ActiveDirectory)) {
    throw 'The ActiveDirectory module is still not available. The provider cannot work without it.'
}

# --- 2. WinRM remoting -------------------------------------------------------

Step 'WinRM remoting'
Enable-PSRemoting -Force -SkipNetworkProfileCheck | Out-Null
Note 'the built-in 5.1 endpoints are registered; team endpoints come from New-AdProviderEndpoint.ps1'

# --- 3. WinRM service hardening ---------------------------------------------

Step 'WinRM authentication and encryption'
# Kerberos over HTTP is encrypted by the Negotiate path. Basic and CredSSP are
# off; AllowUnencrypted stays false so a non-Kerberos client cannot fall back to
# cleartext.
& winrm set winrm/config/service '@{AllowUnencrypted="false"}'                | Out-Null
& winrm set winrm/config/service/auth '@{Basic="false";CredSSP="false";Negotiate="true";Kerberos="true"}' | Out-Null
& winrm set winrm/config/winrs "@{MaxShellsPerUser=`"$MaxShellsPerUser`";MaxConcurrentUsers=`"$MaxConcurrentUsers`"}" | Out-Null
Note "AllowUnencrypted=false, Basic/CredSSP off, MaxShellsPerUser=$MaxShellsPerUser"

# --- 4. Keep the shared endpoints administrator-only ------------------------

Step 'Restricting the built-in endpoints to administrators'
# A team account must never land in an unrestricted session. Team accounts get
# their own endpoint; the shared ones are for administrators.
$adminOnly = 'O:NSG:BAD:P(A;;GA;;;BA)S:P(AU;FA;GA;;;WD)'
foreach ($c in Get-PSSessionConfiguration) {
    if ($c.Name -like 'PowerShell.7*' -or $c.Name -like 'microsoft.powershell*') {
        try {
            Set-PSSessionConfiguration -Name $c.Name -SecurityDescriptorSddl $adminOnly -Force -NoServiceRestart -WarningAction SilentlyContinue | Out-Null
            Note "$($c.Name): administrators only"
        } catch { Warn "$($c.Name): could not set SDDL -- $($_.Exception.Message)" }
    }
}

# --- 5. Firewall ------------------------------------------------------------

Step 'Firewall for WinRM 5985'
if ($AllowedClientCidr.Count -eq 0) {
    Warn 'No -AllowedClientCidr given: leaving the existing firewall rules alone.'
    Warn 'Pass your Jenkins agent subnet(s) so WinRM is not reachable from the whole network.'
} else {
    Get-NetFirewallRule -DisplayName 'AD provider WinRM (managed)' -ErrorAction SilentlyContinue |
        Remove-NetFirewallRule -ErrorAction SilentlyContinue
    New-NetFirewallRule -DisplayName 'AD provider WinRM (managed)' -Direction Inbound -Protocol TCP `
        -LocalPort 5985 -RemoteAddress $AllowedClientCidr -Action Allow -Profile Any | Out-Null
    # Narrow the built-in rules so they cannot re-open 5985 to everything.
    Get-NetFirewallRule | Where-Object { $_.DisplayName -like 'Windows Remote Management (HTTP-In)*' } |
        Set-NetFirewallRule -RemoteAddress $AllowedClientCidr -ErrorAction SilentlyContinue
    Note ("5985 allowed from: " + ($AllowedClientCidr -join ', '))
}

# --- 6. Deny the service accounts every logon type except the network one ----

Step 'Logon-right restrictions for service accounts'
if (-not $ServiceAccountGroup) {
    Warn 'No -ServiceAccountGroup given: skipping. Create a domain group for the'
    Warn 'Terraform service accounts and re-run with it, so a leaked credential'
    Warn 'cannot be used to log on interactively or over RDP.'
} else {
    $sid = ([Security.Principal.NTAccount]$ServiceAccountGroup).Translate([Security.Principal.SecurityIdentifier]).Value
    $cfg = Join-Path $env:TEMP 'adhost-secpol.cfg'
    & secedit /export /cfg $cfg /quiet | Out-Null
    $lines = Get-Content $cfg
    # Network logon is what WinRM uses and must stay. Everything else goes.
    foreach ($right in 'SeDenyInteractiveLogonRight','SeDenyRemoteInteractiveLogonRight','SeDenyServiceLogonRight','SeDenyBatchLogonRight') {
        $hit = $lines | Select-String -Pattern "^$right" | Select-Object -First 1
        if ($hit) {
            if ($hit.Line -notlike "*$sid*") { $lines = $lines -replace [regex]::Escape($hit.Line), ($hit.Line + ",*$sid") }
        } else {
            $lines = $lines -replace '^\[Privilege Rights\]', "[Privilege Rights]`r`n$right = *$sid"
        }
    }
    Set-Content -Path $cfg -Value $lines -Encoding Unicode
    & secedit /configure /db "$env:windir\security\local.sdb" /cfg $cfg /areas USER_RIGHTS | Out-Null
    Remove-Item $cfg -Force -ErrorAction SilentlyContinue
    Note "$ServiceAccountGroup denied interactive, RDP, service and batch logon"
}

# --- 7. ADWS reachability ---------------------------------------------------

Step 'ADWS (TCP 9389) reachability'
try {
    Import-Module ActiveDirectory
    foreach ($dc in (Get-ADDomainController -Filter * | Select-Object -ExpandProperty HostName)) {
        $ok = $false
        try { $t = [System.Net.Sockets.TcpClient]::new(); $ok = $t.ConnectAsync($dc, 9389).Wait(3000) -and $t.Connected; $t.Dispose() } catch { }
        Note ("{0}: 9389 {1}" -f $dc, $(if ($ok) { 'open' } else { 'UNREACHABLE' }))
    }
} catch { Warn "Could not enumerate domain controllers: $($_.Exception.Message)" }

Restart-Service WinRM -Force
Write-Host ''
Write-Host 'Host ready. Create one endpoint per capability tier with New-AdProviderEndpoint.ps1.' -ForegroundColor Green
