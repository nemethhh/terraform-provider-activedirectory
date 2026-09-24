# The winrm cold cell's transport identity: svc_tfcold, a WinRS-only account.
#
# Run twice, once per role. On the DC (-Role account) it creates the account with
# no AD privilege; on the WinRM target (-Role winrs) it adds the account to
# Remote Management Users, which is what a Windows Remote Shell needs. The AD
# work the cell does is delegated to svc_tfacc, delivered as domain.credential,
# so this account must never gain rights on OU=tfacc. Idempotent.
param(
    [Parameter(Mandatory = $true)][ValidateSet('account', 'winrs')][string]$Role,
    [string]$Password = ''
)
$ErrorActionPreference = 'Stop'
$sam = 'svc_tfcold'

if ($Role -eq 'account') {
    if (-not $Password) { throw '-Password is required for -Role account' }
    $secure = ConvertTo-SecureString $Password -AsPlainText -Force
    $existing = Get-ADUser -Filter "SamAccountName -eq '$sam'"
    if ($existing) {
        Set-ADAccountPassword -Identity $existing -NewPassword $secure -Reset
        Enable-ADAccount -Identity $existing
    } else {
        New-ADUser -Name $sam -SamAccountName $sam -UserPrincipalName "$sam@$((Get-ADDomain).DNSRoot)" `
            -AccountPassword $secure -Enabled $true -PasswordNeverExpires $true `
            -Description 'lab: winrm cold transport identity (WinRS only, no AD rights)'
    }
    Get-ADUser $sam | Select-Object -ExpandProperty DistinguishedName
} else {
    # Membership alone opens no shell: the stock RootSDDL grants only
    # Administrators (and Interactive read), and WinRS checks it. Grant Remote
    # Management Users execute+read, the smallest right a remote shell needs.
    $rmu = 'S-1-5-32-580'
    $root = (Get-Item WSMan:\localhost\Service\RootSDDL).Value
    if ($root -notmatch "\(A;;[A-Z]*GX[A-Z]*;;;$rmu\)") {
        $root = $root -replace 'D:P', "D:P(A;;GXGR;;;$rmu)"
        Set-Item WSMan:\localhost\Service\RootSDDL -Value $root -Force
    }
    $member = "$env:USERDOMAIN\$sam"
    if (-not (Get-LocalGroupMember -Group 'Remote Management Users' -ErrorAction SilentlyContinue |
            Where-Object Name -eq $member)) {
        Add-LocalGroupMember -Group 'Remote Management Users' -Member $member
    }
    Get-LocalGroupMember -Group 'Remote Management Users' | Select-Object -ExpandProperty Name
}
