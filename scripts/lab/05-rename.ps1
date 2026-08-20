<#
.SYNOPSIS
    Rename the computer and reboot.
.DESCRIPTION
    Fresh Windows installs all carry the same generated name (WIN-xxxxxxxx), and
    two machines cannot hold one name in a domain.

    Rename BEFORE promoting a domain controller. Renaming a DC afterwards is a
    substantially harder operation.

    The reboot is fired from a detached task so the SSH session closing cannot
    abort it.
.EXAMPLE
    ./psrun.sh s-server 05-rename.ps1 90 -- -NewName s-server
#>
param([Parameter(Mandatory)][string]$NewName)

$ErrorActionPreference = 'Stop'
if ($env:COMPUTERNAME -eq $NewName) { Write-Output 'NAME_ALREADY_SET'; exit 0 }

Rename-Computer -NewName $NewName -Force
Write-Output ("RENAMED_TO $NewName pending reboot; was " + $env:COMPUTERNAME)

schtasks /Create /TN LabReboot /SC ONCE /ST 00:00 /RU SYSTEM /RL HIGHEST /F /TR 'shutdown /r /t 5 /f' | Out-Null
schtasks /Run /TN LabReboot | Out-Null
