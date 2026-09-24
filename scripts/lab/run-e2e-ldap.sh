#!/usr/bin/env bash
# Run the e2e layer over the native LDAP connection, from here.
#
# Each scenario binds as its own delegated principal with a simple bind, so the
# run needs no admin credential and no Kerberos ticket.
#
#   usage: run-e2e-ldap.sh <test-pattern> <timeout-minutes>
set -euo pipefail

pattern=${1:-TestAccE2E}
minutes=${2:-60}

creds=${LAB_CREDS:-$HOME/ad-lab-credentials.txt}
cred() { awk -F'=' "/^$1[ \t]*=/{sub(/^[^=]*=[ \t]*/,\"\");print}" "$creds"; }

dc=${LAB_DC_FQDN:-s-server1.corp.local}
ca=${LAB_CA_FILE:-$HOME/.config/ad-lab/corp-lab-ca.pem}
e2e_pw=$(cred 'e2e\.password')
[[ -n $e2e_pw ]] || { echo "e2e.password missing from $creds" >&2; exit 1; }

export TF_ACC=1
export AD_ACC_CONNECTION=ldap
export AD_ACC_LDAP_TLS=${LAB_LDAP_TLS:-ldaps}
[[ -n ${LAB_LDAP_PORT:-} ]] && export AD_ACC_LDAP_PORT=$LAB_LDAP_PORT
export AD_ACC_LDAP_SERVER=${LAB_LDAP_SERVER:-$dc}
export AD_ACC_SERVER=$dc
if [[ -s $ca ]]; then
  export AD_ACC_LDAP_CA_FILE=$ca
else
  echo "WARNING: $ca missing; falling back to insecure_skip_verify" >&2
  export AD_ACC_LDAP_INSECURE=true
fi

export AD_E2E_CONTAINER=${LAB_E2E_CONTAINER:-OU=e2e,DC=corp,DC=local}
export AD_E2E_ALPHA_USERNAME='CORP\svc_e2e_alpha'
export AD_E2E_BETA_USERNAME='CORP\svc_e2e_beta'
export AD_E2E_LIMITED_USERNAME='CORP\svc_e2e_limited'
export AD_E2E_ALPHA_PASSWORD=$e2e_pw AD_E2E_BETA_PASSWORD=$e2e_pw AD_E2E_LIMITED_PASSWORD=$e2e_pw

echo "server=$AD_ACC_LDAP_SERVER tls=$AD_ACC_LDAP_TLS${AD_ACC_LDAP_PORT:+:$AD_ACC_LDAP_PORT} e2e=$AD_E2E_CONTAINER pattern=$pattern"
exec go test ./internal/provider/ -run "$pattern" -v -count=1 -timeout "${minutes}m"
