# Lab operations: build the Windows AD lab described in LAB.md, ship this
# provider to it, and run the acceptance suite there.
#
# Included by the root GNUmakefile, so every target is reachable as `make lab-*`
# from the repository root. Kept in its own file because none of it is needed to
# build or test the provider -- it exists only to drive two Windows VMs.
#
# No secret is stored here. Credentials are read at call time from
# $(LAB_CREDS), and LAB_ADMIN_PW may instead be supplied in the environment.
# They are passed to the hosts as arguments, so they are visible in the process
# list on the lab machines for the duration of a call; that is a lab, not a
# production posture.

LAB_CREDS   ?= $(HOME)/ad-lab-credentials.txt
LAB_DIR     := scripts/lab
PSRUN       := bash $(LAB_DIR)/psrun.sh
WINRUN      := python3 $(LAB_DIR)/winrun.py

LAB_DC      ?= s-server
LAB_DC2     ?= s-server2
LAB_MEMBER  ?= s-client
LAB_DC_IP   ?= 192.168.50.216
LAB_DC2_IP  ?= 192.168.50.32
LAB_DOMAIN  ?= corp.local

LAB_CONTAINER        ?= OU=tfacc,DC=corp,DC=local
LAB_DENIED_CONTAINER ?= OU=tfacc-denied,DC=corp,DC=local
LAB_E2E_CONTAINER    ?= OU=e2e,DC=corp,DC=local
LAB_PWSH             ?= C:\Program Files\PowerShell\7\pwsh.exe
LAB_PWSH51           ?= C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe

# The psrp path runs the suite here rather than on the member, so the engine is
# chosen by which session configuration it opens: AdObjects51 is a Windows
# PowerShell 5.1 endpoint, AdObjects7 the PowerShell 7 one. Not the built-in
# PowerShell.7 endpoint -- it has no RunAs identity, and a PS7 endpoint refuses a
# non-administrator caller without one, so the delegated account this suite runs
# as gets an opaque pwrshplugin HTTP 500 there. Both lab endpoints grant the
# group CORP\AD-Terraform-Objects.
LAB_PSRP_HOST   ?= 192.168.50.31
LAB_PSRP_SPN    ?= HTTP/s-client.corp.local
LAB_PSRP_HOST2  ?= 192.168.50.33
LAB_PSRP_SPN2   ?= HTTP/s-client2.corp.local
LAB_PSRP_CONFIG ?= AdObjects51
# The PowerShell 7 WinRM endpoint, the winrm+warm+7 matrix cell's engine.
LAB_WINRM_CONFIG7 ?= AdObjects7
LAB_REALM       ?= CORP.LOCAL
LAB_DC_FQDN     ?= s-server.corp.local
LAB_DC2_FQDN    ?= s-server2.corp.local

# run-suite-psrp.sh carries its own identical defaults and reads these six from
# its environment, not from make — a plain `?=` assignment is invisible to a
# child process. Without this export, editing the defaults above does nothing;
# only command-line and environment overrides (`make lab-acc-psrp LAB_PSRP_HOST=...`
# or an exported shell variable) reach the script today.
export LAB_PSRP_HOST LAB_PSRP_SPN LAB_PSRP_HOST2 LAB_PSRP_SPN2 LAB_PSRP_CONFIG LAB_REALM LAB_DC_FQDN LAB_DC2_FQDN

# One awk per lookup, evaluated only when a recipe runs, so no secret is read
# into make's memory for targets that do not need one.
labcred = $$(awk -F'=' '/^$(1)[ \t]*=/{sub(/^[^=]*=[ \t]*/,"");print}' $(LAB_CREDS))

