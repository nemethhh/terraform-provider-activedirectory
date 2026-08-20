<#
.SYNOPSIS
    Install AD DS and promote this host to the first DC of a new forest.
.DESCRIPTION
    Both steps take far longer than an interactive SSH command reasonably holds,
    and promotion reboots the host at the end, which kills the session mid-flight.
    So the work is written to disk and launched as a detached SYSTEM task that
    logs to C:\Windows\Temp\labsetup.log. Poll that log rather than waiting on the
    SSH call:

        ssh s-server 'powershell -NoProfile -Command "Get-Content C:\Windows\Temp\labsetup.log -Tail 5"'

    Preconditions: the host is renamed (05) and on a static address (03/04).
    Expect the host to be unreachable for a long stretch afterwards - the first
    boot as a DC initialises the database, SYSVOL and DFSR, and applies any
    pending updates. ADWS on TCP 9389 is what the provider needs, so check that,
    not just SSH.
.EXAMPLE
    ./psrun.sh s-server 06-promote-dc.ps1 120 -- -DsrmPassword 'CHANGEME' -DomainName corp.local -NetbiosName CORP
#>
param(
    [Parameter(Mandatory)][string]$DsrmPassword,
    [string]$DomainName  = 'corp.local',
    [string]$NetbiosName = 'CORP'
)

$ErrorActionPreference = 'Stop'

$inner = @"
`$ErrorActionPreference = 'Stop'
function Log(`$m){ "`$(Get-Date -Format o)  `$m" | Out-File -FilePath C:\Windows\Temp\labsetup.log -Append -Encoding ascii }
try {
    Log 'START role install'
    `$r = Install-WindowsFeature AD-Domain-Services -IncludeManagementTools
    Log ('role success=' + `$r.Success + ' state=' + (Get-WindowsFeature AD-Domain-Services).InstallState)
    Log 'START promotion'
    Import-Module ADDSDeployment
    `$sec = ConvertTo-SecureString '$DsrmPassword' -AsPlainText -Force
    Install-ADDSForest -DomainName '$DomainName' -DomainNetbiosName '$NetbiosName' ``
        -SafeModeAdministratorPassword `$sec -InstallDns:`$true ``
        -DatabasePath 'C:\Windows\NTDS' -LogPath 'C:\Windows\NTDS' -SysvolPath 'C:\Windows\SYSVOL' ``
        -NoRebootOnCompletion:`$false -Force:`$true
    Log 'PROMOTION_SUBMITTED'
} catch { Log ('FAILED: ' + `$_.Exception.Message) }
"@

Remove-Item C:\Windows\Temp\labsetup.log -Force -ErrorAction SilentlyContinue
Set-Content -Path C:\Windows\Temp\labsetup.ps1 -Value $inner -Encoding ascii
schtasks /Create /TN LabSetup /SC ONCE /ST 00:00 /RU SYSTEM /RL HIGHEST /F `
    /TR 'powershell -NoProfile -ExecutionPolicy Bypass -File C:\Windows\Temp\labsetup.ps1' | Out-Null
schtasks /Run /TN LabSetup | Out-Null
Write-Output 'PROMOTION_LAUNCHED (poll C:\Windows\Temp\labsetup.log; host will reboot)'
