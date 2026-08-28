#!/usr/bin/env bash
# Run the acceptance suite HERE against the lab over the winrm + COLD cell: a
# fresh Windows Remote Shell per op, feeding the script on stdin to
# `powershell -EncodedCommand` (no PSRP session configuration required).
#
# This cell separates the two identities cold makes distinct:
#   * TRANSPORT  = svc_tfcold (cred file cold.*) -- a WinRS-only account (member
#                  of Remote Management Users; NO AD privilege). The WinRM
#                  Kerberos login authenticates as this account, so we kinit it.
#   * AD cmdlet  = svc_tfacc  (cred file svc.*)  -- delegated on OU=tfacc,
#                  delivered as domain.credential (-Credential in the payload).
# configuration_name/language_mode are NOT set: cold uses the default WinRS
# shell and the provider rejects those knobs with mode = "cold".
#
#   usage: run-suite-winrm-cold.sh <test-pattern> <timeout-minutes>
set -euo pipefail

pattern=${1:?test pattern}
minutes=${2:-90}

creds=${LAB_CREDS:-$HOME/ad-lab-credentials.txt}
[[ -r $creds ]] || { echo "cannot read $creds" >&2; exit 1; }
cred() { awk -F'=' "/^$1[ \t]*=/{sub(/^[^=]*=[ \t]*/,\"\");print}" "$creds"; }

# Transport (WinRS) account and AD (cmdlet) account are DIFFERENT here.
tuser=$(cred 'cold\.username'); tpass=$(cred 'cold\.password')
auser=$(cred 'svc\.username');  apass=$(cred 'svc\.password')
[[ -n $tuser && -n $tpass ]] || { echo "cold.username/cold.password missing from $creds" >&2; exit 1; }
[[ -n $auser && -n $apass ]] || { echo "svc.username/svc.password missing from $creds" >&2; exit 1; }

host=${LAB_PSRP_HOST:-192.168.50.31}
spn=${LAB_PSRP_SPN:-HTTP/s-client.corp.local}
dc_ip=${LAB_DC_IP:-192.168.50.216}
dc=${LAB_DC_FQDN:-s-server.corp.local}
dc2=${LAB_DC2_FQDN:-s-server2.corp.local}
realm=${LAB_REALM:-CORP.LOCAL}
container=${LAB_CONTAINER:-OU=tfacc,DC=corp,DC=local}
denied=${LAB_DENIED_CONTAINER:-OU=tfacc-denied,DC=corp,DC=local}

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

export KRB5_CONFIG="$work/krb5.conf" KRB5CCNAME="FILE:$work/ccache"
# kinit the TRANSPORT account (the WinRM login), not the AD account.
printf '%s' "$tpass" | kinit "${tuser##*\\}@$realm" >/dev/null
klist -s || { echo 'kinit produced no usable ticket' >&2; exit 1; }

export TF_ACC=1
export AD_ACC_TRANSPORT=winrm
export AD_ACC_MODE=cold
export AD_ACC_WINRM_HOST="$host"
export AD_ACC_WINRM_SPN="$spn"
export AD_ACC_WINRM_USER="$tuser"
export AD_ACC_WINRM_PASSWORD="$tpass"
# Deliberately unset: cold uses the default WinRS shell, not a PSRP session config.
unset AD_ACC_WINRM_CONFIGURATION_NAME AD_ACC_WINRM_LANGUAGE_MODE
export AD_ACC_CONTAINER="$container"
export AD_ACC_DENIED_CONTAINER="$denied"
export AD_ACC_SERVER="$dc"
export AD_ACC_SECOND_DC="$dc2"
# domain.credential -- the AD identity the cmdlets run as.
export AD_ACC_USERNAME="$auser"
export AD_ACC_PASSWORD="$apass"

echo "cell=winrm+cold host=$host transport=$tuser ad=$auser pattern=$pattern"
go test ./internal/provider/ -run "$pattern" -v -timeout "${minutes}m"
