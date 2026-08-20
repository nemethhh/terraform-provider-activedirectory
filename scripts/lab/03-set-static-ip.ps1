<#
.SYNOPSIS
    Convert the primary adapter from DHCP to a static address, safely.
.DESCRIPTION
    A domain controller on DHCP breaks the moment its lease changes: domain join,
    DNS and the provider's pinned `server` all follow the address.

    This keeps the address the host already has, so nothing downstream moves. The
    risky part is that reconfiguring the adapter drops the SSH session mid-change;
    if it failed halfway the host would be stranded, and a lab host usually has no
    console. Two precautions:

      * the change runs as a detached scheduled task, so a dropped SSH session
        cannot leave it half-applied;
      * a second task re-enables DHCP after -RollbackMinutes unless cancelled.

    After confirming the host is still reachable, run 04-confirm-static.ps1 to
    cancel the rollback. Do NOT skip that step: otherwise the host reverts.
.EXAMPLE
    ./psrun.sh s-server 03-set-static-ip.ps1 90
#>
param([int]$RollbackMinutes = 5, [string]$Dns = '1.1.1.1,1.0.0.1')

$ErrorActionPreference = 'Stop'
$a   = Get-NetIPAddress -AddressFamily IPv4 | Where-Object { $_.IPAddress -like '192.168.*' } | Select-Object -First 1
if (-not $a) { throw 'no 192.168.x.x address found' }
$idx = $a.InterfaceIndex; $ip = $a.IPAddress; $len = $a.PrefixLength
$gw  = (Get-NetRoute -DestinationPrefix '0.0.0.0/0' -InterfaceIndex $idx).NextHop | Select-Object -First 1
if (-not $gw) { throw 'no default gateway found; refusing to continue' }

$rollback = @"
Set-NetIPInterface -InterfaceIndex $idx -Dhcp Enabled -ErrorAction SilentlyContinue
Set-DnsClientServerAddress -InterfaceIndex $idx -ResetServerAddresses -ErrorAction SilentlyContinue
Restart-NetAdapter -InterfaceIndex $idx -ErrorAction SilentlyContinue
"@
Set-Content -Path C:\Windows\Temp\rollback.ps1 -Value $rollback -Encoding ascii
schtasks /Create /TN LabRollbackDhcp /SC ONCE /ST (Get-Date).AddMinutes($RollbackMinutes).ToString('HH:mm') `
    /RU SYSTEM /RL HIGHEST /F `
    /TR 'powershell -NoProfile -ExecutionPolicy Bypass -File C:\Windows\Temp\rollback.ps1' | Out-Null

$apply = @"
Remove-NetRoute -InterfaceIndex $idx -DestinationPrefix '0.0.0.0/0' -Confirm:`$false -ErrorAction SilentlyContinue
Remove-NetIPAddress -InterfaceIndex $idx -AddressFamily IPv4 -Confirm:`$false -ErrorAction SilentlyContinue
Set-NetIPInterface -InterfaceIndex $idx -Dhcp Disabled
New-NetIPAddress -InterfaceIndex $idx -IPAddress $ip -PrefixLength $len -DefaultGateway $gw
Set-DnsClientServerAddress -InterfaceIndex $idx -ServerAddresses $Dns
"@
Set-Content -Path C:\Windows\Temp\applystatic.ps1 -Value $apply -Encoding ascii
schtasks /Create /TN LabApplyStatic /SC ONCE /ST 00:00 /RU SYSTEM /RL HIGHEST /F `
    /TR 'powershell -NoProfile -ExecutionPolicy Bypass -File C:\Windows\Temp\applystatic.ps1' | Out-Null
schtasks /Run /TN LabApplyStatic | Out-Null

Write-Output "SCHEDULED idx=$idx ip=$ip/$len gw=$gw dns=$Dns rollback=${RollbackMinutes}min"
