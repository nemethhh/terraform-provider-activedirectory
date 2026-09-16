#!/usr/bin/env bash
# Run the acceptance suite HERE (on this machine, Linux) against the lab, with
# the provider driving the PSOpenAD module over LDAP instead of the
# ActiveDirectory module over AD Web Services.
#
# This is a cell shape no other target has: the `local` transport, running here.
# Every other local cell ships the tree to the Windows member and runs there,
# because the ActiveDirectory module needs Windows. PSOpenAD does not.
#
#   usage: run-suite-psopenad.sh <test-pattern> <timeout-minutes>
#
# Prerequisite: PSOpenAD 0.8.0+ from github.com/nemethhh/PSOpenAD must be
# installed for the pwsh that runs here. Upstream 0.7.0 has no
# `Set-OpenADObject -SecurityMask`, so every descriptor write -- ACLs, OU
# protection, can_change_password -- fails against it. See LAB.md.
set -euo pipefail

pattern=${1:?test pattern}
minutes=${2:-90}

creds=${LAB_CREDS:-$HOME/ad-lab-credentials.txt}
[[ -r $creds ]] || { echo "cannot read $creds" >&2; exit 1; }
cred() { awk -F'=' "/^$1[ \t]*=/{sub(/^[^=]*=[ \t]*/,\"\");print}" "$creds"; }

user=$(cred 'svc\.username'); pass=$(cred 'svc\.password')
[[ -n $user && -n $pass ]] || { echo "svc.username/svc.password missing from $creds" >&2; exit 1; }

pwsh_path=${LAB_PWSH:-pwsh}
dc_ip=${LAB_DC_IP:-192.168.50.216}
dc=${LAB_DC_FQDN:-s-server.corp.local}
dc2=${LAB_DC2_FQDN:-s-server2.corp.local}
realm=${LAB_REALM:-CORP.LOCAL}
container=${LAB_CONTAINER:-OU=tfacc,DC=corp,DC=local}
denied=${LAB_DENIED_CONTAINER:-OU=tfacc-denied,DC=corp,DC=local}

# Fail early and legibly rather than 40 minutes into a suite, on the one
# prerequisite this cell has that no other cell has.
if ! "$pwsh_path" -NoProfile -c '(Get-Command Set-OpenADObject -ErrorAction Stop).Parameters.ContainsKey("SecurityMask")' 2>/dev/null | grep -qi true; then
  echo "PSOpenAD is missing or too old: Set-OpenADObject has no -SecurityMask." >&2
  echo "Install 0.8.0+ from github.com/nemethhh/PSOpenAD; see LAB.md." >&2
  exit 1
fi

work=$(mktemp -d); trap 'rm -rf "$work"' EXIT
cat > "$work/krb5.conf" <<EOF
[libdefaults]
  default_realm = $realm
  dns_lookup_kdc = false
  dns_lookup_realm = false
  rdns = false
[realms]
  $realm = {
    kdc = $dc_ip
  }
[domain_realm]
  .${realm,,} = $realm
  ${realm,,} = $realm
EOF

# A private ticket cache, never the caller's. New-OpenADSession authenticates
# with this ticket, so no credential reaches the generated Terraform
# configuration and nothing secret lands on disk: AD_ACC_USERNAME and
# AD_ACC_PASSWORD are deliberately NOT exported here. The local transport's pwsh
# child inherits KRB5CCNAME from this environment, which is how it gets there.
export KRB5_CONFIG="$work/krb5.conf" KRB5CCNAME="FILE:$work/ccache"
printf '%s' "$pass" | kinit "${user##*\\}@$realm" >/dev/null
klist -s || { echo 'kinit produced no usable ticket' >&2; exit 1; }

export TF_ACC=1
export AD_ACC_TRANSPORT=local
export AD_ACC_DIALECT=psopenad
export AD_ACC_MODE="${LAB_MODE:-warm}"
export AD_ACC_PWSH_PATH="$pwsh_path"
export AD_ACC_CONTAINER="$container"
export AD_ACC_DENIED_CONTAINER="$denied"
export AD_ACC_SERVER="$dc"
export AD_ACC_SECOND_DC="$dc2"

# GOWORK=off: the cell exercises the released go-adpwsh, not the sibling
# checkout -- the same rule the ssh and winrm cells follow.
echo "dialect=psopenad transport=local mode=$AD_ACC_MODE server=$dc pattern=$pattern"
GOWORK=off go test ./internal/provider/ -run "$pattern" -v -timeout "${minutes}m"
