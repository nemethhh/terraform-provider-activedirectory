default: test

.PHONY: build test testacc sweep lint docs fmt
build:
	go build -o terraform-provider-activedirectory
test:
	go test ./... -timeout 10m
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
	golangci-lint run
# Pinned by version rather than added to go.mod: tfplugindocs is a documentation
# tool, and its dependency tree has no business in the graph a consumer resolves.
docs:
	go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs@v0.25.0 generate \
	  --provider-name activedirectory
fmt:
	gofmt -w . && terraform fmt -recursive ./examples/
