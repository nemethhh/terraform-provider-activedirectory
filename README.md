# Terraform Provider for Active Directory

[![Terraform Registry](https://img.shields.io/github/v/release/nemethhh/terraform-provider-activedirectory?label=Terraform%20Registry&color=7B42BC&logo=terraform)](https://registry.terraform.io/providers/nemethhh/activedirectory/latest)
[![Terraform ≥ 1.11](https://img.shields.io/badge/Terraform-%E2%89%A5%201.11-7B42BC?logo=terraform)](https://developer.hashicorp.com/terraform/downloads)
[![License: MPL 2.0](https://img.shields.io/badge/License-MPL%202.0-blue)](./LICENSE)

Manage organizational units, groups, users, computers, service accounts, group
membership and access control in Active Directory from Terraform. Every write is
followed by a real read-back, so an apply converges on the first run instead of
the third.

- **Registry:** [`nemethhh/activedirectory`](https://registry.terraform.io/providers/nemethhh/activedirectory/latest)
- **Full schema reference:** [registry docs](https://registry.terraform.io/providers/nemethhh/activedirectory/latest/docs)
- **AD engine library:** [`nemethhh/go-adpwsh`](https://github.com/nemethhh/go-adpwsh)

## Features

- **8 resources, 11 data sources** — OUs, groups, users, computers, gMSAs,
  membership (edge or authoritative set) and access-control entries.
- **Full lifecycle** — create, update, **rename and move in place** (never
  destroy-and-recreate, so SIDs and ACLs survive), import and search.
- **First-apply convergence** — each write pins a domain controller and reads
  the object back from that same replica; no second apply to settle drift.
- **Three transports** — run the AD cmdlets locally on a domain-joined host,
  over SSH to a jump box, or over WinRM from anywhere (including Linux); each
  keeps a persistent PowerShell 7 runspace (`mode = "warm"`) by default.
- **Write-only passwords** — a user's `password` never touches state or a plan
  file (Terraform 1.11+); rotation is driven by a version counter.
- **Adoption is first-class** — import by GUID, DN, SID or sAMAccountName, and a
  create that collides with an existing object hands back a ready-to-paste
  `import` block.
- **Delegation without raw ACLs** — friendly rights names and curated
  delegation templates instead of hand-assembled access masks and schema GUIDs.

Nothing is installed on a domain controller: the provider drives the standard
`ActiveDirectory` PowerShell module over AD Web Services.

## Quick start

```hcl
terraform {
  required_providers {
    activedirectory = {
      source  = "nemethhh/activedirectory"
      version = "~> 0.12"
    }
  }
  required_version = ">= 1.11"
}

provider "activedirectory" {
  # Terraform runs on a domain-joined Windows host and spawns pwsh there,
  # authenticating as whoever launched Terraform.
  local {}

  domain {
    server = "dc01.corp.local" # omit to discover a DC at configure time
  }
}

resource "activedirectory_ou" "staff" {
  name        = "Staff"
  container   = "DC=corp,DC=local"
  description = "Everyone on the payroll"
}
```

Per-attribute schemas for every resource and data source live in the
[registry documentation](https://registry.terraform.io/providers/nemethhh/activedirectory/latest/docs);
this README covers the shape and the conventions.

## Requirements

| | |
|---|---|
| **Terraform** | 1.11 or later — the write-only `password` attribute requires it |
| **PowerShell host** | A Windows **member server** (not a domain controller) with `RSAT-AD-PowerShell` and PowerShell 7 (`pwsh`) or Windows PowerShell 5.1 |
| **Network** | TCP 9389 (AD Web Services) from that host to the domain controller — plus TCP 22 for the `ssh` transport, or 5985/5986 for `winrm` |

Every setting can also come from the environment (`AD_PWSH_PATH`, `AD_SSH_*`,
`AD_WINRM_*`, …), and configuration always wins over the environment.

## Connecting to Active Directory

Exactly **one** of `local`, `ssh` or `winrm` is required — there is no implicit
default, because guessing would let a mistyped block run against the wrong
identity.

| Block | Terraform runs on | Authenticates to AD as | Reach for it when |
|---|---|---|---|
| `local` | a domain-joined Windows member server | the token of whoever launched Terraform | Terraform already runs on a domain-joined Windows host |
| `ssh` | anywhere; reaches a Windows jump box over SSH | the SSH session's identity, or `domain.credential` | you want a Windows jump box and Terraform runs elsewhere |
| `winrm` | anywhere, including Linux; reaches a Windows host over WinRM | an ambient Kerberos ticket, or `winrm.user` / `winrm.password` | you drive AD from Linux/CI, or want no jump box at all |

A few things worth knowing:

- **Pin a DC with `domain.server`.** Omit it to discover one at configure time.
  Pinning is what keeps a write and its read-back on the same replica.
- **The double hop.** Over `ssh` (public-key) or `winrm` against a *member* host,
  the session carries no delegatable credentials, so onward auth to AD Web
  Services fails. Add a `domain.credential { username = …, password = … }` block
  to work around it. Against a domain controller directly, or on `local`, it
  never arises.
- **WinRM is a first-class transport.** Kerberos over HTTP (5985) by default, or
  `use_tls` for HTTPS (5986); it can also target a locked-down
  ConstrainedLanguage sandbox endpoint (`language_mode = "constrained"`). See
  [`scripts/host/`](./scripts/host/) for provisioning an endpoint a delegated
  account can use without local-administrator rights.
- **Warm or cold execution.** Every transport takes a `mode`. `warm` (the
  default) keeps a persistent PowerShell 7 runspace, so process start-up and
  `Import-Module ActiveDirectory` are paid once and amortized across operations;
  `cold` runs a fresh `pwsh` per operation and works on a Windows PowerShell 5.1
  host with no persistent runspace.
- **WinRM can fail over across hosts.** Give two or more `winrm.server` blocks
  instead of a single `host`: the provider connects to the first reachable one
  and re-probes the list when it reconnects mid-run. `server_selection =
  "round_robin"` rotates the starting host to avoid a hot primary; the default
  `"failover"` always prefers the first.
- **Replication waits are opt-in.** By default the provider does not block on a
  write reaching other DCs; set `replication { wait = true }` when a downstream
  read on a different DC needs it.

The full attribute list for each block is in the
[provider docs](https://registry.terraform.io/providers/nemethhh/activedirectory/latest/docs).

## What it manages

**Resources**

| Resource | Manages |
|---|---|
| `activedirectory_ou` | An organizational unit |
| `activedirectory_group` | A security or distribution group |
| `activedirectory_user` | A user account, with a write-only password |
| `activedirectory_computer` | A computer account — pre-stage a machine before it joins the domain |
| `activedirectory_gmsa` | A group managed service account; Active Directory owns and rotates the password |
| `activedirectory_group_member` | One **non-authoritative** membership edge — leaves every other member untouched |
| `activedirectory_group_membership` | A group's entire **authoritative** member set — reconciles out-of-band members away |
| `activedirectory_access_rule` | One access-control entry (ACE) on any object — the non-authoritative delegation primitive |

Use at most one of `activedirectory_group_member` and
`activedirectory_group_membership` per group; never manage the same group with
both.

**Data sources**

| Data source | Reads |
|---|---|
| `activedirectory_ou`, `_group`, `_user`, `_computer`, `_gmsa` | One object, by GUID or DN (security objects also by SID or sAMAccountName) |
| `activedirectory_ous`, `_groups`, `_users`, `_computers` | A search under a container, by scope and filter |
| `activedirectory_group_members` | A group's members, optionally recursive |
| `activedirectory_delegation_template` | The access rules a named delegation task expands into — fed into `activedirectory_access_rule` |

## Key conventions

Read one resource's docs end to end and these hold across all of them:

- **The parent is `container`** (never `path`), and it is a distinguished name.
  Referencing another resource's `dn` is what makes it a real dependency edge.
- **The Terraform ID is the `objectGUID`** — it survives rename and move.
- **Rename and move update in place.** The provider never deletes and recreates
  an *object*, because that would destroy its SID and every ACL naming it. The
  relationship resources (`group_member`, `access_rule`) do replace on change —
  a membership edge or an ACE has no SID of its own to preserve.
- **Booleans are stated positively** — `can_change_password`,
  `password_expires` — even though AD's own parameters are the negative form.
- **Import detects the form.** Every resource imports by GUID, DN, SID or
  sAMAccountName; the form is detected and resolved to the GUID on the way in.
- **A not-found on read is drift, not failure** — the resource is removed from
  state so the next plan re-creates it.

## Common tasks

**Create a user with a write-only password.** The value never lands in state or
a plan file; pair it with an ephemeral resource so it exists nowhere at rest.
Rotate by incrementing `password_version`.

```hcl
ephemeral "random_password" "jdoe" {
  length = 24
}

resource "activedirectory_user" "jdoe" {
  sam_account_name = "jdoe"
  container        = activedirectory_ou.staff.dn
  enabled          = true

  password         = ephemeral.random_password.jdoe.result
  password_version = 1 # bump to rotate
}
```

**Adopt an object that already exists.** Import by any identifier — and if a
create ever collides with an existing object, the error *is* the import block
you need.

```hcl
import {
  to = activedirectory_user.jdoe
  id = "jdoe" # GUID, DN, SID or sAMAccountName — detected for you
}
```

**Delegate a task without hand-writing ACEs.** A delegation template expands a
curated task into concrete rules at plan time (pure computation, no directory
read); fan them out onto the access-rule primitive with `for_each`.

```hcl
data "activedirectory_delegation_template" "manage_users" {
  task = "manage_users" # or reset_user_passwords, modify_group_membership, manage_groups
}

resource "activedirectory_access_rule" "manage_users" {
  for_each = {
    for i, r in data.activedirectory_delegation_template.manage_users.rules : tostring(i) => r
  }
  target      = activedirectory_ou.staff.dn
  trustee     = activedirectory_group.admins.id
  rights      = each.value.rights
  object_type = each.value.object_type
  applies_to  = each.value.applies_to
  type        = each.value.type
}
```

**Read the directory.** Search data sources escape and AND your `filter_by`
terms; exceeding `max_results` is an error, never a silently truncated set.

```hcl
data "activedirectory_users" "sales" {
  container = "OU=Sales,DC=corp,DC=local"
  scope     = "subtree"
  filter_by = { department = "Sales" }
}

output "sales_upns" {
  value = [for u in data.activedirectory_users.sales.users : u.user_principal_name]
}
```

## Development

This repository is the Terraform provider — **schemas, plan/state mapping,
diagnostics and import, and nothing else**. Every Active Directory behaviour
(cmdlet composition, DC pinning, the read-back after each write, delete
verification, error classification, serialized writes, the replication wait)
lives in [`go-adpwsh`](https://github.com/nemethhh/go-adpwsh). A change that
needs new AD behaviour belongs in the library, not here.

```bash
make check     # build, vet, gofmt, test — exactly what CI runs, in CI's order
make test      # unit + lifecycle tests against the in-memory fake (needs terraform on PATH)
make testacc   # adds the suites that need a real domain (TF_ACC=1)
make docs      # regenerate docs/ from schema descriptions + examples/
make lab-help  # run the suites against the provisioned Windows lab
```

Every lifecycle suite runs from **one set of assertions against two backends**:
an in-memory fake that always runs — no Windows, `pwsh` or domain required — and
a real domain gated on `TF_ACC=1`. Real-domain paths are validated on a
provisioned Windows lab; compiling and skipping cleanly is not validation.

The full build, test, lab and release process is in
[CONTRIBUTING.md](./CONTRIBUTING.md), and the lab itself in [LAB.md](./LAB.md).

## Licence

Mozilla Public License 2.0. See [LICENSE](./LICENSE).
