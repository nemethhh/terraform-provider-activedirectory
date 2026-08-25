# Terraform Provider for Active Directory

Manage organizational units, groups, user accounts and access control in Active
Directory from Terraform — create, update, rename, move, import, search and
delegate, with a real read-back after every write so an apply converges on the
first run.

- **Registry:** [`nemethhh/activedirectory`](https://registry.terraform.io/providers/nemethhh/activedirectory/latest)
- **Documentation:** [registry docs](https://registry.terraform.io/providers/nemethhh/activedirectory/latest/docs)
- **Licence:** Mozilla Public License 2.0

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
    pwsh_path = "pwsh"
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

## How it works

The provider drives the `ActiveDirectory` PowerShell module — nothing is
installed on a domain controller. There are two deployments, and exactly one is
configured: there is no implicit default, because guessing would let a mistyped
block run against the wrong identity.

**On the host — the `local` block.** Terraform runs on a domain-joined Windows
member server and spawns `pwsh` there. The process inherits that machine's logon
token, so operations authenticate as whoever launched Terraform.

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

Most operations produce a script too large for a command line, so over SSH they
are shipped to a temporary file with SFTP and run with `-File` — which also lets
the transport work when the jump box's OpenSSH `DefaultShell` is `cmd.exe`. This
needs the OpenSSH `sftp` subsystem, which Windows OpenSSH enables by default.

DC pinning matters in both deployments: a write and the read-back that follows
it cannot land on different replicas, which is what makes an apply converge on
the first run rather than the third.

**Apply times.** Every operation spawns a fresh `pwsh` and pays its own
`Import-Module ActiveDirectory` — roughly 1–3 seconds on Windows. This is
inherent to the execution contract and identical in both deployments;
`max_concurrency` bounds how many run at once, defaulting to 4.

## Requirements

| | |
|---|---|
| Terraform | 1.11 or later — the `password` attribute is write-only, which 1.11 introduced |
| Windows host | A Windows **member server** (not a domain controller) with `RSAT-AD-PowerShell` and PowerShell 7 (`pwsh`) **or** Windows PowerShell 5.1 (`powershell.exe`) on `PATH` |
| Network | TCP 9389 from that host to the domain controller. For the `ssh` deployment, also OpenSSH Server on it and TCP 22 from wherever Terraform runs |

With the `local` deployment, Terraform itself runs on that Windows host. With the
`ssh` deployment, Terraform runs anywhere. There is also a `psrp` deployment,
reaching the host over WinRM instead — 5.1 is proven there (34 acceptance runs
against a live domain, see `LAB.md`) but unverified for non-ASCII input over
`local`/`ssh`; see `scripts/host/README.md` to provision a 5.1 endpoint a
delegated account can use without local-administrator rights.

Settings also read from the environment, with configuration always winning:
`AD_PWSH_PATH`, `AD_LOCAL_MAX_CONCURRENCY` and `AD_LOCAL_TIMEOUT` for the local
deployment; `AD_SSH_HOST`, `AD_SSH_PORT`, `AD_SSH_USER`, `AD_SSH_PRIVATE_KEY`,
`AD_SSH_PRIVATE_KEY_PATH` and `AD_SSH_PASSWORD` for the SSH one.

## What it manages

**Resources**

| Resource | Manages |
|---|---|
| `activedirectory_ou` | An organizational unit |
| `activedirectory_group` | A group |
| `activedirectory_user` | A user account |
| `activedirectory_group_member` | A single, non-authoritative membership edge — leaves other members untouched |
| `activedirectory_group_membership` | A group's entire, authoritative member set — reconciles out-of-band members away |
| `activedirectory_access_rule` | A single access-control entry (ACE) on any object — non-authoritative; grants a trustee specific rights, the building block for delegation |

Use at most one of `activedirectory_group_member` and
`activedirectory_group_membership` per group; do not manage the same group with
both.

**Data sources**

| Data source | Reads |
|---|---|
| `activedirectory_ou` / `activedirectory_group` / `activedirectory_user` | One object by GUID, DN, SID or sAMAccountName |
| `activedirectory_ous` / `activedirectory_groups` / `activedirectory_users` | A search under a container, by scope and filter |
| `activedirectory_group_members` | A group's direct members |
| `activedirectory_delegation_template` | The access rules a named delegation task expands into — fed into `activedirectory_access_rule` |

A few conventions worth knowing before reading the schemas:

- The parent of an object is always `container`, never `path`, and it is a
  distinguished name. Referencing another resource's `dn` is what makes it a
  real dependency edge.
- The Terraform ID is always the `objectGUID`, which survives rename and move.
- Booleans are stated positively: `can_change_password`, `password_expires`.
  Active Directory's own parameters are the negative form, and mirroring that
  would make every configuration read as a double negative.
- Renaming or moving an *object* updates it in place — the provider never
  deletes and recreates an object, because that would destroy its SID and every
  ACL that references it. The relationship resources
  (`activedirectory_group_member`, `activedirectory_access_rule`) do replace on
  change, since a membership edge or an ACE has no SID of its own to preserve.

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

## Access control and delegation

`activedirectory_access_rule` manages a **single access-control entry** on any
object's DACL. It is non-authoritative: it owns exactly the ACE it creates and
leaves every other entry — inherited defaults, other trustees' grants —
untouched, so several rules can target the same object safely. Rights and object
types are named, not raw masks or GUIDs; the friendly names resolve to schema
GUIDs against the directory, with the common ones answered from a built-in table.

```hcl
resource "activedirectory_access_rule" "helpdesk_reset_pw" {
  target      = activedirectory_ou.staff.dn        # any object, DN or GUID
  trustee     = activedirectory_group.helpdesk.id  # any security principal
  rights      = ["ExtendedRight"]
  object_type = "Reset Password"
  applies_to  = { scope = "descendants", object_class = "user" }
  type        = "Allow"
}
```

For the common tasks, `activedirectory_delegation_template` expands a named task
into the exact set of rules it needs; fan them out with `for_each`:

```hcl
data "activedirectory_delegation_template" "manage_users" {
  task = "manage_users" # or reset_user_passwords, modify_group_membership, manage_groups
}

resource "activedirectory_access_rule" "manage_users" {
  for_each    = { for i, r in data.activedirectory_delegation_template.manage_users.rules : i => r }
  target      = activedirectory_ou.staff.dn
  trustee     = activedirectory_group.admins.id
  rights      = each.value.rights
  object_type = each.value.object_type
  applies_to  = each.value.applies_to
  type        = each.value.type
}
```

An access rule is a relationship, not an object: changing any field replaces it
(a revoke of the old ACE and a grant of the new one), and drift is matched
against the explicit ACE only, so inherited entries are never fought.

## Contributing

Building from source, the test architecture, the Windows lab and the release
process are documented in [CONTRIBUTING.md](./CONTRIBUTING.md).

## Licence

Mozilla Public License 2.0. See [LICENSE](./LICENSE).
