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

# One awk per lookup, evaluated only when a recipe runs, so no secret is read
# into make's memory for targets that do not need one.
labcred = $$(awk -F'=' '/^$(1)[ \t]*=/{sub(/^[^=]*=[ \t]*/,"");print}' $(LAB_CREDS))

.PHONY: lab-help lab-status lab-ssh-key lab-pwsh lab-rename lab-dns lab-dev-tools \
        lab-promote-dc2 lab-open-ssh lab-acceptance-fixtures lab-verify-repl \
        lab-ship lab-acc lab-acc-repl lab-acc-only lab-sweep \
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
	@echo ''
	@echo '  Using the lab:'
	@echo '    lab-status                 reachability and role health of all three hosts'
	@echo '    lab-verify-repl            are both DCs replicating?'
	@echo '    lab-ship                   copy this working tree (git archive HEAD) to the member'
	@echo '    lab-acc                    run the whole acceptance suite there'
	@echo '    lab-acc-repl               run only the replication suites'
	@echo '    lab-acc-only PATTERN=<re>  run one suite, or any -run pattern'
	@echo '    lab-sweep                  delete tfacc- leftovers'
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
lab-acc-only:
	@test -n "$(PATTERN)" || { echo 'PATTERN=<go test -run pattern> required'; exit 1; }
	$(LAB_DIR)/run-suite.sh '$(PATTERN)' $(or $(MINUTES),40)

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
lab-e2e-only: lab-ship
	@test -n "$(PATTERN)" || { echo 'PATTERN=<go test -run pattern> required'; exit 1; }
	$(LAB_DIR)/run-e2e.sh '$(PATTERN)' $(or $(MINUTES),40)

# Delete tfacc- leftovers beneath OU=e2e (needs admin.password in $(LAB_CREDS)).
lab-e2e-sweep:
	$(LAB_DIR)/run-e2e.sh --sweep 30
