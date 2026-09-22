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

# winrun.py needs pywinrm, which a PEP 668 distribution (Arch, Fedora, Debian
# 12+) refuses to install into the system interpreter. Prefer a venv if one is
# there, fall back to python3 otherwise, so neither kind of host needs a special
# case:
#
#   python3 -m venv ~/.venvs/ad-lab && ~/.venvs/ad-lab/bin/pip install pywinrm
LAB_VENV_PY := $(wildcard $(HOME)/.venvs/ad-lab/bin/python)
WINRUN      := $(or $(LAB_VENV_PY),python3) $(LAB_DIR)/winrun.py

# Rebuilt 2026-09-21 on new addresses. The whole lab moved: s-server/.216 and
# s-client/.31 are gone, and .32 — which used to be the second DC — is now the
# second member. Anything still naming an old address is stale, not a variant.
LAB_DC      ?= s-server1
LAB_DC2     ?= s-server2
LAB_MEMBER  ?= s-client1
LAB_MEMBER2 ?= s-client2
LAB_DC_IP   ?= 192.168.50.21
LAB_DC2_IP  ?= 192.168.50.22
LAB_MEMBER_IP  ?= 192.168.50.31
LAB_MEMBER2_IP ?= 192.168.50.32
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
# Derived from the topology above rather than repeated: LAB_PSRP_HOST2 was left
# at 192.168.50.33 after the rebuild, where nothing answers, so every failover
# run silently exercised one host twice.
LAB_PSRP_HOST   ?= $(LAB_MEMBER_IP)
LAB_PSRP_SPN    ?= HTTP/$(LAB_MEMBER).$(LAB_DOMAIN)
LAB_PSRP_HOST2  ?= $(LAB_MEMBER2_IP)
LAB_PSRP_SPN2   ?= HTTP/$(LAB_MEMBER2).$(LAB_DOMAIN)
LAB_PSRP_CONFIG ?= AdObjects51
# The PowerShell 7 WinRM endpoint, the winrm+warm+7 matrix cell's engine.
LAB_WINRM_CONFIG7 ?= AdObjects7
LAB_REALM       ?= CORP.LOCAL
LAB_DC_FQDN     ?= s-server1.$(LAB_DOMAIN)
LAB_DC2_FQDN    ?= s-server2.$(LAB_DOMAIN)

# The native-LDAP connection. It needs no jump box and no PowerShell: the suite
# runs wherever Terraform does and speaks LDAPS to the DC directly.
LAB_LDAP_SERVER ?= $(LAB_DC_FQDN)
# Where the lab CA certificate is cached locally, for ca_certificate_file.
LAB_CA_FILE     ?= $(HOME)/.config/ad-lab/corp-lab-ca.pem

# run-suite-psrp.sh carries its own identical defaults and reads these six from
# its environment, not from make — a plain `?=` assignment is invisible to a
# child process. Without this export, editing the defaults above does nothing;
# only command-line and environment overrides (`make lab-acc-psrp LAB_PSRP_HOST=...`
# or an exported shell variable) reach the script today.
export LAB_PSRP_HOST LAB_PSRP_SPN LAB_PSRP_HOST2 LAB_PSRP_SPN2 LAB_PSRP_CONFIG LAB_REALM LAB_DC_FQDN LAB_DC2_FQDN
# LAB_MEMBER and LAB_DC_IP were missing from this list, so run-suite.sh and
# run-suite-psrp.sh fell through to their own defaults — which is why editing
# the topology above had no effect on them.
export LAB_MEMBER LAB_MEMBER2 LAB_DC LAB_DC2 LAB_DC_IP LAB_DC2_IP LAB_DOMAIN LAB_LDAP_SERVER LAB_CA_FILE

# One awk per lookup, evaluated only when a recipe runs, so no secret is read
# into make's memory for targets that do not need one.
labcred = $$(awk -F'=' '/^$(1)[ \t]*=/{sub(/^[^=]*=[ \t]*/,"");print}' $(LAB_CREDS))

