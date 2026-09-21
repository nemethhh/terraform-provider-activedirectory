# CLAUDE.md

Guidance for Claude Code (claude.ai/code) working in this repository. It covers
the invariants that break silently when violated and where things live; the
mechanical process — build, test, lab, release — is in
[CONTRIBUTING.md](./CONTRIBUTING.md).

## The one rule

This repository is a Terraform provider and **nothing else**. Every Active
Directory behaviour — cmdlet composition, DC pinning, the read-back after each
write, delete verification, error classification, serialized writes and the
replication wait — lives in a library and is *not* reimplemented here. This repo
contains only schemas, plan/state mapping, diagnostics, and import.

## Two backends behind one contract

There are now **two** libraries, and the provider cannot tell them apart:

- [`go-adpwsh`](https://github.com/nemethhh/go-adpwsh) drives AD through
  PowerShell, over the `local`, `ssh` or `winrm` connection.
- [`go-adldap`](https://github.com/nemethhh/go-adldap) speaks LDAPS to a domain
  controller directly, over the `ldap` connection — no PowerShell, no RSAT, no
  Windows host.

Both satisfy `adcore.Directory` from
[`go-adcore`](https://github.com/nemethhh/go-adcore), which is also where the
shared vocabulary lives: the models, the specs, `Identity`, `Secret`, the
normalized error `Kind`, and the invariant machinery both backends compose.
Provider data is an `adcore.Directory`, so a resource cannot tell which backend
configured it — that is what lets one set of resources serve both.

**A change to AD behaviour belongs in whichever library owns it, never here.**
A change that would apply to both belongs in `go-adcore`. If a resource needs
something the contract does not expose, widen the contract; do not reach past it
to a concrete client.

`go-adcore/adcoretest.RunDirectorySuite` is the behavioural conformance suite
every backend runs. It replaces what a single shared implementation used to
guarantee structurally: read-back after write, delete verification, serialized
writes per identity, a search that errors rather than truncating, and a rename
that never replaces the object. A backend that does not run it is not a
conforming implementation.

`chooseConnection` covers **four** mutually exclusive blocks with no implicit
default, for the same reason it covered three: guessing one would let a mistyped
block run against the wrong identity, or over the wrong protocol. Note that
`ldap` is not a transport — the `mode` (warm/cold) axis describes how `pwsh` is
driven, and that path runs none.

The `ldap` connection currently manages **organizational units, groups and
users**. gMSAs, computers, and everything needing a security descriptor still
require a PowerShell connection; `can_change_password = false` on the LDAP path
is refused as unsupported rather than silently ignored.

**When a task needs new AD behaviour, the change belongs in the library, not
here.** The library's operation set is deliberately narrow: as of `go-adpwsh`
v0.4.0 it exposes directory search as three typed, class-scoped reads
(`OU.Search`/`Group.Search`/`User.Search`), but there is still no generic object
search, and object mutation remains get-by-identity only. The one place this repo
owns PowerShell is the test sweeper (`acc_sweeper_test.go`), and it follows the
library's contract — script is a constant, every value arrives as JSON on stdin,
nothing is ever formatted into script text.

## Invariants that break silently if violated

These are the conventions the three resources (OU, group, user) share. Before
adding or changing a resource, read one existing resource end to end and preserve
these — a violation compiles and often passes the fake, then diverges on a real
domain or cascades a false diff.

**Resource semantics**

- The parent is always `container` (never `path`), and it is a distinguished name.
- The Terraform ID is always the `objectGUID`; it survives rename and move.
- **Nothing forces a replace.** Rename and move are in-place updates, because
  deleting and recreating an AD object destroys its SID and every ACL naming it.
- Booleans are stated positively (`password_expires`, `can_change_password`) even
  though AD's own parameters are the negative form.
- A not-found during `Read` calls `RemoveResource` — drift, not failure. A
  not-found during `Delete` is success.
- `password` is a Terraform **write-only** attribute (Terraform 1.11+). It never
  reaches state, so it cannot be diffed; rotation is driven by incrementing
  `password_version`.

**Shared machinery — reuse it, do not reinvent it**

- **`dn.go`** — `keepEquivalentDN` (a DN echoed back in different case or spacing
  must plan no change) and `dnFollowsNameAndContainer` (keeps `dn` from going
  unknown and cascading a false diff). `splitDN` honours escaped commas:
  `OU=Sales\, EMEA` is one component. Never split a DN on bare commas.
- **`diagnostics.go`** — `errorDiagnostics(op, resourceType, err)` renders every
  `adpwsh.Error` kind. `KindAlreadyExists` emits a ready-to-paste `import {`
  block; a tombstone points at `Restore-ADObject`. Resources never format AD
  errors themselves.
- **`identity.go`** — `identityFromImportID` detects GUID / DN / SID /
  sAMAccountName, so every resource imports by all four forms.
- **`provider.go`** — `clientFromProviderData` and `withTimeout` are the
  boilerplate every resource's `Configure` and CRUD methods run.

**Transport selection has no implicit default**

`Configure` (`provider.go`) runs `chooseTransport` (`config.go`), which requires
**exactly one** of the `local {}`, `ssh {}` and `psrp {}` blocks. Zero or more
than one is an error with one attribute-scoped diagnostic per offending block.
There is deliberately no implicit default: guessing would let a mistyped `ssh`
block execute locally as whoever launched Terraform. Do not add one.
`resolveLocal`/`resolveSSH`/`resolvePSRP` turn a block plus the environment into
the library's transport config — configuration always wins over the environment.
`NewWithTransport` is the test-only hook that substitutes a transport and skips
selection, so the fake-backed suites run a full resource cycle with no jump box.

## Two gotchas

- **Never run a bare `gofmt -l .` or `gofmt -w .` here.** It walks
  `docs/reference/`, gitignored clones of other providers — some 24,000 vendored
  files that are not ours to reformat. Use the Makefile targets, which prune it.
- **`docs/*.md` is generated.** To change documentation, edit the schema
  `MarkdownDescription` strings or `examples/provider/provider.tf`, then
  `make docs`. `examples/provider/provider.tf` renders verbatim into
  `docs/index.md`'s Example Usage.

## Tests: the fake/real duality

Full detail is in [CONTRIBUTING.md](./CONTRIBUTING.md#test-architecture); the
load-bearing facts an agent must not break:

- `internal/provider/` compiles **two test packages** sharing one binary, so
  there is exactly one `TestMain` (in `acc_sweeper_test.go`).
- Every lifecycle suite is a container-parameterised builder in `suites_test.go`
  driven by **two** entry points: `Test*AgainstTheFake` (in-memory, always runs)
  and `TestAcc*` (real domain, needs `TF_ACC=1`).
- **`TestAcc*` means "requires a real domain" — the naming is load-bearing.** A
  fake-backed test must never carry the `Acc` prefix. When you change a lifecycle
  assertion, change the **builder**, never one entry point, or the two backends
  drift.
- Acceptance suites `t.Fatal` (not `t.Skip`) on a missing variable; the e2e layer
  (`TestAccE2E*`) is the one deliberate skip, gated on `AD_E2E_CONTAINER`.
- **The script layer targets Windows PowerShell 5.1**, not just 7. `?.`, `??`,
  ternaries, `ConvertFrom-Json -AsHashtable` and `ForEach-Object -Parallel` are
  rejected by `TestScriptsAvoidPowerShell7Constructs` in go-adpwsh — but that is
  a static gate. Only `make lab-acc-psrp` against both a 5.1 and a 7 endpoint
  proves the scripts still run.

## Real-domain changes must run on the lab

A provisioned Windows lab (`corp.local`, two DCs and a member) is the required
validation path for anything that needs a real domain. **Compiling and skipping
cleanly is not validation.** Any change that touches AD behaviour must be run
against the lab (`make lab-acc`, `make lab-e2e`, or `make lab-acc-only
PATTERN=…`) before it is described as working — say so plainly when a real-domain
path has not actually been run. `LAB.md` is the source of truth for the lab and
its run log; the `make lab-*` mechanics are in
[CONTRIBUTING.md](./CONTRIBUTING.md#the-lab).

## Releases

The provider publishes to the Terraform Registry as
[`nemethhh/activedirectory`](https://registry.terraform.io/providers/nemethhh/activedirectory).
A semver tag (`vX.Y.Z`) triggers `.github/workflows/release.yml`, which runs
GoReleaser to build every target, GPG-sign the checksums and attach the artefacts
the Registry ingests. The full cut-a-release checklist, the GPG-key and
GitHub-secrets setup, and the Registry-side steps are in
[CONTRIBUTING.md](./CONTRIBUTING.md#releasing-to-the-terraform-registry). Never
modify an already-released tag — the Registry stores its checksums.