.PHONY: lab-help lab-status lab-ssh-key lab-pwsh lab-rename lab-dns lab-dev-tools \
        lab-promote-dc2 lab-open-ssh lab-acceptance-fixtures lab-grant-deleg lab-verify-repl \
        lab-ship lab-acc lab-acc-repl lab-acc-only lab-acc-psrp lab-acc-psrp-only lab-sweep \
        lab-acc-matrix lab-acc-local-cold lab-acc-local-warm lab-acc-ssh-cold-51 \
        lab-acc-ssh-cold-7 lab-acc-ssh-warm lab-acc-winrm-51 lab-acc-winrm-7 \
        lab-acc-winrm-cold lab-acc-winrm-failover lab-acc-winrm-roundrobin \
        lab-e2e-fixtures lab-e2e lab-e2e-only lab-e2e-sweep

lab-help:
	@echo 'Lab targets. HOST defaults where sensible; override on the command line.'
	@echo ''
	@echo '  Host build-out (see scripts/lab/README.md for the order):'
	@echo '    lab-ssh-key HOST=<ip>      install the SSH key over WinRM (needs LAB_ADMIN_PW)'
	@echo '    lab-pwsh HOST=<alias>      PowerShell 7; run before DNS moves to the DC'
	@echo '    lab-rename HOST=<alias> NAME=<name>'
	@echo '    lab-dns HOST=<alias>       point DNS at the DC, then prove SRV resolves'
	@echo '    lab-dev-tools HOST=<alias> Go and Terraform, for the host that runs the suite'
	@echo '    lab-promote-dc2 HOST=<alias>  promote an additional DC (needs LAB_ADMIN_PW)'
	@echo '    lab-open-ssh HOST=<ip>     re-open SSH after a firewall-profile change'
	@echo '    lab-acceptance-fixtures    containers, service account and delegation'
	@echo '    lab-grant-deleg            grant svc SeEnableDelegationPrivilege (computer delegation; reboot the DC after)'
	@echo ''
	@echo '  Using the lab:'
	@echo '    lab-status                 reachability and role health of all three hosts'
	@echo '    lab-verify-repl            are both DCs replicating?'
	@echo '    lab-ship                   copy this working tree (git archive HEAD) to the member'
	@echo '    lab-acc                    run the whole acceptance suite there'
	@echo '    lab-acc-repl               run only the replication suites'
	@echo '    lab-acc-only PATTERN=<re>  run one suite, or any -run pattern'
	@echo '    lab-acc-psrp               run the suite from here over psrp (LAB_PSRP_CONFIG picks the engine)'
	@echo '    lab-acc-psrp-only PATTERN=<re>  one suite over psrp'
	@echo '    lab-sweep                  delete tfacc- leftovers'
	@echo ''
	@echo '  Transport x mode x pwsh matrix (PATTERN=<re> MINUTES=<n> override; full TestAcc by default):'
	@echo '    lab-acc-matrix             every supported cell in turn, then a pass/fail summary'
	@echo '                               (defaults to a fast lifecycle suite; PATTERN=TestAcc for the full sweep)'
	@echo '    lab-acc-local-cold / -warm      local transport on the member, cold vs warm (pwsh 7)'
	@echo '    lab-acc-ssh-cold-51 / -cold-7   ssh cold over Windows PowerShell 5.1 vs pwsh 7'
	@echo '    lab-acc-ssh-warm                ssh warm (pwsh -sshs subsystem, pwsh 7)'
	@echo '    lab-acc-winrm-51 / -winrm-7     winrm warm over the 5.1 vs pwsh 7 endpoint'
	@echo '    lab-acc-winrm-cold             winrm cold (WinRS stdin; transport=cold.* AD=svc.*)'
	@echo '    lab-e2e-fixtures           e2e OUs and three delegated principals (one-time, admin)'
	@echo '    lab-e2e                    ship, then run the whole e2e suite'
	@echo '    lab-e2e-only PATTERN=<re>  run one e2e suite, or any -run pattern'
	@echo '    lab-e2e-sweep              delete tfacc- leftovers beneath OU=e2e (admin)'

# --- host build-out ---------------------------------------------------------

