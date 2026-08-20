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

    Note the explicit DENY on the denied subtree. Active Directory grants
    Authenticated Users read on most objects by default, so without it
    TestAccDeniedImportOutsideTheDelegatedSubtree would import the object
    successfully and the suite would be asserting nothing. The deny is what makes
    that test exercise the real boundary.

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

dsacls $denied /I:T /D "$Netbios\${SvcName}:GR" | Out-Null
dsacls $denied /I:T /D "$Netbios\${SvcName}:GW" | Out-Null
Write-Output "DENIED read+write on $denied"

Write-Output ('adws=' + (Get-Service ADWS).Status)
Write-Output ('AD_ACC_CONTAINER=' + $tfacc)
Write-Output ('AD_ACC_DENIED_CONTAINER=' + $denied)
Write-Output 'PROVISION_DONE'
