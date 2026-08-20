<#
.SYNOPSIS
    Install AD DS and promote this host as an ADDITIONAL domain controller in an
    existing domain.
.DESCRIPTION
    The sibling of 06, and deliberately a separate script: 06 calls
    Install-ADDSForest to create a forest, this calls Install-ADDSDomainController
    to join one that already exists. Running 06 against an existing domain would
    try to create a second forest of the same name.

    Same detached pattern as 06, for the same reasons: the role install outlives
    a comfortable SSH command, promotion reboots, and a held session would die
    mid-operation telling you nothing. Poll the log instead:

        ssh s-server2 'powershell -NoProfile -Command "Get-Content C:\Windows\Temp\labsetup.log -Tail 5"'

    Preconditions, all of which this script verifies before submitting anything,
    because each produces a misleading failure hours later:

      * DNS must already resolve the domain and reach the existing DC. A member
        that cannot find the domain's SRV records reports "the specified domain
        either does not exist or could not be contacted", which reads like a
        credential problem.
      * The machine SID must differ from the existing domain's SID. Cloning a VM
        image without sysprep /generalize gives both hosts the same SID; the
        domain then claims the very SID this host uses locally, and promotion
        fails with an authentication error while the DC logs event 4776 with
        Error Code 0x0 - credentials accepted, logon session refused. LAB.md
        records the full diagnosis; it cost a rebuild the first time.
      * The clock must be within five minutes of the DC. Kerberos refuses a
        larger skew and reports it as a bad password.

    -Credential must be a Domain Admin of the target domain. DSRM's password is
    this host's own, and may differ from the first DC's.
.EXAMPLE
    ./psrun.sh s-server2 12-promote-second-dc.ps1 300 -- -DsrmPassword 'x' -AdminUser 'CORP\Administrator' -AdminPassword 'y'
#>
param(
    [Parameter(Mandatory)][string]$DsrmPassword,
    [Parameter(Mandatory)][string]$AdminUser,
    [Parameter(Mandatory)][string]$AdminPassword,
    [string]$DomainName = 'corp.local',
    [string]$SiteName   = 'Default-First-Site-Name'
)

$ErrorActionPreference = 'Stop'

# --- preflight, in the order that fails cheapest first ----------------------
#
# Everything here goes over LDAP through ADSI rather than the ActiveDirectory
# module. That module arrives with the role install, which happens inside the
# detached task below -- so a preflight written against Get-ADDomain would fail
# on precisely the un-promoted host it is meant to check.

$localSid   = (Get-CimInstance Win32_UserAccount -Filter "LocalAccount=True AND Name='Administrator'").SID
$machineSid = $localSid -replace '-500$', ''
Write-Output "MACHINE_SID $machineSid"

$srv = Resolve-DnsName -Name "_ldap._tcp.dc._msdcs.$DomainName" -Type SRV -ErrorAction SilentlyContinue
if (-not $srv) {
    throw "DNS cannot resolve _ldap._tcp.dc._msdcs.$DomainName. Run 11-point-dns-at-dc.ps1 first."
}
$dcHost = ($srv | Where-Object { $_.NameTarget } | Select-Object -First 1).NameTarget
Write-Output "DOMAIN_DC $dcHost"

if (-not (Test-NetConnection -ComputerName $dcHost -Port 389 -InformationLevel Quiet)) {
    throw "Cannot reach LDAP on ${dcHost}:389 from this host."
}

# Binding with the credential proves it works before an hour of promotion rides
# on it. A wrong password here is the failure that masquerades as everything else.
try {
    $rootDse = New-Object DirectoryServices.DirectoryEntry("LDAP://$dcHost/RootDSE", $AdminUser, $AdminPassword)
    $namingContext = $rootDse.Properties['defaultNamingContext'].Value
    $dcTimeRaw     = $rootDse.Properties['currentTime'].Value
} catch {
    throw "Cannot bind to LDAP://$dcHost as ${AdminUser}: $($_.Exception.Message)"
}
if (-not $namingContext) { throw "Bound to $dcHost but read no defaultNamingContext; check the credential." }
Write-Output "NAMING_CONTEXT $namingContext"

