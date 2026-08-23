package provider

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	adpwsh "github.com/nemethhh/go-adpwsh"
	adlocal "github.com/nemethhh/go-adpwsh/transport/local"
	adpsrp "github.com/nemethhh/go-adpwsh/transport/psrp"
	adssh "github.com/nemethhh/go-adpwsh/transport/ssh"
)

type providerModel struct {
	PwshPath    types.String      `tfsdk:"pwsh_path"`
	Local       *localModel       `tfsdk:"local"`
	SSH         *sshModel         `tfsdk:"ssh"`
	PSRP        *psrpModel        `tfsdk:"psrp"`
	Domain      *domainModel      `tfsdk:"domain"`
	Replication *replicationModel `tfsdk:"replication"`
}

type localModel struct {
	PwshPath       types.String `tfsdk:"pwsh_path"`
	MaxConcurrency types.Int64  `tfsdk:"max_concurrency"`
	Timeout        types.String `tfsdk:"timeout"`
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

type psrpModel struct {
	Host               types.String `tfsdk:"host"`
	Port               types.Int64  `tfsdk:"port"`
	UseTLS             types.Bool   `tfsdk:"use_tls"`
	InsecureSkipVerify types.Bool   `tfsdk:"insecure_skip_verify"`
	User               types.String `tfsdk:"user"`
	Password           types.String `tfsdk:"password"`
	Domain             types.String `tfsdk:"domain"`
	SPN                types.String `tfsdk:"spn"`
	Realm              types.String `tfsdk:"realm"`
	Krb5ConfPath       types.String `tfsdk:"krb5_conf_path"`
	CCachePath         types.String `tfsdk:"ccache_path"`
	KeytabPath         types.String `tfsdk:"keytab_path"`
	ConfigurationName  types.String `tfsdk:"configuration_name"`
	MaxConcurrency     types.Int64  `tfsdk:"max_concurrency"`
	Timeout            types.String `tfsdk:"timeout"`
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

// boolWithEnv resolves a bool attribute with an environment fallback.
// Configuration wins; the environment is the fallback.
func boolWithEnv(v types.Bool, getenv func(string) string, envVar string, def bool) bool {
	if !v.IsNull() && !v.IsUnknown() {
		return v.ValueBool()
	}
	switch strings.ToLower(getenv(envVar)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

// resolvePSRP turns the psrp block plus the environment into a transport
// configuration, mirroring resolveSSH: configuration always wins, and the
// refusal is rendered against the attribute the user can change.
func resolvePSRP(m providerModel, getenv func(string) string) (adpsrp.Config, diag.Diagnostics) {
	var diags diag.Diagnostics
	root := path.Root("psrp")
	s := psrpModel{}
	if m.PSRP != nil {
		s = *m.PSRP
	}

	cfg := adpsrp.Config{
		Host:               str(s.Host, getenv, "AD_PSRP_HOST"),
		UseTLS:             boolWithEnv(s.UseTLS, getenv, "AD_PSRP_USE_TLS", false),
		InsecureSkipVerify: boolWithEnv(s.InsecureSkipVerify, getenv, "AD_PSRP_INSECURE_SKIP_VERIFY", false),
		Username:           str(s.User, getenv, "AD_PSRP_USER"),
		Password:           str(s.Password, getenv, "AD_PSRP_PASSWORD"),
		Domain:             str(s.Domain, getenv, "AD_PSRP_DOMAIN"),
		SPN:                str(s.SPN, getenv, "AD_PSRP_SPN"),
		Realm:              str(s.Realm, getenv, "AD_PSRP_REALM"),
		Krb5ConfPath:       firstNonEmpty(str(s.Krb5ConfPath, getenv, "AD_PSRP_KRB5_CONF"), getenv("KRB5_CONFIG")),
		CCachePath:         firstNonEmpty(str(s.CCachePath, getenv, "AD_PSRP_CCACHE"), strings.TrimPrefix(getenv("KRB5CCNAME"), "FILE:")),
		KeytabPath:         str(s.KeytabPath, getenv, "AD_PSRP_KEYTAB"),
		ConfigurationName:  str(s.ConfigurationName, getenv, "AD_PSRP_CONFIGURATION_NAME"),
		Timeout:            duration(s.Timeout, root.AtName("timeout"), 60*time.Second, &diags),
	}

	if !s.Port.IsNull() && !s.Port.IsUnknown() {
		cfg.Port = int(s.Port.ValueInt64())
	} else if p := getenv("AD_PSRP_PORT"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			diags.AddAttributeError(root.AtName("port"), "Invalid AD_PSRP_PORT",
				fmt.Sprintf("%q is not a port number: %s", p, err))
		}
		cfg.Port = n
	}

	if !s.MaxConcurrency.IsNull() && !s.MaxConcurrency.IsUnknown() {
		cfg.Concurrency = int(s.MaxConcurrency.ValueInt64())
	} else if v := getenv("AD_PSRP_MAX_CONCURRENCY"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			diags.AddAttributeError(root.AtName("max_concurrency"), "Invalid AD_PSRP_MAX_CONCURRENCY",
				fmt.Sprintf("%q is not a whole number: %s", v, err))
		}
		cfg.Concurrency = n
	}

	// Validate the raw config (catches a negative concurrency) before defaults.
	if err := cfg.Validate(); err != nil {
		diags.AddAttributeError(root, "Invalid PSRP configuration", err.Error())
	}
	cfg = cfg.WithDefaults()

	if cfg.Host == "" {
		diags.AddAttributeError(root.AtName("host"), "Missing PSRP host",
			"Set psrp.host or the AD_PSRP_HOST environment variable.")
	}
	return cfg, diags
}

// firstNonEmpty returns the first value that is not empty. Configuration always
// wins; the environment is the fallback, never an override.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// resolveLocal turns the local block plus the environment into a transport
// configuration. Unlike resolveSSH there is nothing to dial and no credential
// precedence to enforce: the process inherits the identity of whoever launched
// Terraform, which is the whole point of running on the host.
func resolveLocal(m providerModel, getenv func(string) string) (adlocal.Config, diag.Diagnostics) {
	var diags diag.Diagnostics
	root := path.Root("local")
	l := localModel{}
	if m.Local != nil {
		l = *m.Local
	}

	var cfg adlocal.Config

	// One path per transport: the local block's own attribute, then the
	// top-level pwsh_path the SSH transport already uses, then the environment.
	cfg.PwshPath = firstNonEmpty(
		str(l.PwshPath, nil, ""),
		str(m.PwshPath, nil, ""),
		getenv("AD_PWSH_PATH"),
	)

	if !l.MaxConcurrency.IsNull() && !l.MaxConcurrency.IsUnknown() {
		cfg.Concurrency = int(l.MaxConcurrency.ValueInt64())
	} else if v := getenv("AD_LOCAL_MAX_CONCURRENCY"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			diags.AddAttributeError(root.AtName("max_concurrency"),
				"Invalid AD_LOCAL_MAX_CONCURRENCY",
				fmt.Sprintf("%q is not a whole number: %s", v, err))
		}
		cfg.Concurrency = n
	}

	// The environment supplies the default the attribute falls back to, so
	// configuration still wins and a malformed variable is still reported.
	timeoutDefault := 60 * time.Second
	if v := getenv("AD_LOCAL_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			diags.AddAttributeError(root.AtName("timeout"), "Invalid AD_LOCAL_TIMEOUT",
				fmt.Sprintf("%q is not a Go duration such as \"60s\" or \"2m\": %s", v, err))
		} else {
			timeoutDefault = d
		}
	}
	cfg.Timeout = duration(l.Timeout, root.AtName("timeout"), timeoutDefault, &diags)

	// Validate before WithDefaults, exactly as the library's own New does:
	// afterwards a negative concurrency has already become 4 and the mistake is
	// invisible.
	if err := cfg.Validate(); err != nil {
		diags.AddAttributeError(root, "Invalid local configuration", err.Error())
	}
	return cfg.WithDefaults(), diags
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

