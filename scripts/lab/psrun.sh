#!/usr/bin/env bash
# Run a PowerShell script on a lab host over SSH.
#
# Uses -EncodedCommand (UTF-16LE base64), which sidesteps every quoting trap in
# the ssh -> cmd.exe -> powershell chain. This is the same mechanism the provider
# itself uses, so what works here works there.
#
#   usage: psrun.sh <ssh-alias> <script.ps1> [timeout-seconds] [-- <arg>...]
#
# Anything after `--` is appended to the script invocation as PowerShell
# arguments, e.g.  psrun.sh s-server 04-promote-dc.ps1 900 -- -DsrmPassword 'x'
set -euo pipefail

host=${1:?ssh alias required}
script=${2:?script path required}
tmo=${3:-120}
shift 3 || shift $#
[[ "${1:-}" == "--" ]] && shift

body=$(cat "$script")
if [[ $# -gt 0 ]]; then
    # Wrap the script in a scriptblock so parameters can be passed positionally.
    body="& { $body } $*"
fi

enc=$(printf '%s' "$body" | iconv -f UTF-8 -t UTF-16LE | base64 -w0)
timeout "$tmo" ssh -o BatchMode=yes -o ConnectTimeout=10 "$host" \
    "powershell -NoProfile -NonInteractive -EncodedCommand $enc" 2>&1 |
    grep -viE 'warning: connection|post-quantum|store now|may need to be upgraded|^#< CLIXML' |
    grep -v '^<Objs' | sed '/^[[:space:]]*$/d'
