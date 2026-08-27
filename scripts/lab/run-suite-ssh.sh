#!/usr/bin/env bash
# Run the acceptance suite HERE (on this machine) against the lab, with the
# provider reaching Active Directory over SSH to a Windows jump box.
#
# Like run-suite-psrp.sh and unlike run-suite.sh, nothing is shipped: the working
# tree is what runs, and no Go or Terraform is needed on the jump box. No Kerberos
# either — SSH key auth reaches the jump box, and because a key-based session
# lands as a *local* account with no delegatable domain token, the AD cmdlets
# authenticate with an explicit domain.credential (the svc account).
#
#   usage: run-suite-ssh.sh <test-pattern> <timeout-minutes>
#
# Two knobs pick the cell:
#   LAB_MODE  = warm (default) | cold   — warm needs pwsh 7 + the `powershell`
#               sshd subsystem on the jump box; cold runs pwsh -EncodedCommand.
#   LAB_PWSH  = the jump box PowerShell used by the cold path. Point it at
#               Windows PowerShell 5.1 to exercise the 5.1 cold cell. Ignored by
#               warm (the subsystem launches pwsh 7 itself).
set -euo pipefail

pattern=${1:?test pattern}
minutes=${2:-90}

creds=${LAB_CREDS:-$HOME/ad-lab-credentials.txt}
[[ -r $creds ]] || { echo "cannot read $creds" >&2; exit 1; }
cred() { awk -F'=' "/^$1[ \t]*=/{sub(/^[^=]*=[ \t]*/,\"\");print}" "$creds"; }

user=$(cred 'svc\.username'); pass=$(cred 'svc\.password')
[[ -n $user && -n $pass ]] || { echo "svc.username/svc.password missing from $creds" >&2; exit 1; }

host=${LAB_SSH_HOST:-192.168.50.31}
ssh_user=${LAB_SSH_USER:-Administrator}
ssh_key=${LAB_SSH_KEY:-$HOME/.ssh/tf_ad_lab}
mode=${LAB_MODE:-warm}
pwsh=${LAB_PWSH:-'C:\Program Files\PowerShell\7\pwsh.exe'}
dc=${LAB_DC_FQDN:-s-server.corp.local}
dc2=${LAB_DC2_FQDN:-s-server2.corp.local}
container=${LAB_CONTAINER:-OU=tfacc,DC=corp,DC=local}
denied=${LAB_DENIED_CONTAINER:-OU=tfacc-denied,DC=corp,DC=local}

[[ -r $ssh_key ]] || { echo "cannot read SSH key $ssh_key" >&2; exit 1; }

export TF_ACC=1
export AD_ACC_TRANSPORT=ssh
export AD_ACC_MODE="$mode"
export AD_ACC_SSH_HOST="$host"
export AD_ACC_SSH_USER="$ssh_user"
export AD_ACC_SSH_KEY_PATH="$ssh_key"
# The cold path runs this PowerShell over the SSH channel; warm ignores it. The
# suite's verification client (CheckDestroy) reaches AD the same way, so it uses
# this too.
export AD_ACC_PWSH_PATH="$pwsh"
export AD_ACC_CONTAINER="$container"
export AD_ACC_DENIED_CONTAINER="$denied"
export AD_ACC_SERVER="$dc"
export AD_ACC_SECOND_DC="$dc2"
# The double hop: the key-based SSH session holds no delegatable credential, so
# the AD cmdlets need this explicitly. It is also what confines the run to
# whatever this account is delegated.
export AD_ACC_USERNAME="$user"
export AD_ACC_PASSWORD="$pass"

echo "transport=ssh mode=$mode pwsh=$pwsh host=$host ssh_user=$ssh_user pattern=$pattern"
go test ./internal/provider/ -run "$pattern" -v -timeout "${minutes}m"
