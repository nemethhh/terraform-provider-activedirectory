# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## The split that governs everything

This repository is a Terraform provider and **nothing else**. Every Active Directory
behaviour — cmdlet composition, DC pinning, the read-back after each write, delete
verification, error classification, serialized writes, the replication wait — lives in
[`github.com/nemethhh/go-adpwsh`](https://github.com/nemethhh/go-adpwsh) and is *not*
reimplemented here. This repo contains only schemas, plan/state mapping, diagnostics,
and import.

When a task needs new AD behaviour, the change belongs in the library, not here. The
library's operation set is deliberately closed and exposes no directory search; the one
place this repo owns PowerShell is the test sweeper (`acc_sweeper_test.go`), and it
follows the library's contract — script is a constant, every value arrives as JSON on
stdin, nothing is ever formatted into script text.

## Commands

```bash
make build     # compile the provider binary
make test      # everything that needs no domain (go test ./... -timeout 10m)
make testacc   # adds the suites that need a real domain (TF_ACC=1)
make sweep     # delete tfacc- leftovers after a crashed acceptance run
make docs      # regenerate docs/ with tfplugindocs (pinned @v0.25.0)
make fmt       # gofmt -w . && terraform fmt -recursive ./examples/
make lint      # golangci-lint run — no config file in repo, and the binary is not
               # installed by default; CI does not run it either
```

Single test, subtest, and acceptance test:

```bash
go test ./internal/provider/ -run TestResolveLocalDefaults -v
go test ./internal/provider/ -run 'TestHostileInputRoundTripsAgainstTheFake/comma' -v
TF_ACC=1 AD_ACC_CONTAINER='OU=tfacc,DC=corp,DC=local' \
  go test ./internal/provider/ -run TestAccOULifecycle -v -timeout 30m
```

`make test` needs the `terraform` binary on PATH — the lifecycle tests drive a real
Terraform CLI against an in-memory directory. It needs no Windows, no `pwsh`, no domain.

### Two format gotchas

- `gofmt -l .` and `make fmt` walk `docs/reference/`, gitignored vendored clones of other
  providers. Check formatting with `gofmt -l $(git ls-files '*.go')` instead; `make fmt`
  will rewrite those clones on disk (harmless, nothing tracked).
- `docs/*.md` is **generated**. To change documentation, edit the schema
  `MarkdownDescription` strings or `examples/provider/provider.tf`, then `make docs`.
  `examples/provider/provider.tf` is rendered verbatim into `docs/index.md`'s Example Usage.

### Local library development

A gitignored `go.work` resolves the sibling `../go-adpwsh` checkout. `go.mod` still pins
the published version, so **`GOWORK=off go build ./...` and `GOWORK=off go test ./...`
must both pass** — that is what a consumer without the workspace gets, and it is the only
way to catch depending on unreleased library code.

## Architecture

### Transport selection

`Configure` (`provider.go`) runs `chooseTransport` (`config.go`), which requires **exactly
one** of the `local {}` and `ssh {}` blocks. Zero or two is an error with one
attribute-scoped diagnostic per offending block. There is deliberately **no implicit
default**: guessing would let a mistyped `ssh` block execute locally as whoever launched
Terraform. Do not add one.

`resolveLocal` and `resolveSSH` are siblings that turn a block plus the environment into
the library's transport config. Configuration always wins over the environment; the
environment is a fallback, never an override. Errors attributable to a configuration
value use `AddAttributeError` with the attribute's path so Terraform underlines the line.

`NewWithTransport` is the test-only hook that substitutes a transport and skips selection
entirely — that is how the fake-backed suites run a full resource cycle with no jump box.

### Shared machinery the three resources depend on

The OU, group and user resources are deliberately near-identical in shape. Before adding
a resource, read one existing resource end to end; the pieces it must reuse are:

- **`dn.go`** — `keepEquivalentDN` (a DN echoed back in different case or spacing must
  plan no change) and `dnFollowsNameAndContainer` (keeps `dn` from going unknown and
  cascading a false diff). `splitDN` honours escaped commas: `OU=Sales\, EMEA` is one
  component. Never split a DN on bare commas.
- **`diagnostics.go`** — `errorDiagnostics(op, resourceType, err)` renders every
  `adpwsh.Error` kind. `KindAlreadyExists` emits a ready-to-paste `import {` block;
  a tombstone points at `Restore-ADObject`. Resources never format AD errors themselves.
- **`identity.go`** — `identityFromImportID` detects GUID / DN / SID / sAMAccountName, so
  every resource imports by all four forms.
- **`provider.go`** — `clientFromProviderData` and `withTimeout` are the boilerplate every
  resource's `Configure` and CRUD methods run.

### Resource conventions

- The parent is always `container` (never `path`), and it is a distinguished name.
- The Terraform ID is always the `objectGUID`; it survives rename and move.
- **Nothing forces a replace.** Rename and move are in-place updates, because deleting and
  recreating an AD object destroys its SID and every ACL naming it.
- Booleans are stated positively (`password_expires`, `can_change_password`) even though
  AD's own parameters are the negative form.
- A not-found during `Read` calls `RemoveResource` — drift, not failure. A not-found during
  `Delete` is success.
- `password` is a Terraform **write-only** attribute (Terraform 1.11+). It never reaches
  state, so it cannot be diffed; rotation is driven by incrementing `password_version`.

## Test architecture

This is the part that is not obvious from any single file.

### One directory, two test packages

`internal/provider/` compiles two test packages: internal `package provider` (unit tests
for `config.go`, `dn.go`, `diagnostics.go`, `identity.go`) and external
`package provider_test` (everything that drives Terraform). They share one test binary, so
there is exactly one `TestMain` — it lives in `acc_sweeper_test.go` and only diverges from
normal behaviour when `-sweep` is passed.

### Every lifecycle suite runs against two backends

`suites_test.go` holds no tests. It holds **config builders parameterised by container DN**
(`ouLifecycleSteps`, `groupLifecycleSteps`, `userLifecycleSteps`, `hostileDescriptionSteps`,
…) taking a `suiteEnv`. Each builder is driven by two entry points:

| Entry point | Backend | Gate |
|---|---|---|
| `Test*AgainstTheFake` | `fake.Directory`, via `factoriesWith(dir)` | `resource.UnitTest`, always runs |
| `TestAcc*` | a real domain, via `accFactories()` | `resource.Test`, needs `TF_ACC=1` |

**The naming convention is load-bearing: `TestAcc*` means "requires a real domain".** A
fake-backed test must never carry the `Acc` prefix. Fake-versus-real divergence is the
central risk in this design, and one set of assertions run against both backends is the
only thing that detects it — so when you change a lifecycle assertion, change the builder,
never one entry point.

### Acceptance suites fail loudly rather than skipping

`accPreCheck` calls `t.Fatal`, not `t.Skip`, on a missing variable: `resource.Test` has
already decided the suite should run, so a half-configured CI reporting green is worse
than one reporting red. Required: `AD_ACC_CONTAINER`; plus `AD_ACC_DENIED_CONTAINER` for
the denial suite and `AD_ACC_SECOND_DC` for replication. Optional: `AD_ACC_SERVER`,
`AD_ACC_USERNAME`/`AD_ACC_PASSWORD` (both or neither), `AD_ACC_PWSH_PATH`.

`accProviderConfig` writes the `local {}` block **literally** — selecting the transport by
environment variable would let the suite pass without ever exercising it.

Four suites have no fake counterpart and are acceptance-only: delegation denial
(`acc_denied_test.go`), Terraform-parallelism concurrency, `generate-config-out` brownfield
adoption, and the replication wait.

### Object naming and the sweeper

Every object any suite creates is prefixed `tfacc-` (`accNamePrefix`) and lives beneath
`AD_ACC_CONTAINER`, which is treated as pre-existing and is never created or destroyed.
`make sweep` deletes exactly the `tfacc-` objects beneath it, deepest first, and nothing
else. `accCheckDestroy` asks the *directory* whether the object is gone rather than
trusting state, because an object absent from state may still exist and now be unmanaged.

### What is written but has never been executed

No lab exists. The four acceptance-only suites, the sweeper's PowerShell, real `pwsh`,
real `Import-Module ActiveDirectory` and real ADWS have never run. Their verification is
that they compile and skip cleanly. Do not describe them as passing.

## The lab

`LAB.md` documents the two Windows Server 2025 hosts used to exercise the provider
against a real domain, and how to reach them (`ssh s-server`, `ssh s-client`). Neither
is usable by the acceptance suite yet — as of writing both carry the installer's default
computer name, are in WORKGROUP, and have neither AD DS, RSAT-AD-PowerShell nor `pwsh`
installed. `LAB.md` lists what remains.

## Scaffolding that is never committed

`docs/superpowers/` (specs, plans, brainstorms) and `docs/reference/` (vendored clones of
other providers, kept for comparison) are gitignored working material. Never `git add`
them. The design documents behind the current code are in `docs/superpowers/specs/` and are
worth reading before a substantial change.
