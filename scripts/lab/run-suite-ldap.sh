#!/usr/bin/env bash
# Run the acceptance suite over the native LDAP connection.
#
# Unlike every other runner here this one needs no jump box and ships nothing:
# the provider speaks LDAPS to the domain controller from wherever Terraform
# runs, so the suite executes locally against the working tree.
#
#   usage: run-suite-ldap.sh <test-pattern> <timeout-minutes>
set -euo pipefail

pattern=${1:-TestAcc}
minutes=${2:-60}

creds=${LAB_CREDS:-$HOME/ad-lab-credentials.txt}
cred() { awk -F'=' "/^$1[ \t]*=/{sub(/^[^=]*=[ \t]*/,\"\");print}" "$creds"; }

dc=${LAB_DC_FQDN:-s-server1.corp.local}
dc2=${LAB_DC2_FQDN:-s-server2.corp.local}
domain=${LAB_DOMAIN:-corp.local}
ca=${LAB_CA_FILE:-$HOME/.config/ad-lab/corp-lab-ca.pem}

export TF_ACC=1
export AD_ACC_CONNECTION=ldap
export AD_ACC_LDAP_SERVER=${LAB_LDAP_SERVER:-$dc}
export AD_ACC_SERVER=$dc
export AD_ACC_SECOND_DC=$dc2
export AD_ACC_CONTAINER=${LAB_CONTAINER:-OU=tfacc,DC=corp,DC=local}
export AD_ACC_DENIED_CONTAINER=${LAB_DENIED_CONTAINER:-OU=tfacc-denied,DC=corp,DC=local}

# Verify the DC's certificate rather than skipping, when the CA has been cached
# by `make lab-ca-cert`. Skipping would leave the verification path untested.
if [[ -s $ca ]]; then
  export AD_ACC_LDAP_CA_FILE=$ca
else
  echo "WARNING: $ca missing; falling back to insecure_skip_verify" >&2
  export AD_ACC_LDAP_INSECURE=true
fi

# The GSSAPI bind is refused by Windows Server 2025 (see LAB.md), so a simple
# bind is used when no ticket is present. KRB5CCNAME with a live ticket selects
# the Kerberos path instead.
if [[ -n ${KRB5CCNAME:-} ]] && klist -s 2>/dev/null; then
  echo "auth: kerberos (ticket in $KRB5CCNAME)"
else
  AD_ACC_LDAP_USERNAME=$(cred svc.username)
  AD_ACC_LDAP_PASSWORD=$(cred svc.password)
  [[ -n $AD_ACC_LDAP_USERNAME && -n $AD_ACC_LDAP_PASSWORD ]] || {
    echo "svc.username/svc.password missing from $creds" >&2; exit 1; }
  # A UPN binds regardless of which naming context the account sits in.
  AD_ACC_LDAP_USERNAME="${AD_ACC_LDAP_USERNAME#*\\}@$domain"
  export AD_ACC_LDAP_USERNAME AD_ACC_LDAP_PASSWORD
  echo "auth: simple as $AD_ACC_LDAP_USERNAME"
fi

echo "server=$AD_ACC_LDAP_SERVER container=$AD_ACC_CONTAINER pattern=$pattern"
exec go test ./internal/provider/ -run "$pattern" -v -count=1 -timeout "${minutes}m"
