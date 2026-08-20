<#
.SYNOPSIS
    Re-open SSH after a host changes Windows firewall profile.
.DESCRIPTION
    Both promoting a DC and joining a domain move the host onto the **Domain**
    firewall profile. The OpenSSH inbound rule that Windows creates at install
    time is scoped to Private (and sometimes Public) only, so the moment the
    profile changes, SSH goes dark while WinRM, LDAP and ADWS keep answering.

    The failure is easy to misread as "the host did not come back from its
    reboot": it stops answering ping too, because the Domain profile blocks
    inbound ICMP by default.

    Run this over WinRM, which stays reachable:

        LAB_USER='CORP\Administrator' LAB_ADMIN_PW='...' \
            python3 winrun.py <ip> 09-open-ssh-firewall.ps1

    Run it after step 06 (promotion) and after step 07 (join).
.EXAMPLE
    ./psrun.sh s-client 09-open-ssh-firewall.ps1 90
#>
$ErrorActionPreference = 'Continue'

Write-Output ("host       = " + $env:COMPUTERNAME + " domain=" + (Get-CimInstance Win32_ComputerSystem).Domain)
Write-Output ("netprofile = " + ((Get-NetConnectionProfile).NetworkCategory -join ','))

$rule = Get-NetFirewallRule -Name 'OpenSSH-Server-In-TCP' -ErrorAction SilentlyContinue
if ($rule) {
    Write-Output ("fw_before  = enabled=" + $rule.Enabled + " profile=" + $rule.Profile)
    Set-NetFirewallRule -Name 'OpenSSH-Server-In-TCP' -Enabled True -Profile Any
    Write-Output 'fw_action  = enabled on all profiles'
} else {
    New-NetFirewallRule -Name 'OpenSSH-Server-In-TCP' -DisplayName 'OpenSSH Server (sshd)' `
        -Enabled True -Direction Inbound -Protocol TCP -Action Allow -LocalPort 22 -Profile Any | Out-Null
    Write-Output 'fw_action  = rule created'
}

Set-Service sshd -StartupType Automatic -ErrorAction SilentlyContinue
if ((Get-Service sshd -ErrorAction SilentlyContinue).Status -ne 'Running') { Start-Service sshd -ErrorAction SilentlyContinue }
Write-Output ("sshd       = " + (Get-Service sshd).Status)

# Inbound ICMP, so "is it back yet?" checks are meaningful on this profile.
Enable-NetFirewallRule -Name 'FPS-ICMP4-ERQ-In' -ErrorAction SilentlyContinue
Write-Output ("securechannel = " + (Test-ComputerSecureChannel -ErrorAction SilentlyContinue))
