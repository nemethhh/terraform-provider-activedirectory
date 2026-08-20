#!/usr/bin/env python3
"""Run PowerShell on a lab host over WinRM/NTLM.

The fallback for when SSH is unavailable -- which happens every time a host
changes firewall profile (DC promotion, domain join), because the OpenSSH rule
is scoped to Private while WinRM's is not.

    LAB_USER='CORP\\Administrator' LAB_ADMIN_PW='...' \
        python3 winrun.py 192.168.50.31 09-open-ssh-firewall.ps1
"""
import os
import sys

import winrm

host, script = sys.argv[1], sys.argv[2]
session = winrm.Session(
    f'http://{host}:5985/wsman',
    auth=(os.environ['LAB_USER'], os.environ['LAB_ADMIN_PW']),
    transport='ntlm',
    server_cert_validation='ignore',
)
result = session.run_ps(open(script).read())
out = result.std_out.decode(errors='replace').strip()
if out:
    print(out)
err = result.std_err.decode(errors='replace')
if result.status_code != 0 and 'CLIXML' not in err[:20]:
    print('STDERR:', err[:600])
sys.exit(result.status_code)
