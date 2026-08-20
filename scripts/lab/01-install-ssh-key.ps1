<#
.SYNOPSIS
    Install an SSH public key for an administrator account on Windows.
.DESCRIPTION
    ssh-copy-id does NOT work for members of the Administrators group. Windows
    OpenSSH ignores ~/.ssh/authorized_keys for admins and reads
    C:\ProgramData\ssh\administrators_authorized_keys instead, which must be
    writable only by Administrators and SYSTEM. Getting the ACL wrong makes sshd
    silently refuse the key with no useful error.

    Run this once per host, authenticating with the local Administrator password.
    No sshd restart is required.
.EXAMPLE
    ./psrun.sh s-server 01-install-ssh-key.ps1 60 -- -PublicKey "$(cat ~/.ssh/tf_ad_lab.pub)"
#>
param([Parameter(Mandatory)][string]$PublicKey)

$ErrorActionPreference = 'Stop'
$f   = 'C:\ProgramData\ssh\administrators_authorized_keys'
$dir = Split-Path $f
if (-not (Test-Path $dir)) { New-Item -ItemType Directory -Path $dir -Force | Out-Null }

$existing = @()
if (Test-Path $f) { $existing = @(Get-Content $f -ErrorAction SilentlyContinue) }
if ($existing -notcontains $PublicKey) {
    Add-Content -Path $f -Value $PublicKey -Encoding ascii
    Write-Output 'KEY_ADDED'
} else {
    Write-Output 'KEY_ALREADY_PRESENT'
}

# Administrators + SYSTEM only; inheritance removed. sshd refuses the file otherwise.
icacls $f /inheritance:r /grant 'Administrators:F' /grant 'SYSTEM:F' | Out-Null
Write-Output ('KEYCOUNT=' + @(Get-Content $f).Count)
Write-Output ('SSHD=' + (Get-Service sshd).Status)
