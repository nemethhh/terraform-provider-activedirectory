package provider_test

import (
	"context"
	"testing"

	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"

	"github.com/nemethhh/go-adpwsh/transport/fake"
	"github.com/nemethhh/terraform-provider-activedirectory/internal/provider"
)

func TestProviderMetadata(t *testing.T) {
	p := provider.New("test")()
	var resp fwprovider.MetadataResponse
	p.Metadata(context.Background(), fwprovider.MetadataRequest{}, &resp)
	if resp.TypeName != "activedirectory" {
		t.Errorf("TypeName = %q, want activedirectory", resp.TypeName)
	}
	if resp.Version != "test" {
		t.Errorf("Version = %q", resp.Version)
	}
}

// The schema must be internally consistent — the framework validates names,
// nesting and attribute combinations here rather than at apply time.
func TestProviderSchemaIsValid(t *testing.T) {
	p := provider.New("test")()
	var resp fwprovider.SchemaResponse
	p.Schema(context.Background(), fwprovider.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("provider schema: %v", resp.Diagnostics)
	}
}

var _ = map[string]func() (tfprotov6.ProviderServer, error){
	"activedirectory": providerserver.NewProtocol6WithError(provider.New("test")()),
}

func factoriesWith(dir *fake.Directory) map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"activedirectory": providerserver.NewProtocol6WithError(provider.NewWithTransport(dir.Transport())),
	}
}

// providerConfig is prepended to every lifecycle test's configuration. The
// transport is faked, so the SSH values are placeholders that only have to
// satisfy validation.
const providerConfig = `
provider "activedirectory" {
  ssh {
    host                     = "jump.corp.local"
    user                     = "svc_tf"
    password                 = "unused-because-the-transport-is-faked"
    insecure_ignore_host_key = true
  }
}
`
