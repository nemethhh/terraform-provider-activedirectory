<#
.SYNOPSIS
    Point this host's DNS at the domain controller.
.DESCRIPTION
    A prospective domain controller or member must resolve the domain's SRV
    records before it can be promoted or joined, and only the domain's own DNS
    serves them. A host still pointing at a router or a public resolver fails
    with "the specified domain either does not exist or could not be contacted",
    which reads like a credential problem and is not one.

    Run this AFTER 02, not before: PowerShell 7 is fetched from the internet, and
    the domain's DNS may not forward outbound.

    The order matters for an additional domain controller. It points at the
    existing DC first and at itself second: a DC that resolves only through
    itself before its own DNS has replicated becomes an island, unable to find
    the partners it needs to replicate from. After promotion the loopback entry
    is the local DNS this host now runs.

    Address only, never a name: this runs before the host can resolve the DC's
    name, which is the entire problem it exists to fix.
.EXAMPLE
    ./psrun.sh s-server2 11-point-dns-at-dc.ps1 90 -- -DcAddress 192.168.50.216
#>
param(
    [Parameter(Mandatory)][string]$DcAddress,
    [string]$DomainName = 'corp.local',
    [switch]$IncludeLoopback = $true
)

$ErrorActionPreference = 'Stop'

$a = Get-NetIPAddress -AddressFamily IPv4 | Where-Object { $_.IPAddress -like '192.168.*' } | Select-Object -First 1
if (-not $a) { throw 'no 192.168.x.x address found' }
$idx = $a.InterfaceIndex

$servers = @($DcAddress)
if ($IncludeLoopback) { $servers += '127.0.0.1' }

Write-Output ("DNS_BEFORE " + ((Get-DnsClientServerAddress -InterfaceIndex $idx -AddressFamily IPv4).ServerAddresses -join ','))
Set-DnsClientServerAddress -InterfaceIndex $idx -ServerAddresses $servers
Clear-DnsClientCache
Write-Output ("DNS_AFTER  " + ((Get-DnsClientServerAddress -InterfaceIndex $idx -AddressFamily IPv4).ServerAddresses -join ','))

# Prove it before the caller moves on: an unresolvable domain here is a
# five-minute fix, and an hour of confusion once promotion has started.
$srv = Resolve-DnsName -Name "_ldap._tcp.dc._msdcs.$DomainName" -Type SRV -ErrorAction SilentlyContinue
if (-not $srv) { throw "DNS is set but $DomainName SRV records still do not resolve through $DcAddress." }
Write-Output ("SRV_OK " + (($srv | Where-Object NameTarget | ForEach-Object NameTarget) -join ','))
