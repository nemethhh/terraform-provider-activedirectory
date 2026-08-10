default: test

.PHONY: build test testacc lint docs fmt
build:
	go build -o terraform-provider-activedirectory
test:
	go test ./... -timeout 10m
testacc:
	TF_ACC=1 go test ./... -v -timeout 120m
lint:
	golangci-lint run
docs:
	go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs generate \
	  --provider-name activedirectory
fmt:
	gofmt -w . && terraform fmt -recursive ./examples/
