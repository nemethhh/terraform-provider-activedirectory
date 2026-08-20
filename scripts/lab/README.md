# Lab provisioning scripts

Build the two-host Windows lab described in [`../../LAB.md`](../../LAB.md) from a
fresh Windows Server install to a domain that the acceptance suite can run
against.

**No credentials are stored here.** Every script that needs one takes it as a
parameter. Keep the lab's secrets outside the repository — the working set lives
in `~/ad-lab-credentials.txt` (mode 0600).

## How they run

`psrun.sh` sends a script to a host over SSH as a UTF-16LE `-EncodedCommand`,
which sidesteps every quoting trap in the `ssh` → `cmd.exe` → `powershell` chain.
It is the same mechanism the provider itself uses.

```bash
./psrun.sh <ssh-alias> <script.ps1> [timeout-seconds] [-- <powershell-args>...]
```

Two conventions worth knowing before editing these:

- **Anything slow or reboot-inducing runs detached**, as a SYSTEM scheduled task
  logging to a file, and is polled afterwards. `Install-WindowsFeature AD-Domain-Services`
  alone outlives a comfortable SSH command, and promotion reboots the host, so a
  held session would die mid-operation and tell you nothing about the outcome.
- **Anything that could strand the host self-heals.** There is no console access
  to these machines, so the static-IP change arms a rollback to DHCP that is only
  cancelled once the host is confirmed reachable.

## Order

| # | Script | Host | Notes |
|---|---|---|---|
| 1 | `01-install-ssh-key.ps1` | both | Needs the Administrator password. Writes `administrators_authorized_keys`, not `~/.ssh/authorized_keys` — `ssh-copy-id` does not work for admins. |
| 2 | `02-install-pwsh.ps1` | both | Run before DNS moves to the DC. |
| 3 | `03-set-static-ip.ps1` | both | Arms a DHCP rollback. |
| 4 | `04-confirm-static.ps1` | both | **Do not skip** — cancels the rollback. Verify reachability first. |
| 5 | `05-rename.ps1` | both | Reboots. Must precede promotion. |
| 6 | `06-promote-dc.ps1` | DC | Detached; poll `C:\Windows\Temp\labsetup.log`. Long reboot. |
| 7 | `07-join-domain.ps1` | member | Repoints DNS, installs RSAT, joins, reboots. |
| 8 | `08-provision-acceptance.ps1` | DC | Containers, `svc_tfacc`, delegation. |

Steps 1–5 are safe to run against both hosts in parallel; 6–8 are ordered.

## Verifying

The provider needs **ADWS on TCP 9389**, not merely SSH. A DC that boots without
it is a silent failure, so check the port rather than assuming:

```bash
timeout 5 bash -c '</dev/tcp/192.168.50.216/9389' && echo ADWS_OK
```

Then export what the suite reads (see the acceptance section of `../../README.md`)
and run it:

```bash
export TF_ACC=1 \
       AD_ACC_CONTAINER='OU=tfacc,DC=corp,DC=local' \
       AD_ACC_DENIED_CONTAINER='OU=tfacc-denied,DC=corp,DC=local' \
       AD_ACC_SERVER='s-server.corp.local'
make testacc
```

`AD_ACC_SECOND_DC` has no host in this lab, so the replication suite cannot run
until a second DC exists.

## Re-running

All eight are idempotent: they detect the already-done state and say so rather
than failing. After a crashed acceptance run, `make sweep` clears leftover
`tfacc-` objects; the containers and `svc_tfacc` are fixtures and survive.
