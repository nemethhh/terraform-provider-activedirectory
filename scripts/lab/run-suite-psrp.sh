#!/usr/bin/env bash
# Run the acceptance suite HERE (on this machine) against the lab, with the
# provider reaching Active Directory over PSRP/WinRM.
#
# Unlike run-suite.sh, nothing is shipped: the working tree is what runs, and no
# Go or Terraform is needed on any Windows host. What this does need is a
# Kerberos ticket, so it writes a lab krb5.conf and kinits into a PRIVATE ticket
# cache -- the caller's own cache is never touched.
#
#   usage: run-suite-psrp.sh <test-pattern> <timeout-minutes>
#
# LAB_PSRP_CONFIG selects the WinRM session configuration, which is how the
# engine is chosen: a 5.1 endpoint or a PowerShell 7 one.
set -euo pipefail

pattern=${1:?test pattern}
minutes=${2:-90}

creds=${LAB_CREDS:-$HOME/ad-lab-credentials.txt}
[[ -r $creds ]] || { echo "cannot read $creds" >&2; exit 1; }
cred() { awk -F'=' "/^$1[ \t]*=/{sub(/^[^=]*=[ \t]*/,\"\");print}" "$creds"; }

user=$(cred 'svc\.username'); pass=$(cred 'svc\.password')
[[ -n $user && -n $pass ]] || { echo "svc.username/svc.password missing from $creds" >&2; exit 1; }

host=${LAB_PSRP_HOST:-192.168.50.31}
spn=${LAB_PSRP_SPN:-HTTP/s-client.corp.local}
config=${LAB_PSRP_CONFIG:-AdObjects51}
langmode=${LAB_PSRP_LANGUAGE_MODE:-}
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
printf '%s' "$pass" | kinit "${user##*\\}@$realm" >/dev/null
klist -s || { echo 'kinit produced no usable ticket' >&2; exit 1; }

export TF_ACC=1
export AD_ACC_TRANSPORT=winrm
# The winrm transport is warm-only today; LAB_MODE exists for symmetry with the
# other runners and defaults to warm. (winrm + cold is refused by the provider.)
export AD_ACC_MODE="${LAB_MODE:-warm}"
export AD_ACC_WINRM_HOST="$host"
export AD_ACC_WINRM_SPN="$spn"
export AD_ACC_WINRM_USER="$user"
export AD_ACC_WINRM_PASSWORD="$pass"
export AD_ACC_WINRM_CONFIGURATION_NAME="$config"
# Empty unless LAB_PSRP_LANGUAGE_MODE=constrained is passed; when set, the acc
# harness emits `language_mode = "constrained"` into the provider's winrm block so
# the suite runs against a ConstrainedLanguage sandbox endpoint.
export AD_ACC_WINRM_LANGUAGE_MODE="$langmode"
export AD_ACC_CONTAINER="$container"
export AD_ACC_DENIED_CONTAINER="$denied"
export AD_ACC_SERVER="$dc"
export AD_ACC_SECOND_DC="$dc2"
# The double hop: the session on the management host holds no delegatable
# credential, so the AD cmdlets need this explicitly. It is also what confines
# the run to whatever this account is delegated.
export AD_ACC_USERNAME="$user"
export AD_ACC_PASSWORD="$pass"

echo "endpoint=$config host=$host user=$user pattern=$pattern"
go test ./internal/provider/ -run "$pattern" -v -timeout "${minutes}m"