.PHONY: lab-help lab-status lab-ssh-key lab-pwsh lab-rename lab-dns lab-dev-tools \
        lab-promote-dc2 lab-open-ssh lab-acceptance-fixtures lab-grant-deleg lab-verify-repl \
        lab-adcs lab-ca-cert lab-acc-ldap lab-dev-workspace lab-dev-workspace-off \
        lab-ship lab-acc lab-acc-repl lab-acc-only lab-acc-psrp lab-acc-psrp-only lab-sweep \
        lab-acc-matrix lab-acc-local-cold lab-acc-local-warm lab-acc-ssh-cold-51 \
        lab-acc-ssh-cold-7 lab-acc-ssh-warm lab-acc-winrm-51 lab-acc-winrm-7 \
        lab-acc-winrm-cold lab-acc-winrm-failover lab-acc-winrm-roundrobin \
        lab-channel-binding lab-acc-ldap-krb-matrix \
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
	@echo '    lab-acc-ldap               run the suite over LDAPS, no PowerShell (PATTERN=<re>)'
	@echo '    lab-channel-binding VALUE=<0|1|2>  set LdapEnforceChannelBinding on a DC and restart NTDS'
	@echo '    lab-acc-ldap-krb-matrix    every Kerberos credential source x every channel-binding policy'
	@echo '    lab-adcs                   install the Enterprise CA that LDAPS needs (once)'
	@echo '    lab-ca-cert                cache the CA locally for ca_certificate_file'
	@echo '    lab-dev-workspace          build the runners that run from here against the sibling'
	@echo '                               working trees instead of the versions go.mod pins'
	@echo '    lab-dev-workspace-off      back to the pinned releases'
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

# A fresh AD DS install has no certificate, so LDAPS resets every handshake and
# the provider's ldap connection cannot be used at all. Run once on the first DC;
# the second auto-enrols from the same CA.
lab-adcs:
	$(PSRUN) $(or $(HOST),$(LAB_DC)) $(LAB_DIR)/14-install-adcs.ps1 900

