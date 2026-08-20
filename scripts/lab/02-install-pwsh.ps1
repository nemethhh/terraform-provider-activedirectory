<#
.SYNOPSIS
    Install PowerShell 7 (pwsh).
.DESCRIPTION
    The provider invokes `pwsh` specifically. Windows PowerShell 5.1, which ships
    with the OS, does not satisfy it. Idempotent: exits early if pwsh is present.

    Run this while DNS still points at a public resolver — after the host joins
    the domain its DNS is the DC, which may not forward outbound.
.EXAMPLE
    ./psrun.sh s-client 02-install-pwsh.ps1 600
#>
$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

if (Get-Command pwsh -ErrorAction SilentlyContinue) { Write-Output 'PWSH_ALREADY'; exit 0 }

Invoke-Expression "& { $(Invoke-RestMethod https://aka.ms/install-powershell.ps1) } -UseMSI -Quiet"

$env:Path = [Environment]::GetEnvironmentVariable('Path','Machine')
$p = Get-Command pwsh -ErrorAction SilentlyContinue
if ($p) { Write-Output ('PWSH_INSTALLED ' + $p.Source) } else { throw 'pwsh not on PATH after install' }
