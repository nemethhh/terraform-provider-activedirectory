<#
.SYNOPSIS
    Confirm the static address took, and cancel the DHCP rollback armed by 03.
.DESCRIPTION
    Run only after verifying from outside that the host is still reachable. If the
    interface is somehow still on DHCP this leaves the rollback armed rather than
    cancelling it, so a partial failure still self-heals.
.EXAMPLE
    ./psrun.sh s-server 04-confirm-static.ps1 60
#>
$ErrorActionPreference = 'SilentlyContinue'
$a  = Get-NetIPAddress -AddressFamily IPv4 | Where-Object { $_.IPAddress -like '192.168.*' } | Select-Object -First 1
$if = Get-NetIPInterface -InterfaceIndex $a.InterfaceIndex -AddressFamily IPv4

if ($if.Dhcp -eq 'Disabled' -and $a.IPAddress) {
    schtasks /Delete /TN LabRollbackDhcp /F | Out-Null
    schtasks /Delete /TN LabApplyStatic  /F | Out-Null
    Remove-Item C:\Windows\Temp\rollback.ps1, C:\Windows\Temp\applystatic.ps1 -Force -ErrorAction SilentlyContinue
    Write-Output ("STATIC_OK " + $a.IPAddress + "/" + $a.PrefixLength +
                  " gw=" + ((Get-NetRoute -DestinationPrefix '0.0.0.0/0').NextHop -join ',') + " rollback_cancelled")
} else {
    Write-Output ("STILL_DHCP dhcp=" + $if.Dhcp + " ip=" + $a.IPAddress + " - leaving rollback armed")
}
