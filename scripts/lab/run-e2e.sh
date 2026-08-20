#!/usr/bin/env bash
# Run the e2e suite, or its sweeper, on the member server.
#
# A normal run needs NO admin credentials: each scenario authenticates as its
# own delegated principal (all sharing e2e.password), and CheckDestroy verifies
# as that same principal. Admin is read only for --sweep, whose job is to remove
# whatever any principal left behind after a crash.
#
#   usage: run-e2e.sh <test-pattern|--sweep> <timeout-minutes>
set -euo pipefail

pattern=${1:?test pattern, or --sweep}
minutes=${2:-90}
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

creds=${LAB_CREDS:-$HOME/ad-lab-credentials.txt}
[[ -r $creds ]] || { echo "cannot read $creds" >&2; exit 1; }
cred() { awk -F'=' "/^$1[ \t]*=/{sub(/^[^=]*=[ \t]*/,\"\");print}" "$creds"; }

member=${LAB_MEMBER:-s-client}
e2e_container=${LAB_E2E_CONTAINER:-OU=e2e,DC=corp,DC=local}
dc=${LAB_DC_FQDN:-s-server.corp.local}
pwsh_path=${LAB_PWSH:-'C:\Program Files\PowerShell\7\pwsh.exe'}

e2e_pw=$(cred 'e2e\.password')
[[ -n $e2e_pw ]] || { echo "e2e.password missing from $creds" >&2; exit 1; }

if [[ $pattern == --sweep ]]; then
    admin_user=$(cred 'admin\.username'); admin_user=${admin_user:-CORP\\Administrator}
    admin_pw=$(cred 'admin\.password')
    [[ -n $admin_pw ]] || { echo "admin.password missing from $creds (needed for --sweep)" >&2; exit 1; }
    go_cmd='go test ./internal/provider -v -sweep=domain -sweep-run=activedirectory_e2e -timeout 30m'
    label=SWEEP
else
    admin_user=''; admin_pw=''
    go_cmd="go test ./internal/provider/ -run $pattern -v -timeout ${minutes}m"
    label=GOTEST
fi

script=$(mktemp /tmp/lab-e2e-XXXXXX.ps1)
trap 'rm -f "$script"' EXIT
cat > "$script" <<PS1
param(
  [Parameter(Mandatory)][string]\$E2EPassword,
  [string]\$AdminUser = '',
  [string]\$AdminPassword = ''
)
\$env:Path = [Environment]::GetEnvironmentVariable('Path','Machine')
Set-Location 'C:\src\provider'
\$env:TF_ACC                   = '1'
\$env:AD_ACC_SERVER            = '$dc'
\$env:AD_ACC_PWSH_PATH         = '$pwsh_path'
\$env:AD_E2E_CONTAINER         = '$e2e_container'
\$env:AD_E2E_ALPHA_USERNAME    = 'CORP\svc_e2e_alpha'
\$env:AD_E2E_ALPHA_PASSWORD    = \$E2EPassword
\$env:AD_E2E_BETA_USERNAME     = 'CORP\svc_e2e_beta'
\$env:AD_E2E_BETA_PASSWORD     = \$E2EPassword
\$env:AD_E2E_LIMITED_USERNAME  = 'CORP\svc_e2e_limited'
\$env:AD_E2E_LIMITED_PASSWORD  = \$E2EPassword
if (\$AdminUser) {
    # Only set for --sweep: the sweeper reads AD_ACC_USERNAME/PASSWORD and must
    # be able to delete objects any principal created.
    \$env:AD_ACC_CONTAINER = '$e2e_container'
    \$env:AD_ACC_USERNAME  = \$AdminUser
    \$env:AD_ACC_PASSWORD  = \$AdminPassword
}
\$log = 'C:\Windows\Temp\lab-e2e.log'
Remove-Item \$log -Force -ErrorAction SilentlyContinue
\$sw = [Diagnostics.Stopwatch]::StartNew()
cmd /c "$go_cmd > \`"\$log\`" 2>&1"
Write-Output "${label}_EXIT=\$LASTEXITCODE ELAPSED=\$([int]\$sw.Elapsed.TotalSeconds)s"
Get-Content \$log | Select-String -Pattern '^(--- (PASS|FAIL|SKIP)|ok |FAIL|panic|sweep:)|\[INFO\] sweep'
PS1

bash "$here/psrun.sh" "$member" "$script" $(( minutes * 60 + 300 )) -- \
    -E2EPassword "$e2e_pw" -AdminUser "$admin_user" -AdminPassword "$admin_pw" 2>&1 |
    grep -vE 'WARNING: |vulnerable to|openssh.com/pq.html|^\*\* '