// transportKind is which of the two mutually exclusive transport blocks the
// configuration selects.
type transportKind int

const (
	transportUnset transportKind = iota
	transportLocal
	transportSSH
)

func (k transportKind) String() string {
	switch k {
	case transportLocal:
		return "local"
	case transportSSH:
		return "ssh"
	default:
		return "unset"
	}
}

// chooseTransport enforces the exactly-one rule. There is deliberately no
// implicit default: defaulting to local when the block is absent turns a typo'd
// `ssh` block into silent local execution against the wrong identity, and
// defaulting to ssh turns a typo'd `local` block into a dial to nowhere.
func chooseTransport(m providerModel) (transportKind, diag.Diagnostics) {
	var diags diag.Diagnostics

	const summary = "Exactly one transport block is required"
	const detail = "Set either a `local` block, to run pwsh on the machine Terraform runs on, " +
		"or an `ssh` block, to run it on a Windows jump box — not both, and not neither.\n\n" +
		"There is no implicit default. Guessing one would let a mistyped block run against the " +
		"wrong identity: a typo in `ssh` would silently execute locally as whoever launched " +
		"Terraform."

	switch {
	case m.Local != nil && m.SSH != nil:
		// One diagnostic per block, so Terraform underlines both lines.
		diags.AddAttributeError(path.Root("local"), summary, detail)
		diags.AddAttributeError(path.Root("ssh"), summary, detail)
		return transportUnset, diags
	case m.Local != nil:
		return transportLocal, diags
	case m.SSH != nil:
		return transportSSH, diags
	default:
		// Nothing was written, so there is no attribute to point at.
		diags.AddError(summary, detail)
		return transportUnset, diags
	}
}
