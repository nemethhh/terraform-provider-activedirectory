<#
.SYNOPSIS
    Point DNS at the domain controller and join this host to the domain.
.DESCRIPTION
    A member server must resolve the domain through the DC, not a public resolver,
    or the join fails with a misleading "domain could not be contacted". DNS is
    therefore repointed first and the resolution is proven before the join is
    attempted.

    Installs RSAT-AD-PowerShell too: the provider's cmdlets come from it, and it is
    required on whichever host runs pwsh.

    Logs to C:\Windows\Temp\join.log and reboots on success.
.EXAMPLE
    ./psrun.sh s-client 07-join-domain.ps1 600 -- -DcAddress 192.168.50.216 -DomainName corp.local -JoinUser 'CORP\Administrator' -JoinPassword 'CHANGEME'
#>
param(
    [Parameter(Mandatory)][string]$DcAddress,
    [Parameter(Mandatory)][string]$JoinUser,
    [Parameter(Mandatory)][string]$JoinPassword,
    [string]$DomainName = 'corp.local'
)

$ErrorActionPreference = 'Stop'
function Log($m){ "$(Get-Date -Format o)  $m" | Out-File -FilePath C:\Windows\Temp\join.log -Append -Encoding ascii }

try {
    if ((Get-CimInstance Win32_ComputerSystem).PartOfDomain) { Write-Output 'ALREADY_JOINED'; exit 0 }

    $a = Get-NetIPAddress -AddressFamily IPv4 | Where-Object { $_.IPAddress -like '192.168.*' } | Select-Object -First 1
    Set-DnsClientServerAddress -InterfaceIndex $a.InterfaceIndex -ServerAddresses $DcAddress
    Clear-DnsClientCache
    Log ("dns -> $DcAddress")

    $probe = Resolve-DnsName -Name $DomainName -Type A -ErrorAction SilentlyContinue
    if (-not $probe) { throw "$DomainName does not resolve via $DcAddress; is the DC up and serving DNS?" }
    Log ("resolved $DomainName -> " + ($probe.IPAddress -join ','))

    $r = Install-WindowsFeature RSAT-AD-PowerShell
    Log ('rsat state=' + (Get-WindowsFeature RSAT-AD-PowerShell).InstallState)

    $cred = New-Object System.Management.Automation.PSCredential(
        $JoinUser, (ConvertTo-SecureString $JoinPassword -AsPlainText -Force))
    Add-Computer -DomainName $DomainName -Credential $cred -Force
    Log 'JOIN_OK rebooting'
    Write-Output 'JOIN_OK rebooting'
    shutdown /r /t 5 /f
} catch {
    Log ('JOIN_FAILED: ' + $_.Exception.Message)
    Write-Output ('JOIN_FAILED: ' + $_.Exception.Message)
}