# Fetch the CA certificate so a client can verify LDAPS instead of skipping it.
lab-ca-cert:
	@mkdir -p $(dir $(LAB_CA_FILE))
	@$(PSRUN) $(or $(HOST),$(LAB_DC)) $(LAB_DIR)/print-ca-cert.ps1 120 2>/dev/null | sed -n '/BEGIN CERTIFICATE/,/END CERTIFICATE/p' | tr -d '\r' > $(LAB_CA_FILE)
	@test -s $(LAB_CA_FILE) && openssl x509 -in $(LAB_CA_FILE) -noout -subject && echo "wrote $(LAB_CA_FILE)" || { echo 'no certificate returned'; exit 1; }

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
	@printf '%-10s %-16s %s\n' HOST ADDRESS PORTS
	@for pair in "$(LAB_DC):$(LAB_DC_IP)" "$(LAB_DC2):$(LAB_DC2_IP)" \
	             "$(LAB_MEMBER):$(LAB_MEMBER_IP)" "$(LAB_MEMBER2):$(LAB_MEMBER2_IP)"; do \
	  n=$${pair%%:*}; h=$${pair##*:}; \
	  printf '%-10s %-16s ' $$n $$h; \
	  for p in 22 5985 389 636 9389; do \
	    if timeout 3 bash -c "cat </dev/null >/dev/tcp/$$h/$$p" 2>/dev/null; \
	      then printf '%s:open ' $$p; else printf '%s:--   ' $$p; fi; \
	  done; echo; \
	done
	@echo '(389/636/9389 are expected only on the domain controllers;'
	@echo ' 636 is what the provider ldap connection needs, 9389 what the PowerShell ones need)'

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
#
# The three sibling modules travel with the provider, and a go.work generated
# beside them makes `go test` on the member build against those shipped sources
# instead of the released versions go.mod pins. That is what the local cells
# are for: a library change is exercised against a real domain once it is
# committed, without tagging and pushing a release first.
#
# The workspace exists only on the member and is never committed here, so the
# provider's go.mod keeps pinning released versions for everyone else. The
# ssh/winrm cells deliberately do the opposite -- they run this working tree
# against the released libraries -- which is why they pass GOWORK=off.
#
# Until 2026-09-22 this worked through `replace` directives in the provider's
# go.mod pointing at ../go-adcore and friends, with the layout under C:\src
# mirroring this working tree so the ../ paths resolved. Those replaces are
# gone (a clean clone of the provider alone could not build), and the workspace
# file is what preserves the property they provided.
LAB_SIBLING_MODULES ?= go-adcore go-adldap go-adpwsh

# The workspace's go directive has to be at least the highest of the modules'.
# Read it from the provider's go.mod rather than hardcoding a version that goes
# stale on the next toolchain bump.
LAB_GO_VERSION := $(shell awk '/^go /{print $$2; exit}' go.mod)

# A workspace for developing the sibling libraries against the lab.
#
# run-suite-ldap.sh and the ssh/winrm runners execute `go test` from here, so
# they build whatever this working tree resolves -- which, now that go.mod pins
# released versions rather than replacing them with ../ paths, is the released
# libraries. That is right for a release check and wrong for the loop this
# repository is usually in: change go-adldap, run it against corp.local, decide
# whether it works, and only then tag it.
#
# The file is written one directory up, outside this git repository, so it
# cannot be committed by accident and no .gitignore entry is needed. lab-ship
# generates its own workspace on the member and is unaffected either way, and
# the ssh/winrm cells pass GOWORK=off so they stay on the released libraries
# whether this is on or not -- that contrast is the point of those cells.
LAB_DEV_WORKSPACE := ../go.work

lab-dev-workspace:
	@test -n '$(LAB_GO_VERSION)' || { echo "could not read the go directive from go.mod"; exit 1; }
	@for m in $(LAB_SIBLING_MODULES); do \
	    test -d ../$$m || { echo "sibling module ../$$m is missing"; exit 1; }; \
	  done
	@{ printf 'go %s\n\nuse (\n\t./%s\n' '$(LAB_GO_VERSION)' '$(notdir $(CURDIR))'; \
	   for m in $(LAB_SIBLING_MODULES); do printf '\t./%s\n' $$m; done; \
	   printf ')\n'; \
	 } > $(LAB_DEV_WORKSPACE)
	@echo "workspace on: $(LAB_DEV_WORKSPACE)"
	@go list -m -f '  {{.Path}} => {{.Dir}}' $(foreach m,$(LAB_SIBLING_MODULES),github.com/nemethhh/$(m))
	@echo "  make lab-dev-workspace-off to go back to the versions go.mod pins"

lab-dev-workspace-off:
	@rm -f $(LAB_DEV_WORKSPACE) $(LAB_DEV_WORKSPACE).sum
	@echo "workspace off: building against the versions go.mod pins"

lab-ship:
	@test -n '$(LAB_GO_VERSION)' || { echo "could not read the go directive from go.mod"; exit 1; }
	@rm -f /tmp/lab-ship-*.tgz /tmp/lab-ship-go.work
	git archive --format=tar --prefix=provider/ HEAD | gzip -9 > /tmp/lab-ship-provider.tgz
	@for m in $(LAB_SIBLING_MODULES); do \
	    test -d ../$$m || { echo "sibling module ../$$m is missing"; exit 1; }; \
	    git -C ../$$m archive --format=tar --prefix=$$m/ HEAD | gzip -9 > /tmp/lab-ship-$$m.tgz; \
	  done
	@{ printf 'go %s\n\nuse (\n\t./provider\n' '$(LAB_GO_VERSION)'; \
	   for m in $(LAB_SIBLING_MODULES); do printf '\t./%s\n' $$m; done; \
	   printf ')\n'; \
	 } > /tmp/lab-ship-go.work
	scp -q /tmp/lab-ship-*.tgz /tmp/lab-ship-go.work $(LAB_MEMBER):
	@{ printf '%s\n' \
	  '$$ErrorActionPreference = "Stop"' \
	  'New-Item -ItemType Directory -Force -Path C:\src | Out-Null'; \
	  for m in provider $(LAB_SIBLING_MODULES); do \
	    printf 'if (Test-Path C:\src\%s) { Remove-Item C:\src\%s -Recurse -Force }\n' $$m $$m; \
	    printf 'tar -xzf "$$env:USERPROFILE\lab-ship-%s.tgz" -C C:\src\n' $$m; \
	  done; \
	  printf '%s\n' \
	    'Move-Item -Force "$$env:USERPROFILE\lab-ship-go.work" C:\src\go.work' \
	    'Remove-Item C:\src\go.work.sum -Force -ErrorAction SilentlyContinue' \
	    'Write-Output ("files=" + (Get-ChildItem -Recurse -File C:\src\provider).Count)' \
	    'Write-Output ("workspace=" + (((Get-Content C:\src\go.work) -replace "\s+"," ") -join " "))'; \
	} > /tmp/lab-unpack.ps1
	@$(PSRUN) $(LAB_MEMBER) /tmp/lab-unpack.ps1 160 2>&1 | grep -vE 'WARNING|vulnerable|openssh.com'
	@rm -f /tmp/lab-unpack.ps1 /tmp/lab-ship-*.tgz /tmp/lab-ship-go.work


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
# MINUTES=<n>. Local cells ship the committed tree -- provider and all three
# sibling libraries -- to the member and run there (like lab-ship + lab-acc),
# building against those committed library sources through the workspace
# lab-ship generates. ssh/winrm cells run the working tree from here against
# the released libraries go.mod pins (GOWORK=off), so a provider change is
# exercised without a commit. The pair is deliberate: local cells answer "does
# the committed library work on a real domain", ssh/winrm cells answer "does
# this uncommitted provider change work against what users actually have".
# warm needs pwsh 7 on the target (for ssh, the `powershell` sshd subsystem). A '|' alternation in PATTERN is safe for ssh/winrm (run here) but
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

# The whole acceptance suite over the native LDAP connection. Runs here, not on
# a member: the ldap connection needs no PowerShell and no jump box.
lab-acc-ldap:
	$(LAB_DIR)/run-suite-ldap.sh $(or $(PATTERN),TestAcc) $(or $(MINUTES),60)

# Set the channel-binding policy on a DC. 0 never, 1 when supported, 2 always.
# The lab ships at 0; 2 is what the CIS Benchmark and the DISA STIG require and
# what the Kerberos bind's channel-binding token exists for.
lab-channel-binding:
	@test -n "$(VALUE)" || { echo 'VALUE=0|1|2 required'; exit 1; }
	$(PSRUN) $(or $(HOST),$(LAB_DC)) $(LAB_DIR)/15-set-channel-binding.ps1 300 -- -Value $(VALUE)

# Every Kerberos credential source against every channel-binding policy.
# The ccache cell at 1 is the regression case: a client that sent no token
# passed there before this feature, so a wrong token would newly fail.
#
# The whole recipe is one shell process (every line below is `\`-joined into
# one logical command), so a `trap ... EXIT` set as its first statement fires
# on every way that process can end -- the normal fall-through, the early
# `exit 1` when a policy change itself fails, and a signal -- and restores the
# DC to VALUE=0 every time. Without it, the unconditional restore this recipe
# used to put only at the bottom never ran on the early-exit path, which is
# exactly the case a failed `lab-channel-binding` call takes.
lab-acc-ldap-krb-matrix:
	@trap '$(MAKE) --no-print-directory lab-channel-binding VALUE=0 >/dev/null 2>&1' EXIT; \
	fail=0; results=''; \
	for v in 0 1 2; do \
	  $(MAKE) --no-print-directory lab-channel-binding VALUE=$$v >/dev/null || exit 1; \
	  for a in kerberos kerberos-password; do \
	    echo; echo "=== LdapEnforceChannelBinding=$$v auth=$$a ==="; \
	    if LAB_LDAP_AUTH=$$a $(LAB_DIR)/run-suite-ldap.sh $(or $(PATTERN),TestAccOULifecycle) $(or $(MINUTES),40); then \
	      results="$$results\nPASS  cb=$$v $$a"; \
	    else \
	      results="$$results\nFAIL  cb=$$v $$a"; fail=1; \
	    fi; \
	  done; \
	done; \
	echo; echo '=== channel-binding matrix ==='; printf '%b\n' "$$results"; \
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