# Over WinRM, because this is what installs the key that SSH needs.
lab-ssh-key:
	@test -n "$(HOST)" || { echo 'HOST=<ip> required'; exit 1; }
	@test -n "$${LAB_ADMIN_PW}" || { echo 'LAB_ADMIN_PW must be set in the environment'; exit 1; }
	@printf '$$PublicKey = %s\n' "'$$(cat $(HOME)/.ssh/tf_ad_lab.pub)'" > /tmp/lab-ssh-key.ps1
	@tail -n +17 $(LAB_DIR)/01-install-ssh-key.ps1 >> /tmp/lab-ssh-key.ps1
	LAB_USER="$${LAB_USER:-Administrator}" $(WINRUN) $(HOST) /tmp/lab-ssh-key.ps1
	@rm -f /tmp/lab-ssh-key.ps1

lab-pwsh:
	$(PSRUN) $(or $(HOST),$(LAB_MEMBER)) $(LAB_DIR)/02-install-pwsh.ps1 900

lab-rename:
	@test -n "$(NAME)" || { echo 'NAME=<computername> required'; exit 1; }
	$(PSRUN) $(or $(HOST),$(LAB_MEMBER)) $(LAB_DIR)/05-rename.ps1 120 -- -NewName $(NAME)

lab-dns:
	$(PSRUN) $(or $(HOST),$(LAB_DC2)) $(LAB_DIR)/11-point-dns-at-dc.ps1 160 -- -DcAddress $(LAB_DC_IP)

lab-dev-tools:
	$(PSRUN) $(or $(HOST),$(LAB_MEMBER)) $(LAB_DIR)/10-install-dev-tools.ps1 1500

# Detached and rebooting; poll C:\Windows\Temp\labsetup.log, then run lab-open-ssh.
lab-promote-dc2:
	@test -n "$${LAB_ADMIN_PW}" || { echo 'LAB_ADMIN_PW must be set in the environment'; exit 1; }
	$(PSRUN) $(or $(HOST),$(LAB_DC2)) $(LAB_DIR)/12-promote-second-dc.ps1 360 -- \
	  -DsrmPassword "$(call labcred,dsrm.password)" \
	  -AdminUser 'CORP\Administrator' -AdminPassword "$${LAB_ADMIN_PW}"

# Over WinRM on purpose: promotion and domain join both close SSH.
lab-open-ssh:
	@test -n "$(HOST)" || { echo 'HOST=<ip> required'; exit 1; }
	@test -n "$${LAB_ADMIN_PW}" || { echo 'LAB_ADMIN_PW must be set in the environment'; exit 1; }
	LAB_USER="$${LAB_USER:-CORP\\Administrator}" $(WINRUN) $(HOST) $(LAB_DIR)/09-open-ssh-firewall.ps1

lab-acceptance-fixtures:
	$(PSRUN) $(LAB_DC) $(LAB_DIR)/08-provision-acceptance.ps1 300 -- \
	  -SvcPassword "$(call labcred,svc.password)"

# Grant the svc account SeEnableDelegationPrivilege so the computer suite can set
# trusted_for_delegation / allowed_to_delegate_to. One-time; the DC must be
# rebooted afterwards for the privilege to take effect (see the script header).
lab-grant-deleg:
	$(PSRUN) $(LAB_DC) $(LAB_DIR)/grant-svc-deleg-priv.ps1 200

# --- using the lab ----------------------------------------------------------

lab-status:
	@for h in $(LAB_DC_IP) $(LAB_DC2_IP) 192.168.50.31; do \
	  printf '%-16s ' $$h; \
	  for p in 22 389 9389; do \
	    if timeout 3 bash -c "cat </dev/null >/dev/tcp/$$h/$$p" 2>/dev/null; \
	      then printf '%s:open ' $$p; else printf '%s:--   ' $$p; fi; \
	  done; echo; \
	done
	@echo '(389/9389 are expected only on the domain controllers)'

