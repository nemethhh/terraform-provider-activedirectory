# Terraform Provider for Active Directory

Manages organizational units, groups and user accounts in Active Directory
from Terraform.

There are two deployments, and exactly one of them is configured — there is no
implicit default, because guessing would let a mistyped block run against the
wrong identity.

**On the host — the `local` block.** Terraform runs on a domain-joined Windows
member server and spawns `pwsh` there. The process inherits that machine's logon
token, so Active Directory operations authenticate as whoever launched Terraform.

```
terraform (Windows host)
   └─ provider ──spawn──▶ pwsh -EncodedCommand …   (payload on stdin)
                            └─ Import-Module ActiveDirectory
                                 └─ ADWS :9389 ──▶ pinned DC
```

**Over SSH — the `ssh` block.** Terraform runs anywhere and reaches a Windows
jump box.

```
terraform (anywhere)
   └─ ssh ──▶ jump box
                └─ pwsh -EncodedCommand …   (payload on stdin)
                     └─ Import-Module ActiveDirectory
                          └─ ADWS :9389 ──▶ pinned DC
```

Over SSH, a session authenticated by public key receives a network logon token
carrying no delegatable credentials, so onward authentication to AD Web Services
fails — the classic double hop. A `domain.credential` block works around it. On
the host the problem does not arise, and `domain.credential` remains available
for the case where operations must authenticate as some account other than the
one that launched Terraform.

Nothing is installed on a domain controller. Pinning matters in both
deployments: a write and the read-back that follows it cannot land on different
replicas, which is what makes an apply converge on the first run rather than the
third.

**Apply times.** Every operation spawns a fresh `pwsh` and pays its own
`Import-Module ActiveDirectory` — roughly 1–3 seconds on Windows. This is
inherent to the execution contract and identical in both deployments;
`max_concurrency` bounds how many run at once, defaulting to 4.

