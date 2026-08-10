// Package provider implements the Terraform provider for Active Directory.
// It contains only Terraform concerns: schemas, plan and state mapping,
// diagnostics, and import. Every Active Directory behaviour lives in
// github.com/nemethhh/go-adpwsh and is not reimplemented here.
package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"

	adpwsh "github.com/nemethhh/go-adpwsh"
)

type adProvider struct {
	version string

	// transport, when non-nil, replaces the SSH transport at Configure time.
	// It is the test-only hook that lets the lifecycle tests drive a full
	// resource cycle with no jump box.
	transport adpwsh.Transport
}

// New returns the provider factory the plugin server serves.
func New(version string) func() provider.Provider {
	return func() provider.Provider { return &adProvider{version: version} }
}

// NewWithTransport returns a provider that talks to the supplied transport
// instead of dialling SSH. Test-only.
func NewWithTransport(tr adpwsh.Transport) provider.Provider {
	return &adProvider{version: "test", transport: tr}
}

func (p *adProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "activedirectory"
	resp.Version = p.version
}

func (p *adProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{}
}

func (p *adProvider) Configure(_ context.Context, _ provider.ConfigureRequest, _ *provider.ConfigureResponse) {
}

func (p *adProvider) Resources(_ context.Context) []func() resource.Resource {
	return nil
}

func (p *adProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return nil
}
