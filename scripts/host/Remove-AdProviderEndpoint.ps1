<#
.SYNOPSIS
    Remove a PSRP endpoint created by New-AdProviderEndpoint.ps1.
.DESCRIPTION
    Run on the management host as a local administrator. This revokes every
    grantee's access to the host at once; it does not touch any account or its
    Active Directory delegation. To remove one team instead, take its account out
    of the group the endpoint grants -- no host change needed.
#>
[CmdletBinding()]
param([Parameter(Mandatory)][string] $TierName)
$ErrorActionPreference = 'Stop'
if (-not (Get-PSSessionConfiguration -Name $TierName -ErrorAction SilentlyContinue)) {
    Write-Host "No endpoint named $TierName."; return
}
Unregister-PSSessionConfiguration -Name $TierName -Force
Restart-Service WinRM -Force
Write-Host "Removed endpoint $TierName." -ForegroundColor Green