# Asks each DC about itself rather than one DC about both. repadmin reaching the
# other DC goes over RPC as the SSH session's own token, which on a DC carries
# nothing delegatable -- so a cross-DC query fails with error 110 while
# replication is perfectly healthy. Run locally, each answer is authoritative.
lab-verify-repl:
	@printf '%s\n' \
	  '$$r = repadmin /showrepl 2>&1 | Out-String' \
	  'Write-Output ("HOST " + $$env:COMPUTERNAME)' \
	  '$$ok = ([regex]::Matches($$r, "was successful")).Count' \
	  '$$bad = ([regex]::Matches($$r, "failed|error")).Count' \
	  'Write-Output ("  inbound successes=$$ok  failure_mentions=$$bad")' \
	  '(repadmin /showrepl 2>&1 | Select-String "Last attempt" | Select-Object -Last 4) | ForEach-Object { "  " + $$_.ToString().Trim() }' \
	  > /tmp/lab-showrepl.ps1
	@for h in $(LAB_DC) $(LAB_DC2); do \
	  $(PSRUN) $$h /tmp/lab-showrepl.ps1 200 2>&1 | grep -vE 'WARNING|vulnerable|openssh.com|^\s*$$'; \
	done
	@rm -f /tmp/lab-showrepl.ps1

# git archive rather than the working tree: what runs on the lab is exactly what
# is committed, and no gitignored clone or build artefact rides along.
lab-ship:
	git archive --format=tar --prefix=provider/ HEAD | gzip -9 > /tmp/provider-src.tgz
	scp -q /tmp/provider-src.tgz $(LAB_MEMBER):provider-src.tgz
	@printf '%s\n' \
	  '$$ErrorActionPreference = "Stop"' \
	  'New-Item -ItemType Directory -Force -Path C:\src | Out-Null' \
	  'if (Test-Path C:\src\provider) { Remove-Item C:\src\provider -Recurse -Force }' \
	  'tar -xzf "$$env:USERPROFILE\provider-src.tgz" -C C:\src' \
	  'Write-Output ("files=" + (Get-ChildItem -Recurse -File C:\src\provider).Count)' \
	  > /tmp/lab-unpack.ps1
	@$(PSRUN) $(LAB_MEMBER) /tmp/lab-unpack.ps1 160 2>&1 | grep -vE 'WARNING|vulnerable|openssh.com'
	@rm -f /tmp/lab-unpack.ps1 /tmp/provider-src.tgz

# The suite runner lives in run-suite.sh: generating PowerShell through make's
# quoting rules costs more than it saves, and that script is what a person reads
# when a run misbehaves.
lab-acc:
	$(LAB_DIR)/run-suite.sh TestAcc 90

lab-acc-repl:
	$(LAB_DIR)/run-suite.sh TestAccReplication 40

# Any subset, e.g. make lab-acc-only PATTERN=TestAccOULifecycle
# PATTERN must not contain a '|' alternation: it reaches go test -run through a
# cmd /c "..." on the member, where cmd.exe reads an unquoted '|' as a pipe. Use
# a common prefix (Go -run is a regex) or run the whole suite with lab-acc.
lab-acc-only:
	@test -n "$(PATTERN)" || { echo 'PATTERN=<go test -run pattern> required'; exit 1; }
	$(LAB_DIR)/run-suite.sh '$(PATTERN)' $(or $(MINUTES),40)

# Runs here, not on the member: no lab-ship, so the working tree is what runs.
lab-acc-psrp:
	$(LAB_DIR)/run-suite-psrp.sh TestAcc 90

lab-acc-psrp-only:
	@test -n "$(PATTERN)" || { echo 'PATTERN=<go test -run pattern> required'; exit 1; }
	$(LAB_DIR)/run-suite-psrp.sh '$(PATTERN)' $(or $(MINUTES),40)

