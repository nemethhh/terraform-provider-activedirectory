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
# The TLS form and port default to LDAPS on 636; set LAB_LDAP_TLS=starttls with
# LAB_LDAP_PORT=389 to exercise the StartTLS handshake instead. Everything after
# the handshake is the same code, so one lifecycle suite proves it.
export AD_ACC_LDAP_TLS=${LAB_LDAP_TLS:-ldaps}
[[ -n ${LAB_LDAP_PORT:-} ]] && export AD_ACC_LDAP_PORT=$LAB_LDAP_PORT
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
# LAB_LDAP_AUTH names the bind explicitly: kerberos (the default when a ticket
# is present), simple, or ntlm. It exists so the ntlm path can be run at all —
# it is offered in the schema and, until this, had never been exercised.
if [[ ${LAB_LDAP_AUTH:-} == ntlm ]]; then
  AD_ACC_LDAP_AUTH=ntlm
  AD_ACC_LDAP_USERNAME=$(cred svc.username)
  AD_ACC_LDAP_PASSWORD=$(cred svc.password)
  [[ -n $AD_ACC_LDAP_USERNAME && -n $AD_ACC_LDAP_PASSWORD ]] || {
    echo "svc.username/svc.password missing from $creds" >&2; exit 1; }
  export AD_ACC_LDAP_AUTH AD_ACC_LDAP_USERNAME AD_ACC_LDAP_PASSWORD
  echo "auth: ntlm as $AD_ACC_LDAP_USERNAME"
elif [[ -n ${KRB5CCNAME:-} ]] && klist -s 2>/dev/null; then
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

echo "server=$AD_ACC_LDAP_SERVER tls=$AD_ACC_LDAP_TLS${AD_ACC_LDAP_PORT:+:$AD_ACC_LDAP_PORT} container=$AD_ACC_CONTAINER pattern=$pattern"
exec go test ./internal/provider/ -run "$pattern" -v -count=1 -timeout "${minutes}m"
