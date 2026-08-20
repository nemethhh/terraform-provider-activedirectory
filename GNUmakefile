default: test

# Every Go file this repository owns. docs/reference/ holds gitignored clones of
# other providers, kept for comparison: 24,000-odd vendored files that are not
# ours to format and that make a bare `gofmt .` both slow and wrong. Pruned with
# find rather than `git ls-files` so the targets also work in a source tarball.
GOFILES := $(shell find . -path ./docs/reference -prune -o -path ./.git -prune -o -name '*.go' -print)

# Tool versions are pinned here rather than added to go.mod. Neither tool is
# imported by the provider, and their dependency trees have no business in the
# graph a consumer resolves.
TFPLUGINDOCS := github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs@v0.25.0
GOLANGCILINT := github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.1

.PHONY: default build test testacc sweep lint docs fmt fmt-check vet check clean

build:
	go build -o terraform-provider-activedirectory

# Everything that needs no domain: unit tests plus the lifecycle suites against
# the in-memory directory. Needs the terraform binary on PATH; needs no Windows.
test:
	go test ./... -timeout 10m

# Adds the suites that need a real domain. See README.md, "Running the
# acceptance suite", for the variables it requires -- a missing one is a
# failure, not a skip.
testacc:
	TF_ACC=1 go test ./... -v -timeout 120m

# Deletes every tfacc- object beneath AD_ACC_CONTAINER, deepest first. Run it
# after a crashed acceptance run: without it the leftovers fail the next run on
# already-exists and the suite becomes one-shot. The region argument is
# meaningless for Active Directory and is only there because the sweeper harness
# demands one.
sweep:
	go test ./internal/provider -v -sweep=domain -timeout 30m

lint:
	go run $(GOLANGCILINT) run ./...

docs:
	go run $(TFPLUGINDOCS) generate --provider-name activedirectory

fmt:
	gofmt -w $(GOFILES)
	terraform fmt -recursive ./examples/

fmt-check:
	@unformatted="$$(gofmt -l $(GOFILES))"; \
	if [ -n "$$unformatted" ]; then echo "gofmt needed:"; echo "$$unformatted"; exit 1; fi
	terraform fmt -check -recursive ./examples/

vet:
	go vet ./...

# What CI runs, in the order CI runs it, so a red build can be reproduced with
# one command instead of by reading the workflow.
check: build vet fmt-check test

clean:
	rm -f terraform-provider-activedirectory
