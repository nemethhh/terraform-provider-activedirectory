# Contributing

This is a Terraform provider and **nothing else**. Every Active Directory
behaviour — cmdlet composition, DC pinning, the read-back after each write,
delete verification, error classification, serialized writes and the replication
wait — lives in [`github.com/nemethhh/go-adpwsh`](https://github.com/nemethhh/go-adpwsh)
and is not reimplemented here. This repository contains only schemas, plan and
state mapping, diagnostics, and import. A change that needs new AD behaviour
belongs in the library, not here.

## Commands

```bash
make check     # build, vet, gofmt and test -- exactly what CI runs, in CI's order
make test      # unit and lifecycle tests; needs the terraform binary on PATH
make build     # compile the provider
make lint      # golangci-lint, pinned; the first run builds it and is slow
make fmt       # gofmt (this repo's files) + terraform fmt ./examples/
make fmt-check # the same, as a check rather than a rewrite
make docs      # regenerate docs/ with tfplugindocs, pinned
make testacc   # adds the suites that need a real domain (TF_ACC=1)
make sweep     # delete tfacc- leftovers after a crashed acceptance run
make lab-help  # operations against the Windows lab (see LAB.md)
```

`make check` is what CI runs, in the same order, so a red build is reproducible
with one command. `golangci-lint` and `tfplugindocs` are pinned by version in the
GNUmakefile and fetched on demand: neither is imported by the provider, so
neither belongs in the dependency graph a consumer resolves.

`make test` needs the `terraform` binary on `PATH` — the lifecycle tests drive a
real Terraform CLI against an in-memory directory. It needs no Windows, no
`pwsh`, and no domain.

### Two gotchas

- **Never run a bare `gofmt -l .` or `gofmt -w .` here.** It walks
  `docs/reference/`, gitignored clones of other providers — some 24,000 vendored
  files that are not ours to reformat. The Makefile targets prune that directory;
  use them rather than gofmt directly.
- **`docs/*.md` is generated.** To change documentation, edit the schema
  `MarkdownDescription` strings or `examples/provider/provider.tf`, then
  `make docs`. `examples/provider/provider.tf` is rendered verbatim into
  `docs/index.md`'s Example Usage.

## Local library development

A gitignored `go.work` resolves a sibling `../go-adpwsh` checkout:

```bash
go work init . ../go-adpwsh
```

`go.mod` still pins the published version, so `GOWORK=off go build ./...` and
`GOWORK=off go test ./...` must both pass — that is what a consumer without the
workspace gets, and it is the only way to catch depending on unreleased library
code. When a change spans both repositories, publish the go-adpwsh tag first,
then bump the pin here.

## Test architecture

### One directory, two test packages

`internal/provider/` compiles two test packages: internal `package provider`
(unit tests for `config.go`, `dn.go`, `diagnostics.go`, `identity.go`) and
external `package provider_test` (everything that drives Terraform). They share
one test binary, so there is exactly one `TestMain` — it lives in
`acc_sweeper_test.go` and only diverges from normal behaviour under `-sweep`.

### Every lifecycle suite runs against two backends

`suites_test.go` holds no tests; it holds config builders parameterised by
container DN. Each is driven by two entry points:

| Entry point | Backend | Gate |
|---|---|---|
| `Test*AgainstTheFake` | `go-adpwsh`'s in-memory `fake.Directory` | `resource.UnitTest`, always runs |
| `TestAcc*` | a real domain | `resource.Test`, needs `TF_ACC=1` |

**The naming convention is load-bearing: `TestAcc*` means "requires a real
domain".** A fake-backed test must never carry the `Acc` prefix. Fake-versus-real
divergence is the central risk in this design, and one set of assertions run
against both backends is the only thing that detects it — so change the builder,
never one entry point.

### Acceptance suites fail loudly rather than skipping

`accPreCheck` calls `t.Fatal`, not `t.Skip`, on a missing variable: a
half-configured CI reporting green is worse than one reporting red.

### The e2e layer

`TestAccE2E*` drives the provider in-process, through the real Terraform CLI,
against the real domain as multiple delegated non-admin principals. It carries
the suite's one deliberate skip: it `t.Skip`s when `AD_E2E_CONTAINER` is unset,
because it is a separately provisioned environment, and is fatal-if-missing on
every other `AD_E2E_*` once that is set.

### Object naming and the sweeper

Every object any suite creates is prefixed `tfacc-` and lives beneath
`AD_ACC_CONTAINER`, which is treated as pre-existing and is never created or
destroyed. `make sweep` deletes exactly the `tfacc-` objects beneath it, deepest
first, and nothing else.

## Running the acceptance suite

```bash
make test     # everything that needs no domain; no TF_ACC, no Windows
make testacc  # adds the suites that need one
make sweep    # deletes leftovers after a crashed run
```

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

A missing required variable is a **failure**, not a skip.

### The delegation the service account needs

One service account, **not a domain admin**, with **Full Control over
`AD_ACC_CONTAINER` and nothing outside it**. Full Control inside the subtree is
required, not merely convenient: the OU destroy path lifts
`ProtectedFromAccidentalDeletion`, which edits the object's DACL — a right that
standard OU delegation does not grant.

## The lab

A provisioned Windows lab exists for the domain-backed runs (see `LAB.md`): two
domain controllers and a domain-joined member running the real `corp.local`
domain. The `make lab-*` targets ship this working tree to it and run the suites
there — `make lab-status` for health, `make lab-acc` for the whole acceptance
suite, `make lab-acc-only PATTERN=<re>` for a subset, `make lab-e2e` for the e2e
layer (`make lab-help` lists them all).

`make lab-acc-psrp` is the odd one out: it runs the suite from *here* over psrp
instead of shipping to the member, and `LAB_PSRP_CONFIG` picks the WinRM session
configuration it opens — which is also how it picks the PowerShell engine
(`AdObjects51` for 5.1, `PowerShell.7` for 7; `lab-acc-psrp-only PATTERN=<re>`
takes a subset the same way `lab-acc-only` does). **A change to the script layer
must be validated against both engines**, once with each `LAB_PSRP_CONFIG` —
the static gate in go-adpwsh (`TestScriptsAvoidPowerShell7Constructs`) catches
known PowerShell-7-only syntax, but only a real run on 5.1 proves the scripts
still execute there.

**Any change to real-domain behaviour must be validated against the lab before
it is called done.** The in-memory backend proves the plan/state mapping but not
the PowerShell paths, so a real-domain path that has not been run on the lab is
not "passing". `LAB.md` is the source of truth for the lab's state and run log.

**`lab-acc`, `lab-acc-repl` and `lab-acc-only` do not ship first** — unlike
`lab-e2e`, they have no `lab-ship` prerequisite, because they run against
whatever was last unpacked on the member. Run `make lab-ship` yourself before
one of them, or an uncommitted fix silently never reaches the member and the
run tests stale code. `lab-ship` also always sends `git archive HEAD`, never the
working tree, so an uncommitted change is invisible to it either way.

## Scaffolding that is never committed

`docs/superpowers/` (specs, plans, brainstorms) and `docs/reference/` (vendored
clones of other providers, kept for comparison) are gitignored working material.
Never `git add` them.

## Releasing to the Terraform Registry

Releases are cut from semver tags; the `.github/workflows/release.yml` job runs
GoReleaser, which builds every target, signs the checksums with a GPG key and
attaches the artefacts the Registry ingests to a GitHub Release. The Registry
picks the release up through a webhook.

### One-time setup

1. **Create a signing key.** The Registry accepts **RSA or DSA** keys but *not*
   the default ECC type, so choose RSA explicitly:

   ```bash
   gpg --full-generate-key          # (1) RSA and RSA, 4096 bits, with a passphrase
   gpg --list-secret-keys --keyid-format=long   # note the key's long ID / fingerprint
   ```

2. **Publish the public key to the Registry.** Export it and add it at
   [registry.terraform.io/settings/gpg-keys](https://registry.terraform.io/settings/gpg-keys):

   ```bash
   gpg --armor --export "<key ID or email>"
   ```

3. **Add the private key to the repository's Actions secrets** (Settings →
   Secrets and variables → Actions):

   | Secret | Value |
   |---|---|
   | `GPG_PRIVATE_KEY` | `gpg --armor --export-secret-keys "<key ID or email>"` |
   | `PASSPHRASE` | the passphrase for that key |

   The workflow derives `GPG_FINGERPRINT` from the imported key; you do not set
   it as a secret.

4. **Connect the provider to the Registry.** Sign in at registry.terraform.io
   with the `nemethhh` GitHub account, go to
   [Publish → Provider](https://registry.terraform.io/publish/provider), and
   select this repository. A release webhook is created automatically.

### Cutting a release

```bash
make check          # build, vet, fmt, test all green
make docs           # regenerate docs/ and commit if anything changed
# bump the version constraint in README.md / examples if the minor changes
git tag v0.2.0      # a valid semver, preceded by v
git push origin v0.2.0
```

The lab member is a consumer, not a checkout: `lab-ship` sends `git archive
HEAD`, `go.work` is gitignored so it never rides along, and the member resolves
`go-adpwsh` from `go.mod` like anyone installing the provider would. A
member-side lab run (`lab-acc`, `lab-e2e`) therefore cannot exercise a library
change that has not been released yet — only `lab-acc-psrp`, running from a
workspace-enabled checkout here, can. This is exactly why [local library
development](#local-library-development) says to publish the go-adpwsh tag
before bumping the pin: bump `go.mod` first and the member keeps testing the
old library while the release goes out believing the new one was covered.

The tag push triggers `release.yml`; when it finishes, a GitHub Release carrying
the signed artefacts exists and the Registry ingests the new version.

- The tag must be a valid semantic version preceded by `v` (e.g. `v0.2.0`), and
  **no branch may share the tag's name**.
- **Never modify or replace an already-released version** — the Registry stores
  the checksums, and re-tagging causes checksum errors for everyone who has
  pulled it. Cut a new patch version instead.