Every Active Directory behaviour — the cmdlet composition, DC pinning, the
read-back after every write, delete verification, error classification,
serialized writes and the replication wait — lives in
[`github.com/nemethhh/go-adpwsh`](https://github.com/nemethhh/go-adpwsh) and is
not reimplemented here. This repository contains only Terraform concerns:
schemas, plan and state mapping, diagnostics, and import.

## Requirements

| | |
|---|---|
| Terraform | 1.11 or later — the `password` attribute is write-only, which 1.11 introduced |
| Windows host | A Windows **member server** (not a domain controller) with `RSAT-AD-PowerShell` and PowerShell 7 (`pwsh`) on `PATH` |
| Network | TCP 9389 from that host to the domain controller. For the `ssh` deployment, also OpenSSH Server on it and TCP 22 from wherever Terraform runs |
| Go | 1.25 or later, to build from source |

With the `local` deployment, Terraform itself runs on that Windows host. With the
`ssh` deployment, Terraform runs anywhere.

## Usage

```hcl
terraform {
  required_providers {
    activedirectory = {
      source  = "nemethhh/activedirectory"
      version = "~> 0.2"
    }
  }
  required_version = ">= 1.11"
}

provider "activedirectory" {
  local {
    pwsh_path       = "pwsh"
    max_concurrency = 4
  }

  domain {
    server = "dc01.corp.local" # omit to discover one at configure time
  }
}

resource "activedirectory_ou" "staff" {
  name        = "Staff"
  container   = "DC=corp,DC=local"
  description = "Everyone on the payroll"
}
```

Settings also read from the environment, with configuration always winning:
`AD_PWSH_PATH`, `AD_LOCAL_MAX_CONCURRENCY` and `AD_LOCAL_TIMEOUT` for the local
deployment; `AD_SSH_HOST`, `AD_SSH_PORT`, `AD_SSH_USER`, `AD_SSH_PRIVATE_KEY`,
`AD_SSH_PRIVATE_KEY_PATH` and `AD_SSH_PASSWORD` for the SSH one.

A few conventions worth knowing before reading the schemas:

- The parent of an object is always `container`, never `path`, and it is a
  distinguished name. Referencing another resource's `dn` is what makes it a
  real dependency edge.
- The Terraform ID is always the `objectGUID`, which survives rename and move.
- Booleans are stated positively: `can_change_password`, `password_expires`.
  Active Directory's own parameters are the negative form, and mirroring that
  would make every configuration read as a double negative.
- Renaming or moving an object updates it in place. Nothing in this provider
  forces a replace, because deleting and recreating an AD object destroys its
  SID and every ACL that references it.

## Adopting an existing directory

Almost no Active Directory is greenfield, so adoption is a first-class path
rather than an afterthought.

**Import blocks.** Every resource imports by GUID, distinguished name, SID or
sAMAccountName; the form is detected and resolved to the GUID on the way in.

```hcl
import {
  to = activedirectory_user.jdoe
  id = "jdoe"
}
```

**Adoption on conflict.** Creating an object that already exists does not just
report the collision — it hands back the import block that adopts it, ready to
paste. If the name is instead held by a deleted object the Recycle Bin still
retains, the diagnostic says so and points at `Restore-ADObject`, because that
is a different problem with a different fix.

**Co-managed attributes.** Where an HR sync or another system writes the same
attribute, put it in `ignore_changes` rather than fighting it every apply:

```hcl
lifecycle {
  ignore_changes = [display_name]
}
```

## Passwords

`password` on `activedirectory_user` is a Terraform **write-only** attribute.
It is sent on create and on rotation and is never written to state or to a plan
file. Because a write-only value cannot be diffed, rotation is driven by
`password_version`: increment it and the plan shows a rotation.

Pair it with an ephemeral resource so the value never lands anywhere at all:

```hcl
ephemeral "random_password" "jdoe" {
  length = 24
}

resource "activedirectory_user" "jdoe" {
  sam_account_name = "jdoe"
  container        = activedirectory_ou.staff.dn
  enabled          = true
  password         = ephemeral.random_password.jdoe.result
  password_version = 1
}
```

## Not implemented yet

| Missing | Why |
|---|---|
| `attributes`, the arbitrary-attribute escape hatch | Needs the library's attribute catalog, which is gated on a schema dump from a real domain. |
| `activedirectory_object`, the untyped resource | Depends on that same catalog. |
| `activedirectory_group_membership` and `activedirectory_group_member` | Membership needs the authoritative-versus-additive distinction designed and tested as a pair; shipping half of it invites the drift it exists to prevent. |
| Data sources | Deliberately after the resources, so the read surface is shaped by what the resources proved is needed. |
| `validate_at_plan` | Its only consumers are the plan-time queries the attribute catalog introduces. |

## Development

```bash
make check     # build, vet, gofmt and test -- exactly what CI runs
make test      # unit and lifecycle tests; needs the terraform binary on PATH
make build     # compile the provider
make lint      # golangci-lint, pinned; the first run builds it and is slow
make fmt       # gofmt and terraform fmt
make fmt-check # the same, as a check rather than a rewrite
make docs      # regenerate docs/ with tfplugindocs, pinned
make testacc   # adds the suites that need a real domain
make sweep     # delete tfacc- leftovers after a crashed acceptance run
make lab-help  # operations against the Windows lab (see LAB.md)
```

`make check` is what CI runs, in the same order, so a red build is reproducible
with one command. `golangci-lint` and `tfplugindocs` are pinned by version in the
GNUmakefile and fetched on demand: neither is imported by the provider, so
neither belongs in the dependency graph a consumer resolves.

The formatting targets cover only the Go files this repository owns. A bare
`gofmt .` would also walk `docs/reference/`, gitignored clones of other providers
kept for comparison -- some 24,000 vendored files that are not ours to reformat.

The lifecycle tests drive full create/read/update/delete/import cycles against
an in-memory directory from `go-adpwsh`'s `transport/fake`, so they need no
Windows VM and no `TF_ACC`.

To develop against a local checkout of the library, put a `go.work` beside the
two repositories:

```bash
go work init . ../go-adpwsh
```

It is gitignored, and `go.mod` still pins the published version, which is what
a consumer without the workspace resolves. Build with `GOWORK=off` to confirm
that path still works.

## Running the acceptance suite

The lifecycle suites and the hostile-input table run against two backends: an
in-memory directory with no configuration at all, and a real domain when
`TF_ACC=1` is set. Four further suites — the delegation boundary, concurrency
under Terraform's own parallelism, `generate-config-out` adoption, and the
replication wait — have no in-memory counterpart and only run against a domain.

```bash
make test     # everything that needs no domain; no TF_ACC, no Windows
make testacc  # adds the suites that need one
make sweep    # deletes leftovers after a crashed run
```

A provisioned Windows lab exists for the domain-backed runs (see `LAB.md`). The
`make lab-*` targets ship this working tree to it and run the suites there:
`make lab-status` for health, `make lab-acc` for the whole acceptance suite,
`make lab-acc-only PATTERN=<re>` for a subset, and `make lab-e2e` for the e2e
layer (`make lab-help` lists them all). **Any change to real-domain behaviour
must be validated against the lab before it is called done** — the in-memory
backend proves the plan/state mapping but not the PowerShell paths, so a
real-domain path that has not been run on the lab is not "passing".

### What the suite needs

| Variable | Required by | Meaning |
|---|---|---|
| `TF_ACC=1` | every acceptance suite | Terraform's own gate |
| `AD_ACC_CONTAINER` | every acceptance suite | DN of the delegated test subtree; every object is created beneath it |
| `AD_ACC_DENIED_CONTAINER` | the denial suite | a DN the account has no rights over |
| `AD_ACC_SECOND_DC` | the replication suite | host name of a second domain controller |
| `AD_ACC_SERVER` | optional | the DC to pin; omit to discover one |
| `AD_ACC_USERNAME`, `AD_ACC_PASSWORD` | optional | run the cmdlets as this account; omit both to run as the account that launched the suite |
| `AD_ACC_PWSH_PATH` | optional | path to `pwsh`; omit to use `PATH` |

A missing required variable is a **failure**, not a skip: a half-configured CI
that reports green is worse than one that reports red.

Omitting `AD_ACC_USERNAME` and `AD_ACC_PASSWORD` is the documented default. When
they are set, the credential is written into the configuration Terraform reads
from a temporary working directory, so it is on disk for the duration of the run.

### The delegation the service account needs

One service account, **not a domain admin**, with **Full Control over
`AD_ACC_CONTAINER` and nothing outside it**.

Full Control inside the subtree is required, not merely convenient: the OU
destroy path lifts `ProtectedFromAccidentalDeletion`, which edits the object's
DACL — a right that standard OU delegation does not grant. The subtree boundary
is what makes the denial suite meaningful, because outside it the account is
genuinely powerless.

`AD_ACC_CONTAINER` is treated as pre-existing. The suite never creates or
destroys it.

### Re-runnability

Every object the suite creates is named `tfacc-…`, every acceptance test asserts
after destroy that the object is really gone from the directory rather than
merely absent from state, and `make sweep` deletes exactly the `tfacc-` objects
beneath `AD_ACC_CONTAINER`, deepest first. It never deletes anything else, so a
sweep cannot touch an object someone placed in the subtree by hand.

## Licence

Mozilla Public License 2.0. See [LICENSE](./LICENSE).
