<#
.SYNOPSIS
    Print the lab CA certificate as PEM, for a client to pin.
#>
param([string]$CaCommonName = 'corp-lab-ca')
$ErrorActionPreference = 'Stop'
$ca = Get-ChildItem Cert:\LocalMachine\CA, Cert:\LocalMachine\Root -ErrorAction SilentlyContinue |
      Where-Object { $_.Subject -match [regex]::Escape($CaCommonName) } | Select-Object -First 1
if (-not $ca) { Write-Error "no CA certificate matching '$CaCommonName'"; exit 1 }
Write-Output ("-----BEGIN CERTIFICATE-----`n" +
              [Convert]::ToBase64String($ca.RawData, 'InsertLineBreaks') +
              "`n-----END CERTIFICATE-----")
