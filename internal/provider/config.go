package provider

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	adpwsh "github.com/nemethhh/go-adpwsh"
	adssh "github.com/nemethhh/go-adpwsh/transport/ssh"
)

type providerModel struct {
	PwshPath    types.String      `tfsdk:"pwsh_path"`
	SSH         *sshModel         `tfsdk:"ssh"`
	Domain      *domainModel      `tfsdk:"domain"`
	Replication *replicationModel `tfsdk:"replication"`
}

type sshModel struct {
	Host                  types.String `tfsdk:"host"`
	Port                  types.Int64  `tfsdk:"port"`
	User                  types.String `tfsdk:"user"`
	PrivateKey            types.String `tfsdk:"private_key"`
	PrivateKeyPath        types.String `tfsdk:"private_key_path"`
	Password              types.String `tfsdk:"password"`
	UseAgent              types.Bool   `tfsdk:"use_agent"`
	KnownHostsFile        types.String `tfsdk:"known_hosts_file"`
	HostKey               types.String `tfsdk:"host_key"`
	InsecureIgnoreHostKey types.Bool   `tfsdk:"insecure_ignore_host_key"`
	MaxConcurrency        types.Int64  `tfsdk:"max_concurrency"`
	Timeout               types.String `tfsdk:"timeout"`
}

type domainModel struct {
	Server     types.String     `tfsdk:"server"`
	Credential *credentialModel `tfsdk:"credential"`
}

type credentialModel struct {
	Username types.String `tfsdk:"username"`
	Password types.String `tfsdk:"password"`
}

type replicationModel struct {
	Wait         types.Bool   `tfsdk:"wait"`
	Targets      types.List   `tfsdk:"targets"`
	ForceSync    types.Bool   `tfsdk:"force_sync"`
	Timeout      types.String `tfsdk:"timeout"`
	PollInterval types.String `tfsdk:"poll_interval"`
}

// str resolves a string attribute, falling back to an environment variable.
// Configuration always wins; the environment is the fallback, not an override.
func str(v types.String, getenv func(string) string, envVar string) string {
	if !v.IsNull() && !v.IsUnknown() {
		return v.ValueString()
	}
	if envVar == "" {
		return ""
	}
	return getenv(envVar)
}

func boolOr(v types.Bool, def bool) bool {
	if v.IsNull() || v.IsUnknown() {
		return def
	}
	return v.ValueBool()
}

func duration(v types.String, p path.Path, def time.Duration, diags *diag.Diagnostics) time.Duration {
	if v.IsNull() || v.IsUnknown() || v.ValueString() == "" {
		return def
	}
	d, err := time.ParseDuration(v.ValueString())
	if err != nil {
		diags.AddAttributeError(p, "Invalid duration",
			fmt.Sprintf("%q is not a Go duration such as \"60s\" or \"2m\": %s", v.ValueString(), err))
		return def
	}
	return d
}

// resolveSSH turns the ssh block plus the environment into a transport
// configuration, enforcing the two precedence rules with attribute-scoped
// diagnostics.
func resolveSSH(m providerModel, getenv func(string) string) (adssh.Config, diag.Diagnostics) {
	var diags diag.Diagnostics
	root := path.Root("ssh")
	s := sshModel{}
	if m.SSH != nil {
		s = *m.SSH
	}

	cfg := adssh.Config{
		Host:                  str(s.Host, getenv, "AD_SSH_HOST"),
		User:                  str(s.User, getenv, "AD_SSH_USER"),
		PrivateKeyPEM:         str(s.PrivateKey, getenv, "AD_SSH_PRIVATE_KEY"),
		PrivateKeyPath:        str(s.PrivateKeyPath, getenv, "AD_SSH_PRIVATE_KEY_PATH"),
		Password:              str(s.Password, getenv, "AD_SSH_PASSWORD"),
		UseAgent:              boolOr(s.UseAgent, false),
		KnownHostsFile:        str(s.KnownHostsFile, nil, ""),
		HostKey:               str(s.HostKey, nil, ""),
		InsecureIgnoreHostKey: boolOr(s.InsecureIgnoreHostKey, false),
		PwshPath:              str(m.PwshPath, nil, ""),
		Timeout:               duration(s.Timeout, root.AtName("timeout"), 60*time.Second, &diags),
	}
	if !s.Port.IsNull() && !s.Port.IsUnknown() {
		cfg.Port = int(s.Port.ValueInt64())
	} else if p := getenv("AD_SSH_PORT"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			diags.AddAttributeError(root.AtName("port"), "Invalid AD_SSH_PORT",
				fmt.Sprintf("%q is not a port number: %s", p, err))
		}
		cfg.Port = n
	}
	if !s.MaxConcurrency.IsNull() && !s.MaxConcurrency.IsUnknown() {
		cfg.Concurrency = int(s.MaxConcurrency.ValueInt64())
	}
	cfg = cfg.WithDefaults()

	// The library owns both precedence rules; the provider's job is to render
	// the refusal against the attribute the user can actually change.
	if err := cfg.Validate(); err != nil {
		diags.AddAttributeError(root, "Invalid SSH configuration", err.Error())
	}
	if cfg.Host == "" {
		diags.AddAttributeError(root.AtName("host"), "Missing SSH host",
			"Set ssh.host or the AD_SSH_HOST environment variable.")
	}
	return cfg, diags
}

// resolveDomain returns the pinned DC and the optional -Credential.
func resolveDomain(m providerModel) (string, *adpwsh.Credential, diag.Diagnostics) {
	var diags diag.Diagnostics
	if m.Domain == nil {
		return "", nil, diags
	}
	server := str(m.Domain.Server, nil, "")
	if m.Domain.Credential == nil {
		return server, nil, diags
	}
	c := m.Domain.Credential
	user, pass := str(c.Username, nil, ""), str(c.Password, nil, "")
	switch {
	case user == "" && pass == "":
		return server, nil, diags
	case user == "" || pass == "":
		diags.AddAttributeError(path.Root("domain").AtName("credential"),
			"Incomplete credential",
			"domain.credential requires both username and password, or neither. "+
				"Omit the block entirely to use the SSH session's own identity.")
		return server, nil, diags
	}
	return server, &adpwsh.Credential{Username: user, Password: adpwsh.NewSecret(pass)}, diags
}

// resolveReplication turns the replication block into the library's config.
func resolveReplication(ctx context.Context, m providerModel) (adpwsh.ReplicationConfig, diag.Diagnostics) {
	var diags diag.Diagnostics
	if m.Replication == nil {
		return adpwsh.ReplicationConfig{}, diags
	}
	r := m.Replication
	root := path.Root("replication")

	cfg := adpwsh.ReplicationConfig{
		Wait:         boolOr(r.Wait, false),
		ForceSync:    boolOr(r.ForceSync, true),
		Timeout:      duration(r.Timeout, root.AtName("timeout"), 60*time.Second, &diags),
		PollInterval: duration(r.PollInterval, root.AtName("poll_interval"), 2*time.Second, &diags),
	}
	if !r.Targets.IsNull() && !r.Targets.IsUnknown() {
		diags.Append(r.Targets.ElementsAs(ctx, &cfg.Targets, false)...)
	}
	if cfg.Wait && len(cfg.Targets) == 0 {
		diags.AddAttributeError(root.AtName("targets"), "Replication wait needs targets",
			`Set replication.targets to the domain controllers to wait for, or to ["all"].`)
	}
	return cfg, diags
}
