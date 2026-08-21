// Package provider implements the Terraform provider for Active Directory.
// It contains only Terraform concerns: schemas, plan and state mapping,
// diagnostics, and import. Every Active Directory behaviour lives in
// github.com/nemethhh/go-adpwsh and is not reimplemented here.
package provider

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	adpwsh "github.com/nemethhh/go-adpwsh"
	adlocal "github.com/nemethhh/go-adpwsh/transport/local"
	adssh "github.com/nemethhh/go-adpwsh/transport/ssh"
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
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages Active Directory objects through the ActiveDirectory " +
			"PowerShell module. `pwsh` runs either on the Windows host Terraform itself runs on " +
			"(the `local` block) or on a Windows jump box reached over SSH (the `ssh` block). " +
			"Exactly one of the two is required.",
		Attributes: map[string]schema.Attribute{
			"pwsh_path": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Path to PowerShell 7 on whichever machine runs it. " +
					"`local.pwsh_path` overrides it when the `local` block is used. Environment: " +
					"`AD_PWSH_PATH`. Defaults to `pwsh`.",
			},
		},
		Blocks: map[string]schema.Block{
			"local": schema.SingleNestedBlock{
				MarkdownDescription: "Run `pwsh` on the machine Terraform itself runs on — a " +
					"domain-joined Windows host. The spawned process inherits that machine's " +
					"logon token, so Active Directory operations authenticate as whoever launched " +
					"Terraform unless `domain.credential` says otherwise. Mutually exclusive with " +
					"`ssh`; exactly one of the two is required.",
				Attributes: map[string]schema.Attribute{
					"pwsh_path": schema.StringAttribute{Optional: true,
						MarkdownDescription: "Path to PowerShell 7 on this machine. Overrides the " +
							"top-level `pwsh_path`. Environment: `AD_PWSH_PATH`. Defaults to " +
							"`pwsh` resolved on `PATH`, and a path that cannot be resolved is a " +
							"configure-time error rather than a failure on the first resource."},
					"max_concurrency": schema.Int64Attribute{Optional: true,
						Validators: []validator.Int64{int64validator.AtLeast(1)},
						MarkdownDescription: "Simultaneous `pwsh` processes. Environment: " +
							"`AD_LOCAL_MAX_CONCURRENCY`. Defaults to `4`: every operation pays its " +
							"own `Import-Module ActiveDirectory`, each process costs real memory, " +
							"and Terraform's default parallelism is 10."},
					"timeout": schema.StringAttribute{Optional: true,
						MarkdownDescription: "Per-operation transport timeout. Environment: " +
							"`AD_LOCAL_TIMEOUT`. Defaults to `60s`."},
				},
			},
			"ssh": schema.SingleNestedBlock{
				MarkdownDescription: "Connection to the Windows jump box.",
				Attributes: map[string]schema.Attribute{
					"host": schema.StringAttribute{Optional: true,
						MarkdownDescription: "Jump box host name. Environment: `AD_SSH_HOST`."},
					"port": schema.Int64Attribute{Optional: true,
						MarkdownDescription: "SSH port. Environment: `AD_SSH_PORT`. Defaults to `22`."},
					"user": schema.StringAttribute{Optional: true,
						MarkdownDescription: "SSH user. Environment: `AD_SSH_USER`."},
					"private_key": schema.StringAttribute{Optional: true, Sensitive: true,
						MarkdownDescription: "PEM-encoded private key. Environment: `AD_SSH_PRIVATE_KEY`."},
					"private_key_path": schema.StringAttribute{Optional: true,
						MarkdownDescription: "Path to a private key file. Environment: `AD_SSH_PRIVATE_KEY_PATH`."},
					"password": schema.StringAttribute{Optional: true, Sensitive: true,
						MarkdownDescription: "SSH password. Environment: `AD_SSH_PASSWORD`."},
					"use_agent": schema.BoolAttribute{Optional: true,
						MarkdownDescription: "Authenticate through the agent at `SSH_AUTH_SOCK`."},
					"known_hosts_file": schema.StringAttribute{Optional: true,
						MarkdownDescription: "known_hosts file used to verify the host key."},
					"host_key": schema.StringAttribute{Optional: true,
						MarkdownDescription: "A pinned host key in `authorized_keys` form. " +
							"Mutually exclusive with `known_hosts_file`."},
					"insecure_ignore_host_key": schema.BoolAttribute{Optional: true,
						MarkdownDescription: "Skip host key verification. An explicit opt-out; " +
							"it takes precedence over the other two settings."},
					"max_concurrency": schema.Int64Attribute{Optional: true,
						MarkdownDescription: "Simultaneous SSH channels. Defaults to `4`, because " +
							"Windows sshd defaults to `MaxSessions 10`."},
					"timeout": schema.StringAttribute{Optional: true,
						MarkdownDescription: "Per-operation transport timeout. Defaults to `60s`."},
				},
			},
			"domain": schema.SingleNestedBlock{
				MarkdownDescription: "Domain targeting.",
				Attributes: map[string]schema.Attribute{
					"server": schema.StringAttribute{Optional: true,
						MarkdownDescription: "The domain controller to pin. Omit to discover one " +
							"at configure time. Every cmdlet this provider runs targets it, so a " +
							"write and its read-back cannot land on different replicas."},
				},
				Blocks: map[string]schema.Block{
					"credential": schema.SingleNestedBlock{
						MarkdownDescription: "Credentials passed to the AD cmdlets. Omit to use " +
							"the SSH session's own identity.",
						Attributes: map[string]schema.Attribute{
							"username": schema.StringAttribute{Optional: true,
								MarkdownDescription: "The account the cmdlets run as, in " +
									"`DOMAIN\\user` or UPN form. Required alongside `password`."},
							"password": schema.StringAttribute{Optional: true, Sensitive: true,
								MarkdownDescription: "That account's password. Required alongside " +
									"`username`. It is never written to state or to a log line."},
						},
					},
				},
			},
			"replication": schema.SingleNestedBlock{
				MarkdownDescription: "Wait for a write to reach other domain controllers.",
				Attributes: map[string]schema.Attribute{
					"wait": schema.BoolAttribute{Optional: true,
						MarkdownDescription: "Wait after each write. Defaults to `false`."},
					"targets": schema.ListAttribute{Optional: true, ElementType: types.StringType,
						MarkdownDescription: `Domain controllers to wait for, or ["all"].`},
					"force_sync": schema.BoolAttribute{Optional: true,
						MarkdownDescription: "Issue Sync-ADObject before polling. Defaults to `true`; " +
							"passive replication can legitimately take 15 minutes, which presents as a hang."},
					"timeout": schema.StringAttribute{Optional: true,
						MarkdownDescription: "How long to wait. Defaults to `60s`."},
					"poll_interval": schema.StringAttribute{Optional: true,
						MarkdownDescription: "Interval between checks. Defaults to `2s`."},
				},
			},
		},
	}
}

