// Package provider implements the Terraform provider for Active Directory.
// It contains only Terraform concerns: schemas, plan and state mapping,
// diagnostics, and import. Every Active Directory behaviour lives in
// github.com/nemethhh/go-adpwsh and is not reimplemented here.
package provider

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/nemethhh/go-adcore"
	adldap "github.com/nemethhh/go-adldap"
	adpwsh "github.com/nemethhh/go-adpwsh"
	adlocal "github.com/nemethhh/go-adpwsh/transport/local"
	adlocalwarm "github.com/nemethhh/go-adpwsh/transport/localwarm"
	adssh "github.com/nemethhh/go-adpwsh/transport/ssh"
	adsshwarm "github.com/nemethhh/go-adpwsh/transport/sshwarm"
	adwinrm "github.com/nemethhh/go-adpwsh/transport/winrm"
)

type adProvider struct {
	version string

	// transport, when non-nil, replaces the SSH transport at Configure time.
	// It is the test-only hook that lets the lifecycle tests drive a full
	// resource cycle with no jump box.
	transport adpwsh.Transport

	// directory, when non-nil, replaces connection selection entirely. It is
	// the hook that lets one lifecycle suite run against either backend.
	directory *adcore.Directory
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

// NewWithDirectory substitutes a directory and skips connection selection, so
// a suite can drive a full resource cycle against any backend — or against the
// in-memory fake, with no jump box and no domain. Test-only.
func NewWithDirectory(dir adcore.Directory) provider.Provider {
	return &adProvider{version: "test", directory: &dir}
}

func (p *adProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "activedirectory"
	resp.Version = p.version
}

func (p *adProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages Active Directory objects over one of two backends. The " +
			"`ldap` block speaks LDAPS to a domain controller directly, with no PowerShell and no " +
			"Windows host. The other three drive the ActiveDirectory PowerShell module: `pwsh` " +
			"runs on the Windows host Terraform itself runs on (the `local` block), on a Windows " +
			"jump box reached over SSH (the `ssh` block), or on a Windows host reached over " +
			"PSRP/WinRM (the `winrm` block). Exactly one of the four is required. Every resource " +
			"and data source works on both backends.",
		Attributes: map[string]schema.Attribute{
			"pwsh_path": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Path to PowerShell 7 on whichever machine runs it. " +
					"`local.pwsh_path` overrides it when the `local` block is used. Environment: " +
					"`AD_PWSH_PATH`. Defaults to `pwsh`. Not used by the `ldap` connection.",
			},
		},
		Blocks: map[string]schema.Block{
			"local": schema.SingleNestedBlock{
				MarkdownDescription: "Run `pwsh` on the machine Terraform itself runs on — a " +
					"domain-joined Windows host. The spawned process inherits that machine's " +
					"logon token, so Active Directory operations authenticate as whoever launched " +
					"Terraform unless `domain.credential` says otherwise. Mutually exclusive with " +
					"`ssh`, `winrm` and `ldap`; exactly one of the four is required.",
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
							"`AD_LOCAL_TIMEOUT`. Defaults to `90s` — deliberately longer than a " +
							"resource's own default 60s operation budget (see the `timeouts` block on " +
							"each resource), so that when both are left at their default, the caller's " +
							"deadline expires first rather than racing the transport's."},
					"mode": schema.StringAttribute{Optional: true,
						Validators: []validator.String{stringvalidator.OneOf("cold", "warm")},
						MarkdownDescription: "Execution mode. `warm` (default) keeps a persistent " +
							"PowerShell 7 runspace so process startup and `Import-Module ActiveDirectory` " +
							"are paid once per pooled process and amortized across operations; `cold` runs " +
							"a fresh `pwsh -EncodedCommand` for every operation. Both modes run `pwsh` on " +
							"this machine."},
				},
			},
			"ssh": schema.SingleNestedBlock{
				MarkdownDescription: "Run `pwsh` on a Windows jump box reached over SSH. " +
					"Mutually exclusive with `local`, `winrm` and `ldap`; exactly one of the four " +
					"is required.",
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
						MarkdownDescription: "Per-operation transport timeout. Defaults to `90s` — " +
							"deliberately longer than a resource's own default 60s operation budget " +
							"(see the `timeouts` block on each resource), so that when both are left " +
							"at their default, the caller's deadline expires first rather than racing " +
							"the transport's."},
					"mode": schema.StringAttribute{Optional: true,
						Validators: []validator.String{stringvalidator.OneOf("cold", "warm")},
						MarkdownDescription: "Execution mode. `warm` (default) keeps a persistent " +
							"PowerShell 7 runspace on the jump box so startup and `Import-Module " +
							"ActiveDirectory` are paid once per pooled channel and amortized; `cold` " +
							"runs a fresh `pwsh -EncodedCommand` per operation. `warm` requires " +
							"PowerShell 7 on the jump box with the `powershell` sshd subsystem " +
							"registered (`pwsh -sshs`); set `mode = \"cold\"` for a Windows PowerShell " +
							"5.1 jump box."},
				},
			},
			"winrm": schema.SingleNestedBlock{
				MarkdownDescription: "Run the AD cmdlets on a Windows host reached over " +
					"PSRP/WinRM. Kerberos over HTTP (5985) by default, using the runner's " +
					"ambient Kerberos ticket; set `use_tls` for HTTPS (5986). Target a " +
					"domain controller, or a member/management host together with " +
					"`domain.credential`. Mutually exclusive with `local`, `ssh` and `ldap`; " +
					"exactly one of the four is required.",
				Attributes: map[string]schema.Attribute{
					"host": schema.StringAttribute{Optional: true,
						MarkdownDescription: "Target host, an FQDN (the Kerberos SPN defaults to `HTTP/<host>`). Environment: `AD_WINRM_HOST`."},
					"port": schema.Int64Attribute{Optional: true,
						MarkdownDescription: "WinRM port. Environment: `AD_WINRM_PORT`. Defaults to `5985` (HTTP) or `5986` when `use_tls` is set."},
					"use_tls": schema.BoolAttribute{Optional: true,
						MarkdownDescription: "Use HTTPS/WinRM-over-TLS. Environment: `AD_WINRM_USE_TLS`. Required for NTLM auth; Kerberos encrypts over plain HTTP without it."},
					"insecure_skip_verify": schema.BoolAttribute{Optional: true,
						MarkdownDescription: "Skip TLS certificate verification (testing only; requires `use_tls`). Environment: `AD_WINRM_INSECURE_SKIP_VERIFY`."},
					"user": schema.StringAttribute{Optional: true,
						MarkdownDescription: "WinRM auth user in `DOMAIN\\user` or UPN form. Required: go-psrp needs the principal name even when an ambient Kerberos ticket cache supplies the credentials, because Linux has no SSPI single sign-on. Only on Windows, authenticating via SSPI SSO, can this be omitted. Environment: `AD_WINRM_USER`."},
					"password": schema.StringAttribute{Optional: true, Sensitive: true,
						MarkdownDescription: "WinRM auth password. Environment: `AD_WINRM_PASSWORD`. Never written to state or a log line."},
					"domain": schema.StringAttribute{Optional: true,
						MarkdownDescription: "NTLM domain. Environment: `AD_WINRM_DOMAIN`."},
					"spn": schema.StringAttribute{Optional: true,
						MarkdownDescription: "Kerberos service principal name. Defaults to `HTTP/<host>` (AD's sPNMappings alias it to the host's HOST SPN). Environment: `AD_WINRM_SPN`."},
					"realm": schema.StringAttribute{Optional: true,
						MarkdownDescription: "Kerberos realm; defaults to krb5.conf's `default_realm`. Environment: `AD_WINRM_REALM`."},
					"krb5_conf_path": schema.StringAttribute{Optional: true,
						MarkdownDescription: "Path to krb5.conf. Environment: `AD_WINRM_KRB5_CONF`, else ambient `KRB5_CONFIG`, else `/etc/krb5.conf`."},
					"ccache_path": schema.StringAttribute{Optional: true,
						MarkdownDescription: "Path to the Kerberos ticket cache. Environment: `AD_WINRM_CCACHE`, else ambient `KRB5CCNAME`."},
					"keytab_path": schema.StringAttribute{Optional: true,
						MarkdownDescription: "Path to a keytab for headless runners. Environment: `AD_WINRM_KEYTAB`."},
					"configuration_name": schema.StringAttribute{Optional: true,
						MarkdownDescription: "WinRM session configuration. Environment: `AD_WINRM_CONFIGURATION_NAME`. " +
							"Defaults to `PowerShell.7`.\n\n" +
							"Point this at a purpose-made Windows PowerShell 5.1 endpoint — see " +
							"`scripts/host/README.md`, which registers one per capability tier and needs no " +
							"PowerShell 7 installation. The difference is not cosmetic — a PowerShell 7 endpoint " +
							"refuses a non-administrator caller unless the endpoint itself runs as a virtual " +
							"account, which is a local administrator on that host. A 5.1 endpoint with no RunAs " +
							"identity runs as the connecting account, so a delegated service account needs no " +
							"privilege on the management host at all. `microsoft.powershell`, the built-in 5.1 " +
							"endpoint, only works when the caller is already a local administrator on that host: " +
							"its stock security descriptor grants only `BUILTIN\\Administrators` and " +
							"`Remote Management Users`, and this branch's own provisioning script locks it (and " +
							"`PowerShell.7*`) to administrators regardless."},
					"language_mode": schema.StringAttribute{Optional: true,
						Validators: []validator.String{stringvalidator.OneOf("full", "constrained")},
						MarkdownDescription: "PowerShell language mode of the target endpoint. Environment: " +
							"`AD_WINRM_LANGUAGE_MODE`. `full` (default) is the existing behaviour. `constrained` targets a " +
							"ConstrainedLanguage sandbox endpoint (register one with " +
							"`scripts/host/New-AdProviderEndpoint.ps1 -Sandbox`): the connecting team account " +
							"is confined to AD cmdlets with no host escape, and the payload is delivered " +
							"without `[Console]`. ACL delegation works in `constrained` mode when the endpoint " +
							"was registered with the `acl` capability (its FunctionDefinitions run the ACL " +
							"cmdlets in a FullLanguage island); against a sandbox endpoint without that " +
							"capability, an ACL apply fails with a message telling you to re-register it."},
					"max_concurrency": schema.Int64Attribute{Optional: true,
						Validators: []validator.Int64{int64validator.AtLeast(1)},
						MarkdownDescription: "Size of the pool of independent WinRM/PSRP sessions (each its own process on the target). Environment: `AD_WINRM_MAX_CONCURRENCY`. Defaults to `4`, like `ssh`/`local`; each session costs a `wsmprovhost` process and a warm AD module on the target, well under WinRM's 30-shell-per-user default. " +
							"Each session's underlying WinRM shell is leased for 2 minutes rather than WinRM's own 30-minute default, so it cannot outlive an idle Terraform process for long; the transport transparently rebuilds a shell that gets reaped out from under it, but if that reap ever surfaces as an error, this 2-minute lease is where it comes from. See `scripts/host/Initialize-AdProvisioningHost.ps1`'s `MaxShellsPerUser`, which must cover `max_concurrency` times however many Terraform processes can start within that 2-minute window."},
					"timeout": schema.StringAttribute{Optional: true,
						MarkdownDescription: "Per-operation transport timeout. Defaults to `90s` — " +
							"deliberately longer than a resource's own default 60s operation budget " +
							"(see the `timeouts` block on each resource), so that when both are left " +
							"at their default, the caller's deadline expires first rather than racing " +
							"the transport's."},
					"mode": schema.StringAttribute{Optional: true,
						Validators: []validator.String{stringvalidator.OneOf("cold", "warm")},
						MarkdownDescription: "Execution mode. `warm` (the default) keeps a persistent " +
							"PSRP runspace, paying `pwsh` startup and `Import-Module ActiveDirectory` " +
							"once per pooled process, and needs a registered PSRP session configuration " +
							"(`configuration_name`). `cold` opens a fresh Windows Remote Shell per " +
							"operation and feeds the script on stdin to `powershell -EncodedCommand` " +
							"(Windows PowerShell 5.1 by default) — slower, but it needs **no** " +
							"server-side PSRP session configuration, so it fits a host where PSRP " +
							"remoting is disabled but WinRS is allowed. `cold` uses only the default " +
							"WinRS shell, so `configuration_name`/`language_mode` do not apply; the " +
							"`user` here must have WinRS shell access (Remote Management Users, or " +
							"admin)."},
					"server_selection": schema.StringAttribute{Optional: true,
						Validators: []validator.String{stringvalidator.OneOf("failover", "round_robin")},
						MarkdownDescription: "How the provider picks among multiple `server` blocks at " +
							"connect time. `failover` (default) always prefers the first reachable host " +
							"in order, leaving the rest on standby. `round_robin` rotates across the hosts " +
							"as connections are (re)established, spreading PowerShell-execution load so no " +
							"single host stays hot; it still fails through to the next host when the chosen " +
							"one is down. Selection is per-connection, not per-operation, and changes only " +
							"**where `pwsh` runs** — each Active Directory write and its read-back stay " +
							"pinned to one domain controller regardless. No effect with a single `host` or " +
							"one `server`. Requires `mode = \"warm\"`."},
				},
				Blocks: map[string]schema.Block{
					"server": schema.ListNestedBlock{
						MarkdownDescription: "A WinRM host in an ordered failover list. Give two or more " +
							"`server` blocks to have the provider connect to the first reachable host and " +
							"re-probe the list whenever it reconnects during a run. Mutually exclusive with " +
							"`host`; all auth/TLS/Kerberos/`configuration_name`/`language_mode`/`mode`/" +
							"`max_concurrency`/`timeout` settings stay on the `winrm` block and are shared. " +
							"Requires `mode = \"warm\"`.",
						NestedObject: schema.NestedBlockObject{
							Attributes: map[string]schema.Attribute{
								"host": schema.StringAttribute{Required: true,
									MarkdownDescription: "Target host, an FQDN (the Kerberos SPN defaults to `HTTP/<host>`)."},
								"port": schema.Int64Attribute{Optional: true,
									MarkdownDescription: "WinRM port for this host. Defaults to the `winrm` block's `port`, then `5985`/`5986`."},
								"spn": schema.StringAttribute{Optional: true,
									MarkdownDescription: "Kerberos SPN for this host. Defaults to `HTTP/<host>`."},
							},
						},
					},
				},
			},
			"ldap": schema.SingleNestedBlock{
				MarkdownDescription: "Connect straight to a domain controller over LDAPS, with no " +
					"PowerShell, no RSAT and no Windows host anywhere — the provider binary and TCP 636 " +
					"are the whole runtime requirement.\n\n" +
					"**Every resource this provider offers works over this connection**, including the " +
					"ones backed by a security descriptor: `activedirectory_access_rule`, delegation " +
					"templates, resource-based constrained delegation, " +
					"`protected_from_accidental_deletion` and `can_change_password`.\n\n" +
					"Exactly one of `simple`, `kerberos` or `ntlm` is required. `kerberos {}` with no " +
					"attributes uses the ticket in the cache `KRB5CCNAME` names, so running " +
					"`KRB5CCNAME=FILE:… kinit` before Terraform keeps every credential out of " +
					"configuration. `KRB5CCNAME` must be set: the default cache location is not " +
					"searched. That path reads a `FILE:` credential cache, which **Windows does not " +
					"have** — a Windows client uses `simple` or `ntlm`.\n\n" +
					"Against a domain that enforces LDAP channel binding (`LdapEnforceChannelBinding = " +
					"2`), `kerberos` binds over both `ldaps` and `starttls`, `simple` is not subject to " +
					"the policy, and `ntlm` is refused.\n\n" +
					"The `domain` block does not apply: `server` names the domain controller and the " +
					"authentication block is the identity, so setting `domain.server` or " +
					"`domain.credential` alongside `ldap` is an error rather than silently ignored. " +
					"`replication` applies as on the other connections; `force_sync` there is a " +
					"directory operation rather than `Sync-ADObject`.\n\n" +
					"Mutually exclusive with `local`, `ssh` and `winrm`; exactly one of the four is " +
					"required.",
				Attributes: map[string]schema.Attribute{
					"server": schema.StringAttribute{Optional: true,
						MarkdownDescription: "The domain controller to connect to, as an FQDN. Pinned for " +
							"the provider's lifetime: there is no discovery and no failover, because a write " +
							"that lands on one DC and a read-back that hits another reports \"not found\". " +
							"Falls back to `AD_LDAP_SERVER`."},
					"port": schema.Int64Attribute{Optional: true,
						MarkdownDescription: "TCP port. Defaults to `636` for `ldaps` and `389` for `starttls`."},
					"tls": schema.StringAttribute{Optional: true,
						MarkdownDescription: "`ldaps` (default, implicit TLS on 636) or `starttls` (upgrade " +
							"on 389). Both are exercised against a real domain, with certificate verification. " +
							"Plain LDAP is deliberately not offered: a simple bind over it sends the " +
							"password in clear text, and a domain with LDAP signing required refuses it anyway. " +
							"Falls back to `AD_LDAP_TLS`."},
					"ca_certificate_file": schema.StringAttribute{Optional: true,
						MarkdownDescription: "PEM file holding the CA that signed the domain controller's " +
							"certificate. Naming one replaces the system trust store rather than adding to it. " +
							"Falls back to `AD_LDAP_CA_CERTIFICATE_FILE`."},
					"insecure_skip_verify": schema.BoolAttribute{Optional: true,
						MarkdownDescription: "Disable certificate verification. For a lab with a self-signed " +
							"DC certificate only — it makes the connection trivially interceptable."},
					"max_concurrency": schema.Int64Attribute{Optional: true,
						MarkdownDescription: "Maximum pooled LDAP connections. Defaults to `4`."},
					"timeout": schema.StringAttribute{Optional: true,
						MarkdownDescription: "Per-operation deadline, as a Go duration (`\"60s\"`). " +
							"Defaults to `90s` — deliberately longer than a resource's own default 60s " +
							"operation budget, so the caller's deadline expires first."},
				},
				Blocks: map[string]schema.Block{
					"simple": schema.SingleNestedBlock{
						MarkdownDescription: "Username and password bind. Safe only because this connection " +
							"is always TLS-protected.",
						Attributes: map[string]schema.Attribute{
							"username": schema.StringAttribute{Optional: true,
								MarkdownDescription: "A UPN (`svc_tf@corp.local`), a DN, or `DOMAIN\\user`. " +
									"Falls back to `AD_LDAP_USERNAME`."},
							"password": schema.StringAttribute{Optional: true, Sensitive: true,
								MarkdownDescription: "Falls back to `AD_LDAP_PASSWORD`."},
						},
					},
					"kerberos": schema.SingleNestedBlock{
						MarkdownDescription: "Bind with a Kerberos ticket. Empty — `kerberos {}` — is the " +
							"intended form: the operator runs `kinit` in their own shell and the ticket is " +
							"read from the cache `KRB5CCNAME` names, so no credential reaches Terraform " +
							"configuration. `KRB5CCNAME` (or `ccache_path`) must be set — the default cache " +
							"location is not searched, so a plain `kinit` with no `KRB5CCNAME` fails with " +
							"\"no Kerberos credential cache\".\n\n" +
							"Only **FILE** credential caches can be read. `KEYRING` and `KCM` — the defaults " +
							"on sssd-managed RHEL, Fedora and Ubuntu — are not readable from Go, so obtain the " +
							"ticket into a file:\n\n" +
							"```sh\nKRB5CCNAME=FILE:/tmp/krb5cc_tf kinit svc_tf@CORP.LOCAL\n```\n\n" +
							"The ticket cache is the Linux and macOS path. `username` with `password` or " +
							"`keytab` is the credential form for a runner where `kinit` was never installed " +
							"at all — CI, a scratch container — and needs no `krb5.conf` (see " +
							"`krb5_conf_path`). Every Kerberos bind carries a `tls-server-end-point` channel-" +
							"binding token, so this connection authenticates against a domain with " +
							"`LdapEnforceChannelBinding` set to `2`, the same as `simple`.",
						Attributes: map[string]schema.Attribute{
							"ccache_path": schema.StringAttribute{Optional: true,
								MarkdownDescription: "Credential cache file. Falls back to the ambient " +
									"`KRB5CCNAME`, but only when this block sets none of `ccache_path`, " +
									"`keytab` or `password` — a credential named here always wins over the " +
									"environment, and `KRB5CCNAME` in turn wins over an ambient " +
									"`AD_LDAP_KEYTAB` or `AD_LDAP_PASSWORD`."},
							"keytab": schema.StringAttribute{Optional: true,
								MarkdownDescription: "Keytab for unattended authentication, for CI with no " +
									"`kinit`. Requires `username`; `realm` defaults from `server`'s domain " +
									"suffix. Conflicts with `ccache_path`. Falls back to `AD_LDAP_KEYTAB`.",
								Validators: []validator.String{
									stringvalidator.ConflictsWith(
										path.MatchRelative().AtParent().AtName("ccache_path"),
									),
								}},
							"password": schema.StringAttribute{Optional: true, Sensitive: true,
								// No AlsoRequires(username) here: that validator reads only req.Config, never
								// the environment-resolved value, and username falls back to AD_LDAP_USERNAME
								// like every other credential attribute in this provider. Enforcing the pairing
								// here would reject a config that sets password in HCL and username via the
								// environment, which is valid and exactly what the lab runners do. Config.Validate
								// sees the resolved values and already enforces this pairing at connect time.
								MarkdownDescription: "Password for an unattended bind where `kinit` was " +
									"never installed — CI, a scratch container. Requires `username`, set in " +
									"configuration or from `AD_LDAP_USERNAME` — unchecked at plan time, since " +
									"the environment fallback means only the resolved value can be judged, so a " +
									"password with no username is refused when the provider connects. " +
									"Conflicts with `keytab` and `ccache_path`. With no `krb5_conf_path` " +
									"and no `/etc/krb5.conf`, a minimal configuration is synthesized " +
									"naming `server` as the KDC. Falls back to `AD_LDAP_PASSWORD`, but only " +
									"when neither `KRB5CCNAME` nor `AD_LDAP_KEYTAB` is set.",
								Validators: []validator.String{
									stringvalidator.ConflictsWith(
										path.MatchRelative().AtParent().AtName("keytab"),
										path.MatchRelative().AtParent().AtName("ccache_path"),
									),
								}},
							"username": schema.StringAttribute{Optional: true,
								MarkdownDescription: "Principal name, with `keytab` or `password`: the account " +
									"name alone (`svc_tf`), with the realm in `realm` — unlike `simple`, not a UPN " +
									"or `DOMAIN\\user`. Not used with a ticket cache, which names its own " +
									"principal. Falls back to `AD_LDAP_USERNAME`."},
							"realm": schema.StringAttribute{Optional: true,
								MarkdownDescription: "Kerberos realm, used with `keytab` or `password`. " +
									"Defaults from `server`'s domain suffix, uppercased, when unset. Falls " +
									"back to `AD_LDAP_REALM`."},
							"krb5_conf_path": schema.StringAttribute{Optional: true,
								MarkdownDescription: "Kerberos configuration file. Falls back to `KRB5_CONFIG`, " +
									"then `/etc/krb5.conf`; with none of them present, a minimal configuration is " +
									"synthesized naming `realm` and `server` as the KDC, for every credential " +
									"source."},
							"spn": schema.StringAttribute{Optional: true,
								MarkdownDescription: "Service principal. Defaults to `ldap/<server>`. Falls back to `AD_LDAP_SPN`."},
						},
					},
					"ntlm": schema.SingleNestedBlock{
						MarkdownDescription: "Bind with NTLM, for a caller that cannot obtain a Kerberos " +
							"ticket — no KDC reachability, no `krb5.conf`, a workgroup runner. It is also " +
							"the Windows client's path, since the Kerberos one reads a `FILE:` credential " +
							"cache Windows does not have.\n\n" +
							"**Known gap:** the LDAP library sends no channel-binding token, so a domain with " +
							"`LdapEnforceChannelBinding` set to `2` rejects this bind even over TLS, with " +
							"`data 80090346` and no mention of channel binding. Use `kerberos`, which sends " +
							"a token, or `simple` there. Verified working against a domain that " +
							"does not enforce it.",
						Attributes: map[string]schema.Attribute{
							"domain": schema.StringAttribute{Optional: true,
								MarkdownDescription: "NetBIOS domain name (`CORP`). Falls back to `AD_LDAP_DOMAIN`."},
							"username": schema.StringAttribute{Optional: true,
								MarkdownDescription: "The account name alone (`svc_tf`), with the domain in " +
									"`domain` — unlike `simple`, not a UPN or `DOMAIN\\user`. Falls back to " +
									"`AD_LDAP_USERNAME`."},
							"password": schema.StringAttribute{Optional: true, Sensitive: true,
								MarkdownDescription: "Falls back to `AD_LDAP_PASSWORD`."},
						},
					},
				},
			},
			"domain": schema.SingleNestedBlock{
				MarkdownDescription: "Domain targeting.",
				Attributes: map[string]schema.Attribute{
					"server": schema.StringAttribute{Optional: true,
						MarkdownDescription: "The domain controller to pin. Omit to discover one " +
							"at configure time. Every cmdlet this provider runs targets it, so a " +
							"write and its read-back cannot land on different replicas. Refused with " +
							"the `ldap` connection, which pins `ldap.server` instead."},
				},
				Blocks: map[string]schema.Block{
					"credential": schema.SingleNestedBlock{
						MarkdownDescription: "Credentials passed to the AD cmdlets. Omit to use " +
							"the transport session's own identity. Refused with the `ldap` connection, " +
							"which authenticates with its own `simple`, `kerberos` or `ntlm` block.",
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
							"passive replication can legitimately take 15 minutes, which presents as a hang. " +
							"Supported on the `ldap` connection, where it is a directory operation rather " +
							"than a cmdlet."},
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

	// A substituted directory is already connected, so connection selection,
	// domain targeting and replication have nothing to resolve.
	if p.directory != nil {
		resp.ResourceData = *p.directory
		resp.DataSourceData = *p.directory
		return
	}

	server, credential, diags := resolveDomain(cfg)
	resp.Diagnostics.Append(diags...)
	replication, diags := resolveReplication(ctx, cfg)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	transport := p.transport
	if transport == nil {
		kind, diags := chooseConnection(cfg)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}

		if kind == connectionLDAP {
			p.configureLDAP(ctx, cfg, server, credential, replication, resp)
			return
		}

		mode, diags := chosenMode(cfg, kind)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}

		// (transport, mode) selects the go-adpwsh constructor: cold is a fresh
		// pwsh per operation, warm a persistent pooled runspace. winrm has only
		// a warm implementation today.
		var err error
		switch kind {
		case transportLocal:
			if mode == modeCold {
				c, d := resolveLocal(cfg, os.Getenv)
				resp.Diagnostics.Append(d...)
				if resp.Diagnostics.HasError() {
					return
				}
				transport, err = adlocal.New(c)
			} else {
				c, d := resolveLocalWarm(cfg, os.Getenv)
				resp.Diagnostics.Append(d...)
				if resp.Diagnostics.HasError() {
					return
				}
				transport, err = adlocalwarm.New(c)
			}
			if err != nil {
				resp.Diagnostics.AddAttributeError(path.Root("local"),
					"Cannot run PowerShell on this machine", transportErrDetail(mode, err))
				return
			}

		case transportSSH:
			if mode == modeCold {
				c, d := resolveSSH(cfg, os.Getenv)
				resp.Diagnostics.Append(d...)
				if resp.Diagnostics.HasError() {
					return
				}
				transport, err = adssh.New(c)
			} else {
				c, d := resolveSSHWarm(cfg, os.Getenv)
				resp.Diagnostics.Append(d...)
				if resp.Diagnostics.HasError() {
					return
				}
				transport, err = adsshwarm.New(c)
			}
			if err != nil {
				resp.Diagnostics.AddAttributeError(path.Root("ssh"),
					"Cannot reach the jump box", transportErrDetail(mode, err))
				return
			}

		case transportWinrm:
			c, d := resolveWinrm(cfg, os.Getenv)
			resp.Diagnostics.Append(d...)
			if resp.Diagnostics.HasError() {
				return
			}
			if mode == modeCold {
				// configuration_name / language_mode are PSRP session-configuration
				// concerns; cold uses the default WinRS shell, so they are a
				// misconfiguration rather than a silent no-op.
				if w := cfg.Winrm; w != nil && len(w.Servers) > 0 {
					resp.Diagnostics.AddAttributeError(path.Root("winrm").AtName("server"),
						"Multiple servers require warm mode",
						"`winrm.server` blocks configure failover across hosts, which winrm "+
							"`mode = \"cold\"` does not support. Use a single `winrm.host` with cold, "+
							"or switch to `mode = \"warm\"`.")
				}
				if w := cfg.Winrm; w != nil {
					if !w.ConfigurationName.IsNull() && w.ConfigurationName.ValueString() != "" {
						resp.Diagnostics.AddAttributeError(path.Root("winrm").AtName("configuration_name"),
							"configuration_name does not apply to winrm cold mode",
							"`configuration_name` selects a PSRP session configuration, which winrm "+
								"`mode = \"cold\"` does not use — cold opens the default Windows Remote "+
								"Shell, not a PSRP endpoint. Remove it, or use `mode = \"warm\"`.")
					}
					if !w.LanguageMode.IsNull() && w.LanguageMode.ValueString() != "" {
						resp.Diagnostics.AddAttributeError(path.Root("winrm").AtName("language_mode"),
							"language_mode does not apply to winrm cold mode",
							"`language_mode` configures a ConstrainedLanguage PSRP endpoint, which "+
								"winrm `mode = \"cold\"` does not use. Remove it, or use `mode = \"warm\"`.")
					}
					if resp.Diagnostics.HasError() {
						return
					}
				}
				// A fresh WinRS shell per op, feeding the wrapped script on stdin to
				// `powershell -EncodedCommand` (no command-size limit). Unlike warm,
				// it needs no server-side PSRP session configuration — for a host
				// where PSRP endpoints are disabled but WinRS is allowed.
				transport, err = adwinrm.NewCold(c)
			} else {
				transport, err = adwinrm.New(c)
			}
			if err != nil {
				detail := transportErrDetail(mode, err)
				if mode == modeCold {
					detail = winrmColdErrDetail(err)
				}
				resp.Diagnostics.AddAttributeError(path.Root("winrm"),
					"Cannot reach the Windows host over WinRM", detail)
				return
			}
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
	dir := client.Directory()
	tflog.Debug(ctx, "activedirectory: configured", map[string]any{
		"server":                 dir.Server,
		"default_naming_context": dir.DNC,
	})

	resp.ResourceData = dir
	resp.DataSourceData = dir
}

func (p *adProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		newOUResource,
		newGroupResource,
		newUserResource,
		newGMSAResource,
		newComputerResource,
		newGroupMemberResource,
		newGroupMembershipResource,
		newAccessRuleResource,
	}
}

