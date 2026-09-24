# Register the `powershell` sshd subsystem that ssh warm mode connects to, and
# restart sshd so it takes effect. Idempotent: an existing line is left alone.
#
# The path is the 8.3 short form because sshd_config splits Subsystem on
# whitespace, and "C:\Program Files" would end the command at "C:\Program".
$ErrorActionPreference = 'Stop'
$config = 'C:\ProgramData\ssh\sshd_config'
$pwsh = 'C:\Program Files\PowerShell\7\pwsh.exe'
if (-not (Test-Path $pwsh)) { throw "PowerShell 7 is not installed at $pwsh (make lab-pwsh first)" }

$short = (New-Object -ComObject Scripting.FileSystemObject).GetFile($pwsh).ShortPath
$line = "Subsystem`tpowershell`t$short -sshs -NoLogo"

$text = Get-Content -Raw $config
if ($text -notmatch '(?m)^\s*Subsystem\s+powershell\s') {
    # Subsystem lines must precede any Match block, which ends the global section.
    if ($text -match '(?m)^\s*Match\s') {
        $text = $text -replace '(?m)^(\s*Match\s)', "$line`r`n`$1"
    } else {
        $text = $text.TrimEnd() + "`r`n$line`r`n"
    }
    Set-Content -Path $config -Value $text -Encoding ascii -NoNewline
    Restart-Service sshd
}
Select-String -Path $config -Pattern '^\s*Subsystem' | ForEach-Object { $_.Line }
