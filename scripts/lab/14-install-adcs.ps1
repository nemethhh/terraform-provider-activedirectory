<#
.SYNOPSIS
    Install an Enterprise Root CA and enrol the domain controllers for LDAPS.
.DESCRIPTION
    A fresh AD DS install has no certificate, so LSASS binds TCP 636 and then
    resets every TLS handshake. It looks exactly like a firewall problem; it is
    not. The provider's `ldap` connection needs LDAPS, so the lab needs a CA.

    An Enterprise Root CA is used rather than a hand-made self-signed
    certificate because it is what a real domain has: every DC auto-enrols a
    Domain Controller certificate from it, the second DC gets one without any
    extra step, and the CA certificate can be pinned with the provider's
    `ca_certificate_file` — which exercises certificate verification instead of
    skipping it.

    Idempotent: re-running reports the existing CA and re-pulses enrolment.
.EXAMPLE
    ./psrun.sh s-server1 14-install-adcs.ps1 900 -- -CaCommonName 'corp-lab-ca'
#>
param(
    [string]$CaCommonName = 'corp-lab-ca',
    [int]$ValidityYears   = 5
)

$ErrorActionPreference = 'Stop'

$state = (Get-WindowsFeature ADCS-Cert-Authority).InstallState
if ($state -ne 'Installed') {
    $r = Install-WindowsFeature ADCS-Cert-Authority -IncludeManagementTools
    Write-Output ("ROLE_INSTALLED success=" + $r.Success)
} else {
    Write-Output "ROLE_PRESENT"
}

# Configuring an already-configured CA throws 0x80070002; treat that as done.
try {
    Install-AdcsCertificationAuthority -CAType EnterpriseRootCa `
        -CACommonName $CaCommonName -ValidityPeriod Years `
        -ValidityPeriodUnits $ValidityYears -Force -ErrorAction Stop | Out-Null
    Write-Output "CA_CONFIGURED $CaCommonName"
} catch {
    if ($_.Exception.Message -match 'already installed|0x80070002') {
        Write-Output "CA_ALREADY_CONFIGURED"
    } else { throw }
}

Start-Sleep -Seconds 5
$svc = Get-Service CertSvc -ErrorAction SilentlyContinue
if ($svc -and $svc.Status -ne 'Running') { Start-Service CertSvc }
Write-Output ("CERTSVC=" + (Get-Service CertSvc).Status)

# Pull a Domain Controller certificate now rather than waiting for the
# auto-enrolment cycle, which is up to eight hours.
certutil -pulse | Out-Null
Start-Sleep -Seconds 10

$dc = Get-ChildItem Cert:\LocalMachine\My -ErrorAction SilentlyContinue |
      Where-Object { $_.EnhancedKeyUsageList.FriendlyName -contains 'Server Authentication' }
if (-not $dc) {
    # Auto-enrolment can lag the CA coming up; ask once more.
    Start-Sleep -Seconds 20
    certutil -pulse | Out-Null
    Start-Sleep -Seconds 15
    $dc = Get-ChildItem Cert:\LocalMachine\My -ErrorAction SilentlyContinue |
          Where-Object { $_.EnhancedKeyUsageList.FriendlyName -contains 'Server Authentication' }
}
if ($dc) {
    foreach ($c in $dc) { Write-Output ("DC_CERT subject={0} expires={1}" -f $c.Subject, $c.NotAfter) }
} else {
    Write-Output "DC_CERT_MISSING (auto-enrolment has not issued one yet)"
}

# The CA certificate, for the client to pin.
$ca = Get-ChildItem Cert:\LocalMachine\CA, Cert:\LocalMachine\Root -ErrorAction SilentlyContinue |
      Where-Object { $_.Subject -match [regex]::Escape($CaCommonName) } | Select-Object -First 1
if ($ca) {
    $pem = "-----BEGIN CERTIFICATE-----`n" +
           [Convert]::ToBase64String($ca.RawData, 'InsertLineBreaks') +
           "`n-----END CERTIFICATE-----"
    Set-Content -Path C:\Windows\Temp\corp-lab-ca.pem -Value $pem -Encoding ascii
    Write-Output "CA_PEM C:\Windows\Temp\corp-lab-ca.pem"
    Write-Output "CA_PEM_BEGIN"
    Write-Output $pem
    Write-Output "CA_PEM_END"
} else {
    Write-Output "CA_CERT_NOT_FOUND"
}