func (p *adProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		newOUDataSource,
		newGroupDataSource,
		newUserDataSource,
		newGMSADataSource,
		newComputerDataSource,
		newGroupMembersDataSource,
		newUsersDataSource,
		newGroupsDataSource,
		newComputersDataSource,
		newOUsDataSource,
		newDelegationTemplateDataSource,
	}
}

// clientFromProviderData is the boilerplate every resource's Configure runs.
//
// It hands back an adcore.Directory rather than a concrete client so that a
// resource cannot tell which backend configured it — which is what lets one
// set of resources serve both. The zero value is returned before the provider
// is configured; a resource checks for that with client.OU == nil, since a
// struct is never nil.
func clientFromProviderData(data any, diags *diag.Diagnostics) adcore.Directory {
	if data == nil {
		return adcore.Directory{} // Configure runs before the provider is configured; not an error.
	}
	dir, ok := data.(adcore.Directory)
	if !ok {
		diags.AddError("Unexpected provider data",
			fmt.Sprintf("Expected adcore.Directory, got %T. This is a bug in the provider.", data))
		return adcore.Directory{}
	}
	return dir
}

// transportErrDetail frames a transport construction failure. For warm mode it
// leads with the PowerShell 7 requirement that cold mode does not have, since a
// missing pwsh 7 (or, over ssh, a missing `powershell` subsystem) is the most
// likely cause of a warm-transport failure.
func transportErrDetail(mode executionMode, err error) string {
	base := "This is a transport problem, not an Active Directory one.\n\n" + err.Error()
	if mode == modeWarm {
		return "Warm mode needs PowerShell 7 (and, for ssh, the `powershell` sshd subsystem). " +
			"If the target only has Windows PowerShell 5.1, set `mode = \"cold\"`.\n\n" + base
	}
	return base
}

