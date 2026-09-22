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

# LAB_LDAP_AUTH names the bind explicitly: kerberos (ticket cache, the default
# when a ticket is present), kerberos-password, kerberos-keytab, ntlm, or
# unset (a ticket if there is one, a simple bind otherwise).
case ${LAB_LDAP_AUTH:-} in
ntlm)
  AD_ACC_LDAP_AUTH=ntlm
  AD_ACC_LDAP_USERNAME=$(cred svc.username)
  AD_ACC_LDAP_PASSWORD=$(cred svc.password)
  [[ -n $AD_ACC_LDAP_USERNAME && -n $AD_ACC_LDAP_PASSWORD ]] || {
    echo "svc.username/svc.password missing from $creds" >&2; exit 1; }
  export AD_ACC_LDAP_AUTH AD_ACC_LDAP_USERNAME AD_ACC_LDAP_PASSWORD
  echo "auth: ntlm as $AD_ACC_LDAP_USERNAME"
  ;;
kerberos-password)
  # No KRB5CCNAME on purpose. A ticket in the environment would make this cell
  # pass through the ccache path and prove nothing about the credential one.
  unset KRB5CCNAME
  AD_ACC_LDAP_AUTH=kerberos
  AD_ACC_LDAP_USERNAME=$(cred svc.username)
  AD_ACC_LDAP_PASSWORD=$(cred svc.password)
  [[ -n $AD_ACC_LDAP_USERNAME && -n $AD_ACC_LDAP_PASSWORD ]] || {
    echo "svc.username/svc.password missing from $creds" >&2; exit 1; }
  AD_ACC_LDAP_USERNAME="${AD_ACC_LDAP_USERNAME#*\\}"
  AD_ACC_LDAP_REALM=${LAB_REALM:-CORP.LOCAL}
  export AD_ACC_LDAP_AUTH AD_ACC_LDAP_USERNAME AD_ACC_LDAP_PASSWORD AD_ACC_LDAP_REALM
  echo "auth: kerberos (password) as $AD_ACC_LDAP_USERNAME@$AD_ACC_LDAP_REALM, no ticket cache"
  ;;
kerberos-keytab)
  unset KRB5CCNAME
  AD_ACC_LDAP_AUTH=kerberos
  AD_ACC_LDAP_KEYTAB=${LAB_KEYTAB:-$HOME/.config/ad-lab/svc.keytab}
  [[ -s $AD_ACC_LDAP_KEYTAB ]] || {
    echo "keytab missing at $AD_ACC_LDAP_KEYTAB; skipping" >&2; exit 0; }
  AD_ACC_LDAP_USERNAME=$(cred svc.username)
  AD_ACC_LDAP_USERNAME="${AD_ACC_LDAP_USERNAME#*\\}"
  AD_ACC_LDAP_REALM=${LAB_REALM:-CORP.LOCAL}
  export AD_ACC_LDAP_AUTH AD_ACC_LDAP_KEYTAB AD_ACC_LDAP_USERNAME AD_ACC_LDAP_REALM
  echo "auth: kerberos (keytab) as $AD_ACC_LDAP_USERNAME@$AD_ACC_LDAP_REALM"
  ;;
kerberos)
  [[ -n ${KRB5CCNAME:-} ]] && klist -s 2>/dev/null || {
    echo "LAB_LDAP_AUTH=kerberos needs a ticket: KRB5CCNAME=FILE:/tmp/krb5cc_tf kinit svc_tfacc@CORP.LOCAL" >&2
    exit 1; }
  echo "auth: kerberos (ticket in $KRB5CCNAME)"
  ;;
*)
  # Unchanged: a ticket if there is one, a simple bind otherwise.
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
  ;;
esac

echo "server=$AD_ACC_LDAP_SERVER tls=$AD_ACC_LDAP_TLS${AD_ACC_LDAP_PORT:+:$AD_ACC_LDAP_PORT} container=$AD_ACC_CONTAINER pattern=$pattern"
exec go test ./internal/provider/ -run "$pattern" -v -count=1 -timeout "${minutes}m"