# --- transport x mode x powershell-version matrix ---------------------------
#
# One target per supported cell of the two-axis design (transport x mode), with
# the PowerShell version split where it is real: ssh+cold runs on 5.1 or 7, and
# winrm+warm picks its engine by session configuration (AdObjects51 vs
# AdObjects7). winrm+cold is intentionally absent — the provider refuses it,
# because an AD operation's encoded preamble exceeds the WinRS command-line
# limit (lab-confirmed 2026-08-27; see LAB.md).
#
# | cell                | transport | mode | pwsh | runner            |
# |---------------------|-----------|------|------|-------------------|
# | lab-acc-local-cold  | local     | cold | 7    | on member (ship)  |
# | lab-acc-local-warm  | local     | warm | 7    | on member (ship)  |
# | lab-acc-ssh-cold-51 | ssh       | cold | 5.1  | from here         |
# | lab-acc-ssh-cold-7  | ssh       | cold | 7    | from here         |
# | lab-acc-ssh-warm    | ssh       | warm | 7    | from here         |
# | lab-acc-winrm-51    | winrm     | warm | 5.1  | from here         |
# | lab-acc-winrm-7     | winrm     | warm | 7    | from here         |
#
# Each cell defaults to the full TestAcc suite; override with PATTERN=<re> and
# MINUTES=<n>. Local cells ship the committed tree to the member and run there
# (like lab-acc). ssh/winrm cells run the working tree from here against the
# released go-adpwsh (GOWORK=off), so a code change is exercised without a
# commit. warm needs pwsh 7 on the target (for ssh, the `powershell` sshd
# subsystem). A '|' alternation in PATTERN is safe for ssh/winrm (run here) but
# not for the local cells (they cross cmd.exe on the member) — use a prefix.
MATRIX_CELLS := lab-acc-local-cold lab-acc-local-warm \
                lab-acc-ssh-cold-51 lab-acc-ssh-cold-7 lab-acc-ssh-warm \
                lab-acc-winrm-51 lab-acc-winrm-7 lab-acc-winrm-cold

lab-acc-local-cold: lab-ship
	LAB_MODE=cold $(LAB_DIR)/run-suite.sh $(or $(PATTERN),TestAcc) $(or $(MINUTES),90)

lab-acc-local-warm: lab-ship
	LAB_MODE=warm $(LAB_DIR)/run-suite.sh $(or $(PATTERN),TestAcc) $(or $(MINUTES),90)

lab-acc-ssh-cold-51:
	GOWORK=off LAB_MODE=cold LAB_PWSH='$(LAB_PWSH51)' \
	  $(LAB_DIR)/run-suite-ssh.sh $(or $(PATTERN),TestAcc) $(or $(MINUTES),90)

lab-acc-ssh-cold-7:
	GOWORK=off LAB_MODE=cold LAB_PWSH='$(LAB_PWSH)' \
	  $(LAB_DIR)/run-suite-ssh.sh $(or $(PATTERN),TestAcc) $(or $(MINUTES),90)

lab-acc-ssh-warm:
	GOWORK=off LAB_MODE=warm \
	  $(LAB_DIR)/run-suite-ssh.sh $(or $(PATTERN),TestAcc) $(or $(MINUTES),90)

lab-acc-winrm-51:
	GOWORK=off LAB_MODE=warm LAB_PSRP_CONFIG=$(LAB_PSRP_CONFIG) \
	  $(LAB_DIR)/run-suite-psrp.sh $(or $(PATTERN),TestAcc) $(or $(MINUTES),90)

lab-acc-winrm-7:
	GOWORK=off LAB_MODE=warm LAB_PSRP_CONFIG=$(LAB_WINRM_CONFIG7) \
	  $(LAB_DIR)/run-suite-psrp.sh $(or $(PATTERN),TestAcc) $(or $(MINUTES),90)

