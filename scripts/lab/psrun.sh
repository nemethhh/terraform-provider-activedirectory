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
    # Quote every argument for PowerShell. Passing them raw loses the shell's
    # quoting, and a value containing '#' would then start a PowerShell comment
    # and silently truncate -- which is exactly how a password ending in '#'
    # turns into an inscrutable "user name or password is incorrect".
    args=""
    for a in "$@"; do
        if [[ $a == -* ]]; then
            args+=" $a"                      # a parameter name, pass through
        else
            args+=" '${a//\'/\'\'}'"          # a value: single-quote, doubling any quote
        fi
    done
    body="& { $body }$args"
fi

enc=$(printf '%s' "$body" | iconv -f UTF-8 -t UTF-16LE | base64 -w0)

# Windows caps a command line at 8191 characters, and base64-of-UTF-16 is about
# 2.7x the source. Past that, ship the script over scp and run it by path
# instead -- otherwise cmd.exe answers "The command line is too long".
if [[ ${#enc} -lt 7000 ]]; then
    run="powershell -NoProfile -NonInteractive -EncodedCommand $enc"
else
    base="psrun_$(basename "$script")"
    tmp=$(mktemp)
    printf '%s' "$body" > "$tmp"
    scp -q -o BatchMode=yes "$tmp" "$host:C:/Windows/Temp/$base"
    rm -f "$tmp"
    run="powershell -NoProfile -NonInteractive -ExecutionPolicy Bypass -File C:\\Windows\\Temp\\$base"
fi

timeout "$tmo" ssh -o BatchMode=yes -o ConnectTimeout=10 "$host" "$run" 2>&1 |
    grep -viE 'warning: connection|post-quantum|store now|may need to be upgraded|^#< CLIXML' |
    grep -v '^<Objs' | sed '/^[[:space:]]*$/d'