# The SID collision that cost a rebuild the first time: an ungeneralised clone
# shares the machine SID the domain was built from, and can never join. LAB.md
# records the full diagnosis, including why the error message points elsewhere.
$domainRoot = New-Object DirectoryServices.DirectoryEntry("LDAP://$dcHost/$namingContext", $AdminUser, $AdminPassword)
$domainSid  = (New-Object System.Security.Principal.SecurityIdentifier(
                   [byte[]]$domainRoot.Properties['objectSid'].Value, 0)).Value
Write-Output "DOMAIN_SID $domainSid"
if ($machineSid -eq $domainSid) {
    throw "This host's machine SID equals the domain SID. It is an ungeneralised clone of the image the first DC was built from and can never join. Rebuild from installation media, or sysprep /generalize. See LAB.md."
}

# Kerberos refuses more than five minutes of skew and reports it as a bad
# password. rootDSE's currentTime is the DC's own clock, in generalized time.
if ($dcTimeRaw) {
    $dcTime = [datetime]::ParseExact($dcTimeRaw.Substring(0, 14), 'yyyyMMddHHmmss', $null)
    $skew   = [math]::Abs(((Get-Date).ToUniversalTime() - $dcTime).TotalMinutes)
    Write-Output ("CLOCK_SKEW_MINUTES {0:n1}" -f $skew)
    if ($skew -gt 5) {
        throw "Clock skew of $skew minutes exceeds Kerberos' tolerance. Fix time sync first; it presents as a bad password."
    }
}

# --- the detached promotion -------------------------------------------------

$inner = @"
`$ErrorActionPreference = 'Stop'
function Log(`$m){ "`$(Get-Date -Format o)  `$m" | Out-File -FilePath C:\Windows\Temp\labsetup.log -Append -Encoding ascii }
try {
    Log 'START role install'
    `$r = Install-WindowsFeature AD-Domain-Services -IncludeManagementTools
    Log ('role success=' + `$r.Success + ' state=' + (Get-WindowsFeature AD-Domain-Services).InstallState)
    Log 'START promotion as additional DC'
    Import-Module ADDSDeployment
    `$dsrm = ConvertTo-SecureString '$DsrmPassword' -AsPlainText -Force
    `$cred = [System.Management.Automation.PSCredential]::new('$AdminUser', (ConvertTo-SecureString '$AdminPassword' -AsPlainText -Force))
    Install-ADDSDomainController -DomainName '$DomainName' -Credential `$cred ``
        -SafeModeAdministratorPassword `$dsrm -InstallDns:`$true -SiteName '$SiteName' ``
        -DatabasePath 'C:\Windows\NTDS' -LogPath 'C:\Windows\NTDS' -SysvolPath 'C:\Windows\SYSVOL' ``
        -NoGlobalCatalog:`$false -NoRebootOnCompletion:`$false -Force:`$true
    Log 'PROMOTION_SUBMITTED'
} catch { Log ('FAILED: ' + `$_.Exception.Message) }
"@

Remove-Item C:\Windows\Temp\labsetup.log -Force -ErrorAction SilentlyContinue
Set-Content -Path C:\Windows\Temp\labsetup.ps1 -Value $inner -Encoding ascii
schtasks /Create /TN LabSetup /SC ONCE /ST 00:00 /RU SYSTEM /RL HIGHEST /F `
    /TR 'powershell -NoProfile -ExecutionPolicy Bypass -File C:\Windows\Temp\labsetup.ps1' | Out-Null
schtasks /Run /TN LabSetup | Out-Null
Write-Output 'PROMOTION_LAUNCHED (poll C:\Windows\Temp\labsetup.log; host will reboot)'
