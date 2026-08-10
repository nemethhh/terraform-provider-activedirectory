# Terraform Provider for Active Directory

Manages organizational units, groups and user accounts in Active Directory
from Terraform.

Terraform runs this provider locally; the provider opens an SSH session to a
Windows jump box, runs `pwsh` there, which does `Import-Module ActiveDirectory`
and talks to Active Directory Web Services on TCP 9389 of a single pinned
domain controller. Nothing is installed on a domain controller, and Terraform
itself never needs to run on Windows. Pinning matters: a write and the read-back
that follows it cannot land on different replicas, which is what makes an apply
converge on the first run rather than the third.

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
| Jump box | A Windows **member server** (not a domain controller) with `RSAT-AD-PowerShell`, PowerShell 7 (`pwsh`) and OpenSSH Server |
| Network | TCP 9389 open from the jump box to the domain controller; TCP 22 from wherever Terraform runs to the jump box |
| Go | 1.25 or later, to build from source |

## Usage

```hcl
terraform {
  required_providers {
    activedirectory = {
      source  = "nemethhh/activedirectory"
      version = "~> 0.1"
    }
  }
  required_version = ">= 1.11"
}

provider "activedirectory" {
  ssh {
    host             = "jump.corp.local"
    user             = "svc_tf"
    private_key_path = "~/.ssh/tf_ad"
    known_hosts_file = "~/.ssh/known_hosts"
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

Every SSH setting also reads from the environment — `AD_SSH_HOST`,
`AD_SSH_PORT`, `AD_SSH_USER`, `AD_SSH_PRIVATE_KEY`, `AD_SSH_PRIVATE_KEY_PATH`,
`AD_SSH_PASSWORD` — with configuration always winning over the environment.

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
make build   # compile the provider
make test    # unit and lifecycle tests; needs the terraform binary on PATH
make docs    # regenerate docs/ with tfplugindocs
make fmt     # gofmt and terraform fmt
```

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

## Licence

Mozilla Public License 2.0. See [LICENSE](./LICENSE).