# winrm + cold: a fresh Windows Remote Shell per op, script on stdin to
# `powershell -EncodedCommand` (no PSRP session configuration). The transport
# account (cred file cold.*) is WinRS-only; the AD identity (svc.*) rides
# domain.credential. run-suite-winrm-cold.sh carries the kinit + env.
lab-acc-winrm-cold:
	GOWORK=off \
	  $(LAB_DIR)/run-suite-winrm-cold.sh $(or $(PATTERN),TestAcc) $(or $(MINUTES),90)

# Two winrm servers (s-client + s-client2); the provider fails over between them.
lab-acc-winrm-failover:
	GOWORK=off LAB_MODE=warm LAB_PSRP_CONFIG=$(LAB_PSRP_CONFIG) \
	  LAB_PSRP_HOST2=$(LAB_PSRP_HOST2) LAB_PSRP_SPN2=$(LAB_PSRP_SPN2) \
	  $(LAB_DIR)/run-suite-psrp.sh $(or $(PATTERN),TestAccOULifecycle) $(or $(MINUTES),40)

# Two winrm servers (s-client + s-client2) with round-robin selection: the
# provider rotates connections across them (winrm.server_selection = "round_robin")
# instead of always preferring the first, still failing through when one is down.
lab-acc-winrm-roundrobin:
	GOWORK=off LAB_MODE=warm LAB_PSRP_CONFIG=$(LAB_PSRP_CONFIG) \
	  LAB_PSRP_HOST2=$(LAB_PSRP_HOST2) LAB_PSRP_SPN2=$(LAB_PSRP_SPN2) \
	  LAB_PSRP_SERVER_SELECTION=round_robin \
	  $(LAB_DIR)/run-suite-psrp.sh $(or $(PATTERN),TestAccOULifecycle) $(or $(MINUTES),40)

# Run every matrix cell in turn, continuing past a failure and printing a
# pass/fail summary at the end (exit non-zero if any cell failed). The single
# command defaults to a fast representative lifecycle suite so the whole sweep is
# practical; PATTERN=TestAcc MINUTES=90 runs the full suite in each cell.
lab-acc-matrix:
	@fail=0; results=''; \
	for t in $(MATRIX_CELLS); do \
	  echo; echo "=== matrix cell: $$t ==="; \
	  if $(MAKE) --no-print-directory $$t PATTERN='$(or $(PATTERN),TestAccOULifecycle)' MINUTES='$(or $(MINUTES),40)'; then \
	    results="$$results\nPASS  $$t"; \
	  else \
	    results="$$results\nFAIL  $$t"; fail=1; \
	  fi; \
	done; \
	echo; echo '=== matrix summary ==='; printf '%b\n' "$$results"; \
	exit $$fail

lab-sweep:
	$(LAB_DIR)/run-suite.sh --sweep 30

# --- e2e layer --------------------------------------------------------------

# One-time: create the three delegated principals and their OUs (needs admin).
lab-e2e-fixtures:
	$(PSRUN) $(LAB_DC) $(LAB_DIR)/13-provision-e2e.ps1 300 -- \
	  -SvcPassword "$(call labcred,e2e.password)"

# Ship this working tree, then run the whole e2e suite there. Needs no admin.
lab-e2e: lab-ship
	$(LAB_DIR)/run-e2e.sh TestAccE2E 90

# Any e2e subset, e.g. make lab-e2e-only PATTERN=TestAccE2EDrift
# PATTERN must not contain a '|' alternation: it reaches go test -run through a
# cmd /c "..." on the member, where cmd.exe reads an unquoted '|' as a pipe. Use
# a common prefix (Go -run is a regex) or run the whole suite with lab-e2e.
lab-e2e-only: lab-ship
	@test -n "$(PATTERN)" || { echo 'PATTERN=<go test -run pattern> required'; exit 1; }
	$(LAB_DIR)/run-e2e.sh '$(PATTERN)' $(or $(MINUTES),40)

# Delete tfacc- leftovers beneath OU=e2e (needs admin.password in $(LAB_CREDS)).
lab-e2e-sweep:
	$(LAB_DIR)/run-e2e.sh --sweep 30
