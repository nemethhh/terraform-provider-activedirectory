#!/usr/bin/env bash
# Run the acceptance suite, or the sweeper, on the member server.
#
# The environment the suite needs is assembled here rather than in the makefile:
# generating PowerShell through make's quoting rules costs more than it saves,
# and this is the file a person actually has to read when a run misbehaves.
#
#   usage: run-suite.sh <test-pattern|--sweep> <timeout-minutes>
#
# Credentials come from the lab credentials file, never from arguments or the
# repository. They reach the host as PowerShell arguments, so they are visible
# in its process list for the duration of the call.
set -euo pipefail

pattern=${1:?test pattern, or --sweep}
minutes=${2:-90}
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

creds=${LAB_CREDS:-$HOME/ad-lab-credentials.txt}
[[ -r $creds ]] || { echo "cannot read $creds" >&2; exit 1; }
cred() { awk -F'=' "/^$1[ \t]*=/{sub(/^[^=]*=[ \t]*/,\"\");print}" "$creds"; }

member=${LAB_MEMBER:-s-client}
container=${LAB_CONTAINER:-OU=tfacc,DC=corp,DC=local}
denied=${LAB_DENIED_CONTAINER:-OU=tfacc-denied,DC=corp,DC=local}
dc=${LAB_DC_FQDN:-s-server.corp.local}
dc2=${LAB_DC2_FQDN:-s-server2.corp.local}
pwsh_path=${LAB_PWSH:-'C:\Program Files\PowerShell\7\pwsh.exe'}

user=$(cred 'svc\.username'); pass=$(cred 'svc\.password')
[[ -n $user && -n $pass ]] || { echo "svc.username/svc.password missing from $creds" >&2; exit 1; }

if [[ $pattern == --sweep ]]; then
    go_cmd='go test ./internal/provider -v -sweep=domain -timeout 30m'
    label=SWEEP
else
    go_cmd="go test ./internal/provider/ -run $pattern -v -timeout ${minutes}m"
    label=GOTEST
fi

script=$(mktemp /tmp/lab-run-XXXXXX.ps1)
trap 'rm -f "$script"' EXIT
cat > "$script" <<PS1
param([Parameter(Mandatory)][string]\$Username,[Parameter(Mandatory)][string]\$Password)
\$env:Path = [Environment]::GetEnvironmentVariable('Path','Machine')
Set-Location 'C:\src\provider'
\$env:TF_ACC                  = '1'
\$env:AD_ACC_CONTAINER        = '$container'
\$env:AD_ACC_DENIED_CONTAINER = '$denied'
\$env:AD_ACC_SECOND_DC        = '$dc2'
\$env:AD_ACC_SERVER           = '$dc'
\$env:AD_ACC_USERNAME         = \$Username
\$env:AD_ACC_PASSWORD         = \$Password
\$env:AD_ACC_PWSH_PATH        = '$pwsh_path'
\$log = 'C:\Windows\Temp\lab-run.log'
Remove-Item \$log -Force -ErrorAction SilentlyContinue
\$sw = [Diagnostics.Stopwatch]::StartNew()
cmd /c "$go_cmd > \`"\$log\`" 2>&1"
Write-Output "${label}_EXIT=\$LASTEXITCODE ELAPSED=\$([int]\$sw.Elapsed.TotalSeconds)s"
Get-Content \$log | Select-String -Pattern '^(--- (PASS|FAIL|SKIP)|ok |FAIL|panic|sweep:)|\[INFO\] sweep'
PS1

# psrun's own timeout must outlast the test timeout, or the ssh session is cut
# before go test can report.
bash "$here/psrun.sh" "$member" "$script" $(( minutes * 60 + 300 )) -- \
    -Username "$user" -Password "$pass" 2>&1 |
    grep -vE 'WARNING: |vulnerable to|openssh.com/pq.html|^\*\* '