// winrmColdErrDetail frames a winrm+cold construction/connection failure. The
// two identities are separate for cold: the `winrm { user }` TRANSPORT account
// must have WinRS shell access (member of Remote Management Users, or an admin,
// and present in the WSMan service RootSDDL), which is a different grant than the
// PSRP session-configuration SDDL warm relies on. A WSMan "access denied" here
// is almost always that transport-side grant — NOT a `domain.credential`
// problem, which is the AD-cmdlet identity delivered in the payload and consumed
// only after the shell exists.
func winrmColdErrDetail(err error) string {
	return "This is a transport problem, not an Active Directory one.\n\n" +
		"winrm `mode = \"cold\"` opens a Windows Remote Shell as the `winrm` block's " +
		"`user`. That account needs WinRS shell access on the target — membership in " +
		"`Remote Management Users` (or local Administrators), and inclusion in the WSMan " +
		"service RootSDDL. An \"access denied\" is that grant, not a `domain.credential` " +
		"issue (the AD identity is delivered separately, in the payload).\n\n" + err.Error()
}

// defaultOperationTimeout is the per-CRUD-operation deadline withTimeout
// applies when a resource's own `timeouts` block leaves that operation unset.
// config.go's defaultTransportTimeout is built from this value — see the
// comment there for why the two must stay in that order.
const defaultOperationTimeout = 60 * time.Second

