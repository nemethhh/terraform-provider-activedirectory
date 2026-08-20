<#
.SYNOPSIS
    Create the OUs and delegated non-admin principals the e2e suite needs.
.DESCRIPTION
    Run on the domain controller, as admin, once. Idempotent: re-running
    creates what is missing and resets the principals' password.

    Layout (see docs/superpowers/specs/2026-08-21-e2e-tests-design.md):
      OU=e2e
        OU=alpha    -> svc_e2e_alpha   : Full Control            (Scenarios A, B, C)
        OU=beta     -> svc_e2e_beta    : Full Control            (Scenario C)
        OU=limited  -> svc_e2e_limited : Full Control, but DENIED
                                         create-child of group/user (Scenario D)

    None of the three is a Domain Admin; the script throws if one is.

    The principals live directly beneath OU=e2e (not beneath a delegated
    sub-OU), so no principal can delete or re-target its own auth account, and
    the tfacc- sweeper never touches them.
.EXAMPLE
    ./psrun.sh s-server 13-provision-e2e.ps1 300 -- -SvcPassword 'CHANGEME'
#>
param(
    [Parameter(Mandatory)][string]$SvcPassword,
    [string]$Root    = 'DC=corp,DC=local',
    [string]$Netbios = 'CORP',
    [string]$E2ERoot = 'e2e'
)

$ErrorActionPreference = 'Stop'
Import-Module ActiveDirectory

function EnsureOU($name, $path) {
    $existing = Get-ADOrganizationalUnit -Filter "Name -eq '$name'" -SearchBase $path `
        -SearchScope OneLevel -ErrorAction SilentlyContinue
    if (-not $existing) {
        # Unprotected: these are fixtures a human may need to remove by hand.
        New-ADOrganizationalUnit -Name $name -Path $path -ProtectedFromAccidentalDeletion $false
        Write-Host "OU_CREATED OU=$name,$path"
    } else {
        Write-Host "OU_EXISTS OU=$name,$path"
    }
    return "OU=$name,$path"
}

function EnsureUser($sam, $path, $secure) {
    if (-not (Get-ADUser -Filter "SamAccountName -eq '$sam'" -ErrorAction SilentlyContinue)) {
        New-ADUser -Name $sam -SamAccountName $sam `
            -UserPrincipalName "$sam@$(($Root -replace 'DC=','' -replace ',','.'))" `
            -Path $path -AccountPassword $secure -Enabled $true -PasswordNeverExpires $true
        Write-Output "SVC_CREATED $sam"
    } else {
        Set-ADAccountPassword -Identity $sam -Reset -NewPassword $secure
        Write-Output "SVC_EXISTS $sam (password reset)"
    }
    $isAdmin = [bool]((Get-ADUser $sam -Properties MemberOf).MemberOf -match 'Domain Admins')
    Write-Output "${sam}_is_domain_admin=$isAdmin"
    if ($isAdmin) { throw "$sam is a Domain Admin; the e2e suite would prove nothing" }
}

$e2e     = EnsureOU $E2ERoot 'DC=corp,DC=local'
$alpha   = EnsureOU 'alpha'   $e2e
$beta    = EnsureOU 'beta'    $e2e
$limited = EnsureOU 'limited' $e2e

$secure = ConvertTo-SecureString $SvcPassword -AsPlainText -Force
EnsureUser 'svc_e2e_alpha'   $e2e $secure
EnsureUser 'svc_e2e_beta'    $e2e $secure
EnsureUser 'svc_e2e_limited' $e2e $secure

# Full Control over each principal's own sub-OU and nothing outside it.
dsacls $alpha /I:T /G "$Netbios\svc_e2e_alpha:GA"   | Out-Null
Write-Output "GRANTED full control on $alpha to svc_e2e_alpha"
dsacls $beta  /I:T /G "$Netbios\svc_e2e_beta:GA"    | Out-Null
Write-Output "GRANTED full control on $beta to svc_e2e_beta"

# limited: Full Control, then a targeted create-child DENY for group and user.
# A create deny on a class yields a clean UnauthorizedAccessException on the
# cmdlet; it does NOT hide objects the way a read deny would.
dsacls $limited /I:T /G "$Netbios\svc_e2e_limited:GA"       | Out-Null
dsacls $limited /I:T /D "$Netbios\svc_e2e_limited:CC;group" | Out-Null
dsacls $limited /I:T /D "$Netbios\svc_e2e_limited:CC;user"  | Out-Null
Write-Output "GRANTED full control on $limited to svc_e2e_limited, DENIED create of group/user"

Write-Output ('adws=' + (Get-Service ADWS).Status)
Write-Output ('AD_E2E_CONTAINER=' + $e2e)
Write-Output 'E2E_PROVISION_DONE'
