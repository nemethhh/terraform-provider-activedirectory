#Requires -Version 5
# Grant SeEnableDelegationPrivilege to svc_tfacc via the Default Domain
# Controllers Policy, so the acceptance suite can set trusted_for_delegation /
# msDS-AllowedToDelegateTo on computer objects as the delegated (non-admin) svc
# account. Without this right, New-/Set-ADComputer refuses those two attributes
# with 0x522 "A required privilege is not held by the client" (Win32 1314).
# Idempotent. Run on a DC (admin). Lab reconfiguration.
#
# IMPORTANT: SeEnableDelegationPrivilege changes take effect only after the DC
# is REBOOTED -- gpupdate alone updates the policy database but LSASS keeps the
# privilege set it built at boot, so a fresh network logon still lacks the
# right until the DC restarts. Reboot the pinned DC (AD_ACC_SERVER) once after
# running this, then the delegation-setting acc steps pass as svc.
$ErrorActionPreference = 'Stop'
Import-Module ActiveDirectory

$svcSid = (Get-ADUser svc_tfacc).SID.Value
$guid   = '{6AC1786C-016F-11D2-945F-00C04FB984F9}'   # Default Domain Controllers Policy
$polDir = "C:\Windows\SYSVOL\sysvol\corp.local\Policies\$guid"
$inf    = "$polDir\MACHINE\Microsoft\Windows NT\SecEdit\GptTmpl.inf"
$gpt    = "$polDir\GPT.INI"

Write-Output "svc SID: $svcSid"

$lines = Get-Content -LiteralPath $inf -Encoding Unicode
$idx = ($lines | Select-String -SimpleMatch 'SeEnableDelegationPrivilege').LineNumber
if (-not $idx) { throw 'SeEnableDelegationPrivilege line not found; aborting' }
$i = $idx - 1
$cur = $lines[$i]
Write-Output "before: $cur"

if ($cur -match [regex]::Escape($svcSid)) {
    Write-Output 'ALREADY GRANTED (svc SID present); no GptTmpl change'
    $changed = $false
} else {
    Copy-Item -LiteralPath $inf -Destination "$inf.bak" -Force
    $lines[$i] = "$cur,*$svcSid"
    Set-Content -LiteralPath $inf -Value $lines -Encoding Unicode
    Write-Output ("after:  " + $lines[$i])
    Write-Output "backup: $inf.bak"
    $changed = $true
}

if ($changed) {
    # Bump the computer-config version so the change is unambiguously newer.
    $gpo  = Get-ADObject -Identity "CN=$guid,CN=Policies,CN=System,DC=corp,DC=local" -Properties versionNumber
    $newVer = [int]$gpo.versionNumber + 1
    Set-ADObject -Identity $gpo.DistinguishedName -Replace @{ versionNumber = $newVer }
    $gptText = Get-Content -LiteralPath $gpt
    $gptText = $gptText -replace '^Version=.*', "Version=$newVer"
    Set-Content -LiteralPath $gpt -Value $gptText
    Write-Output "bumped GPO versionNumber -> $newVer (AD + GPT.INI)"
}

Write-Output '----- gpupdate /force on this DC -----'
$out = gpupdate /force /target:computer 2>&1 | Out-String
Write-Output $out.Trim()

Write-Output '----- verify effective privilege holders on this DC -----'
$tmp = "C:\Windows\Temp\secpol-verify.inf"
secedit /export /areas USER_RIGHTS /cfg $tmp | Out-Null
$verify = Get-Content -LiteralPath $tmp -Encoding Unicode
($verify | Select-String -SimpleMatch 'SeEnableDelegationPrivilege') | ForEach-Object { Write-Output ("effective: " + $_.Line) }
Write-Output ("svc in effective holders (by name svc_tfacc or SID) = " + [bool](($verify -match [regex]::Escape($svcSid)) -or ($verify -match 'svc_tfacc')))
Remove-Item $tmp -Force -ErrorAction SilentlyContinue
Write-Output 'GRANT_DONE'
Write-Output '*** REBOOT REQUIRED: restart the pinned DC (AD_ACC_SERVER) for SeEnableDelegationPrivilege to take effect in LSASS. ***'
