# Set LdapEnforceChannelBinding on a domain controller and restart NTDS so it
# takes effect.
#
#   0  never        - the Windows Server default, and this lab's original state
#   1  when supported - a client that sends no token still binds
#   2  always       - required by the CIS Benchmark and the DISA STIG
#
# Reads the value back after the restart and throws on a mismatch: a run that
# silently tested the wrong policy is worse than one that failed.
param([Parameter(Mandatory = $true)][ValidateSet('0', '1', '2')][string]$Value)

$ErrorActionPreference = 'Stop'
$key = 'HKLM:\SYSTEM\CurrentControlSet\Services\NTDS\Parameters'

New-ItemProperty -Path $key -Name 'LdapEnforceChannelBinding' `
    -Value ([int]$Value) -PropertyType DWord -Force | Out-Null

# NTDS cannot be restarted directly without stopping its dependents; restarting
# with -Force takes them down and brings them back.
Restart-Service -Name NTDS -Force

$actual = (Get-ItemProperty -Path $key -Name 'LdapEnforceChannelBinding').LdapEnforceChannelBinding
if ($actual -ne [int]$Value) {
    throw "LdapEnforceChannelBinding=$actual, expected $Value -- the policy did not take"
}
Write-Output "LdapEnforceChannelBinding=$actual"
