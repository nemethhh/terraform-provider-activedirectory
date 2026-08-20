<#
.SYNOPSIS
    Create the containers and delegated service account the acceptance suite needs.
.DESCRIPTION
    Run on the domain controller once it is serving.

    Creates:
      * AD_ACC_CONTAINER        - OU=tfacc,        the delegated test subtree
      * AD_ACC_DENIED_CONTAINER - OU=tfacc-denied, deliberately out of reach
      * svc_tfacc               - a NON-domain-admin service account

    The delegation is Full Control over the test subtree and nothing outside it.
    Full Control is required rather than merely convenient: the OU destroy path
    lifts ProtectedFromAccidentalDeletion, which edits the object's DACL - a right
    standard OU delegation does not grant.

    The denied subtree carries no grant to the service account and, deliberately,
    no explicit DENY either. An absence of delegation is what a real boundary
    looks like, and it is the only form that produces an actionable error: with
    no grant, New-ADOrganizationalUnit fails with UnauthorizedAccessException
    ("Access is denied"), which the provider classifies as KindDenied.

    An explicit DENY on read was tried first and is wrong. It hides the container
    from the account entirely, so ADWS answers a create with a generic
    "The server is unwilling to process the request" / FaultException carrying
    ErrorCode 0 and no access-denied wording anywhere - indistinguishable from a
    constraint violation, and correctly reported by the provider as unrecognised.
    That made TestAccDeniedOutsideTheDelegatedSubtree fail against a message it
    could never have matched.

    The import test still holds without the deny: the object it tries to adopt
    was never created, so the read returns not-found, which that test accepts.

    Neither container is created or destroyed by the suite; both are treated as
    pre-existing.
.EXAMPLE
    ./psrun.sh s-server 08-provision-acceptance.ps1 180 -- -SvcPassword 'CHANGEME'
#>
param(
    [Parameter(Mandatory)][string]$SvcPassword,
    [string]$Root        = 'DC=corp,DC=local',
    [string]$Netbios     = 'CORP',
    [string]$SvcName     = 'svc_tfacc',
    [string]$TestOU      = 'tfacc',
    [string]$DeniedOU    = 'tfacc-denied'
)

$ErrorActionPreference = 'Stop'
Import-Module ActiveDirectory

function EnsureOU($name, $path) {
    $existing = Get-ADOrganizationalUnit -Filter "Name -eq '$name'" -SearchBase $path -SearchScope OneLevel -ErrorAction SilentlyContinue
    if (-not $existing) {
        # Unprotected: the suite's own OUs are protected by default, but these two
        # are fixtures a human may need to remove.
        New-ADOrganizationalUnit -Name $name -Path $path -ProtectedFromAccidentalDeletion $false
        # Write-Host, not Write-Output: anything written to the output stream
        # inside a function becomes part of its return value, and the DN would
        # come back as an array with the status line glued to the front.
        Write-Host "OU_CREATED OU=$name,$path"
    } else {
        Write-Host "OU_EXISTS OU=$name,$path"
    }
    return "OU=$name,$path"
}

$tfacc  = EnsureOU $TestOU   $Root
$denied = EnsureOU $DeniedOU $Root

$secure = ConvertTo-SecureString $SvcPassword -AsPlainText -Force
if (-not (Get-ADUser -Filter "SamAccountName -eq '$SvcName'" -ErrorAction SilentlyContinue)) {
    New-ADUser -Name $SvcName -SamAccountName $SvcName `
        -UserPrincipalName "$SvcName@$(($Root -replace 'DC=','' -replace ',','.'))" `
        -Path $Root -AccountPassword $secure -Enabled $true -PasswordNeverExpires $true
    Write-Output "SVC_CREATED $SvcName"
} else {
    Set-ADAccountPassword -Identity $SvcName -Reset -NewPassword $secure
    Write-Output "SVC_EXISTS $SvcName (password reset)"
}

# The suite is meaningless if this account is privileged.
$isAdmin = [bool]((Get-ADUser $SvcName -Properties MemberOf).MemberOf -match 'Domain Admins')
Write-Output "svc_is_domain_admin=$isAdmin"
if ($isAdmin) { throw "$SvcName is a Domain Admin; the denial suite would prove nothing" }

dsacls $tfacc  /I:T /G "$Netbios\${SvcName}:GA" | Out-Null
Write-Output "GRANTED full control on $tfacc"

# No grant, and deliberately no explicit deny - see the note above. Any ACE left
# by an earlier run is stripped, because a DENY changes how a refused create is
# reported and makes the denial suite unmatchable.
if (dsacls $denied | Select-String -SimpleMatch "$Netbios\$SvcName") {
    dsacls $denied /R "$Netbios\$SvcName" | Out-Null
    Write-Output "REMOVED stale ACEs for $SvcName on $denied"
}
Write-Output "NO GRANT on $denied (absence of delegation is the boundary)"

Write-Output ('adws=' + (Get-Service ADWS).Status)
Write-Output ('AD_ACC_CONTAINER=' + $tfacc)
Write-Output ('AD_ACC_DENIED_CONTAINER=' + $denied)
Write-Output 'PROVISION_DONE'