// withTimeout applies a resource's timeouts block, defaulting to
// defaultOperationTimeout.
func withTimeout(ctx context.Context, v func(context.Context, time.Duration) (time.Duration, diag.Diagnostics)) (context.Context, context.CancelFunc, diag.Diagnostics) {
	d, diags := v(ctx, defaultOperationTimeout)
	if diags.HasError() {
		return ctx, func() {}, diags
	}
	c, cancel := context.WithTimeout(ctx, d)
	return c, cancel, diags
}

// configureLDAP builds the native-LDAP directory. It shares nothing with the
// PowerShell path: no transport, no execution mode, and no credential from the
// `domain` block — the `ldap` block carries its own authentication.
func (p *adProvider) configureLDAP(ctx context.Context, cfg providerModel, server string, credential *adpwsh.Credential, replication adpwsh.ReplicationConfig, resp *provider.ConfigureResponse) {
	if server != "" {
		resp.Diagnostics.AddAttributeError(path.Root("domain").AtName("server"),
			"domain.server does not apply to the ldap connection",
			"`domain.server` is the domain controller the PowerShell cmdlets target. The `ldap` "+
				"block connects to `ldap.server` and pins it for the provider's lifetime, so a "+
				"server here would be silently ignored.\n\n"+
				"Move the value into `ldap.server`, or remove it.")
	}
	if credential != nil {
		resp.Diagnostics.AddAttributeError(path.Root("domain").AtName("credential"),
			"domain.credential does not apply to the ldap connection",
			"`domain.credential` is the identity the PowerShell cmdlets run as. The `ldap` "+
				"block authenticates itself — with `simple`, `kerberos` or `ntlm` — so a "+
				"credential here would be silently ignored.\n\n"+
				"Move the credential into `ldap.simple`, or remove it and use `ldap.kerberos {}`.")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	lcfg := resolveLDAP(*cfg.LDAP, os.Getenv, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	lcfg.Log = tflogLogger{}
	lcfg.Replication = adldap.ReplicationConfig{
		Wait:         replication.Wait,
		Targets:      replication.Targets,
		ForceSync:    replication.ForceSync,
		Timeout:      replication.Timeout,
		PollInterval: replication.PollInterval,
	}

	client, err := adldap.New(ctx, lcfg)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("ldap"),
			"Cannot configure the Active Directory client",
			"The provider could not connect to the domain controller over LDAP. "+
				"Check that TCP "+ldapPortHint(lcfg)+" is open to it, that its certificate is "+
				"trusted (see `ca_certificate_file`), and that the credential or ticket is "+
				"valid.\n\n"+err.Error())
		return
	}

	dir := client.Directory()
	tflog.Debug(ctx, "activedirectory: configured", map[string]any{
		"connection":             "ldap",
		"server":                 dir.Server,
		"default_naming_context": dir.DNC,
	})

	resp.ResourceData = dir
	resp.DataSourceData = dir
}

// ldapPortHint names the port actually in use, so the diagnostic does not send
// an operator to check 636 when they configured StartTLS on 389.
func ldapPortHint(cfg adldap.Config) string {
	port := cfg.Port
	if port == 0 {
		port = adldap.DefaultPort(cfg.TLS)
	}
	return strconv.Itoa(port)
}