func (p *adProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Mask before anything is logged, not after. The library masks its own
	// payloads; this covers everything the provider itself writes.
	ctx = tflog.MaskFieldValuesWithFieldKeys(ctx, "password", "private_key", "credential", "AccountPassword")

	server, credential, diags := resolveDomain(cfg)
	resp.Diagnostics.Append(diags...)
	replication, diags := resolveReplication(ctx, cfg)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	transport := p.transport
	if transport == nil {
		kind, diags := chooseTransport(cfg)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}

		switch kind {
		case transportLocal:
			localCfg, diags := resolveLocal(cfg, os.Getenv)
			resp.Diagnostics.Append(diags...)
			if resp.Diagnostics.HasError() {
				return
			}
			tr, err := adlocal.New(localCfg)
			if err != nil {
				resp.Diagnostics.AddAttributeError(path.Root("local"),
					"Cannot run PowerShell on this machine",
					"The provider could not start PowerShell 7. This is a transport problem, "+
						"not an Active Directory one.\n\n"+err.Error())
				return
			}
			transport = tr

		case transportSSH:
			sshCfg, diags := resolveSSH(cfg, os.Getenv)
			resp.Diagnostics.Append(diags...)
			if resp.Diagnostics.HasError() {
				return
			}
			tr, err := adssh.New(sshCfg)
			if err != nil {
				resp.Diagnostics.AddAttributeError(path.Root("ssh"),
					"Cannot reach the jump box",
					"The provider could not open an SSH connection. This is a transport problem, "+
						"not an Active Directory one.\n\n"+err.Error())
				return
			}
			transport = tr
		}
	}

	client, err := adpwsh.New(ctx, adpwsh.Config{
		Transport:   transport,
		Server:      server,
		Credential:  credential,
		Replication: replication,
		Log:         tflogLogger{},
	})
	if err != nil {
		resp.Diagnostics.AddError("Cannot configure the Active Directory client",
			"The provider reached PowerShell but could not query the domain. "+
				"Check that RSAT-AD-PowerShell is installed on the machine running pwsh and "+
				"that TCP 9389 is open from it to the domain controller.\n\n"+err.Error())
		return
	}
	tflog.Debug(ctx, "activedirectory: configured", map[string]any{
		"server":                 client.Server(),
		"default_naming_context": client.DefaultNamingContext(),
	})

	resp.ResourceData = client
	resp.DataSourceData = client
}

func (p *adProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		newOUResource,
		newGroupResource,
		newUserResource,
		newGroupMemberResource,
		newGroupMembershipResource,
	}
}

func (p *adProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		newOUDataSource,
		newGroupDataSource,
		newUserDataSource,
		newGroupMembersDataSource,
		newUsersDataSource,
		newGroupsDataSource,
		newOUsDataSource,
	}
}

// clientFromProviderData is the boilerplate every resource's Configure runs.
func clientFromProviderData(data any, diags *diag.Diagnostics) *adpwsh.Client {
	if data == nil {
		return nil // Configure runs before the provider is configured; not an error.
	}
	client, ok := data.(*adpwsh.Client)
	if !ok {
		diags.AddError("Unexpected provider data",
			fmt.Sprintf("Expected *adpwsh.Client, got %T. This is a bug in the provider.", data))
		return nil
	}
	return client
}

// withTimeout applies a resource's timeouts block, defaulting to the value the
// transport already enforces.
func withTimeout(ctx context.Context, v func(context.Context, time.Duration) (time.Duration, diag.Diagnostics)) (context.Context, context.CancelFunc, diag.Diagnostics) {
	d, diags := v(ctx, 60*time.Second)
	if diags.HasError() {
		return ctx, func() {}, diags
	}
	c, cancel := context.WithTimeout(ctx, d)
	return c, cancel, diags
}
