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

	"github.com/nemethhh/go-adcore"
	adldap "github.com/nemethhh/go-adldap"
	adpwsh "github.com/nemethhh/go-adpwsh"
	adlocal "github.com/nemethhh/go-adpwsh/transport/local"
	adlocalwarm "github.com/nemethhh/go-adpwsh/transport/localwarm"
	adssh "github.com/nemethhh/go-adpwsh/transport/ssh"
	adsshwarm "github.com/nemethhh/go-adpwsh/transport/sshwarm"
	adwinrm "github.com/nemethhh/go-adpwsh/transport/winrm"
)

type providerModel struct {
	PwshPath    types.String      `tfsdk:"pwsh_path"`
	Local       *localModel       `tfsdk:"local"`
	SSH         *sshModel         `tfsdk:"ssh"`
	Winrm       *winrmModel       `tfsdk:"winrm"`
	LDAP        *ldapModel        `tfsdk:"ldap"`
	Domain      *domainModel      `tfsdk:"domain"`
	Replication *replicationModel `tfsdk:"replication"`
}

type localModel struct {
	PwshPath       types.String `tfsdk:"pwsh_path"`
	MaxConcurrency types.Int64  `tfsdk:"max_concurrency"`
	Timeout        types.String `tfsdk:"timeout"`
	Mode           types.String `tfsdk:"mode"`
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
	Mode                  types.String `tfsdk:"mode"`
}

type winrmModel struct {
	Host               types.String       `tfsdk:"host"`
	Port               types.Int64        `tfsdk:"port"`
	UseTLS             types.Bool         `tfsdk:"use_tls"`
	InsecureSkipVerify types.Bool         `tfsdk:"insecure_skip_verify"`
	User               types.String       `tfsdk:"user"`
	Password           types.String       `tfsdk:"password"`
	Domain             types.String       `tfsdk:"domain"`
	SPN                types.String       `tfsdk:"spn"`
	Realm              types.String       `tfsdk:"realm"`
	Krb5ConfPath       types.String       `tfsdk:"krb5_conf_path"`
	CCachePath         types.String       `tfsdk:"ccache_path"`
	KeytabPath         types.String       `tfsdk:"keytab_path"`
	ConfigurationName  types.String       `tfsdk:"configuration_name"`
	LanguageMode       types.String       `tfsdk:"language_mode"`
	MaxConcurrency     types.Int64        `tfsdk:"max_concurrency"`
	Timeout            types.String       `tfsdk:"timeout"`
	Mode               types.String       `tfsdk:"mode"`
	ServerSelection    types.String       `tfsdk:"server_selection"`
	Servers            []winrmServerModel `tfsdk:"server"`
}

// winrmServerModel is one repeatable `server{}` sub-block: a host in the
// ordered failover list. Only addressing varies per host; every other
// setting (auth/TLS/Kerberos/configuration_name/language_mode/mode/
// server_selection/max_concurrency/timeout) stays on the shared winrm block.
type winrmServerModel struct {
	Host types.String `tfsdk:"host"`
	Port types.Int64  `tfsdk:"port"`
	SPN  types.String `tfsdk:"spn"`
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

// defaultTransportTimeout is the per-operation deadline handed to a transport
// (local/ssh/winrm) when its own `timeout` attribute is left unset — that is,
// only when BOTH the operation's own deadline (defaultOperationTimeout,
// provider.go) and the transport's are defaulted. It must stay strictly
// longer than defaultOperationTimeout: the caller's context expiring aborts
// the retry loop safely, but a transport-internal deadline is classified
// transient and can cause the whole operation to be re-issued — potentially
// after it already reached Active Directory. Making the caller's deadline
// win the race turns that into a safety ordering rather than a race. This is
// not a tuning knob and never changes what a user sees: an explicit
// `timeouts` block on the resource, or an explicit `timeout` on the
// transport block, always overrides its own default and is used as-is.
const defaultTransportTimeout = defaultOperationTimeout + 30*time.Second

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
		Timeout:               duration(s.Timeout, root.AtName("timeout"), defaultTransportTimeout, &diags),
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

// resolveWinrm turns the winrm block plus the environment into a transport
// configuration, mirroring resolveSSH: configuration always wins, and the
// refusal is rendered against the attribute the user can change.
func resolveWinrm(m providerModel, getenv func(string) string) (adwinrm.Config, diag.Diagnostics) {
	var diags diag.Diagnostics
	root := path.Root("winrm")
	s := winrmModel{}
	if m.Winrm != nil {
		s = *m.Winrm
	}

	cfg := adwinrm.Config{
		Host:               str(s.Host, getenv, "AD_WINRM_HOST"),
		UseTLS:             boolWithEnv(s.UseTLS, getenv, "AD_WINRM_USE_TLS", false),
		InsecureSkipVerify: boolWithEnv(s.InsecureSkipVerify, getenv, "AD_WINRM_INSECURE_SKIP_VERIFY", false),
		Username:           str(s.User, getenv, "AD_WINRM_USER"),
		Password:           str(s.Password, getenv, "AD_WINRM_PASSWORD"),
		Domain:             str(s.Domain, getenv, "AD_WINRM_DOMAIN"),
		SPN:                str(s.SPN, getenv, "AD_WINRM_SPN"),
		Realm:              str(s.Realm, getenv, "AD_WINRM_REALM"),
		Krb5ConfPath:       firstNonEmpty(str(s.Krb5ConfPath, getenv, "AD_WINRM_KRB5_CONF"), getenv("KRB5_CONFIG")),
		CCachePath:         firstNonEmpty(str(s.CCachePath, getenv, "AD_WINRM_CCACHE"), strings.TrimPrefix(getenv("KRB5CCNAME"), "FILE:")),
		KeytabPath:         str(s.KeytabPath, getenv, "AD_WINRM_KEYTAB"),
		ConfigurationName:  str(s.ConfigurationName, getenv, "AD_WINRM_CONFIGURATION_NAME"),
		LanguageMode:       str(s.LanguageMode, getenv, "AD_WINRM_LANGUAGE_MODE"),
		Timeout:            duration(s.Timeout, root.AtName("timeout"), defaultTransportTimeout, &diags),
	}

	if !s.Port.IsNull() && !s.Port.IsUnknown() {
		cfg.Port = int(s.Port.ValueInt64())
	} else if p := getenv("AD_WINRM_PORT"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			diags.AddAttributeError(root.AtName("port"), "Invalid AD_WINRM_PORT",
				fmt.Sprintf("%q is not a port number: %s", p, err))
		}
		cfg.Port = n
	}

	if !s.MaxConcurrency.IsNull() && !s.MaxConcurrency.IsUnknown() {
		cfg.Concurrency = int(s.MaxConcurrency.ValueInt64())
	} else if v := getenv("AD_WINRM_MAX_CONCURRENCY"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			diags.AddAttributeError(root.AtName("max_concurrency"), "Invalid AD_WINRM_MAX_CONCURRENCY",
				fmt.Sprintf("%q is not a whole number: %s", v, err))
		}
		cfg.Concurrency = n
	}

	// Repeatable server{} blocks become the ordered failover list. Mutually
	// exclusive with the single winrm-level host; per-block host is required.
	if len(s.Servers) > 0 {
		if v := str(s.Host, nil, ""); v != "" {
			diags.AddAttributeError(root.AtName("server"), "Set host or server blocks, not both",
				"Use either a single `winrm.host` or one-or-more `winrm.server` blocks — not both.")
		}
		eps := make([]adwinrm.Endpoint, 0, len(s.Servers))
		for i, sv := range s.Servers {
			host := str(sv.Host, nil, "")
			if host == "" {
				diags.AddAttributeError(root.AtName("server").AtListIndex(i).AtName("host"),
					"Missing server host", "Each `winrm.server` block requires a `host`.")
				continue
			}
			ep := adwinrm.Endpoint{Host: host, SPN: str(sv.SPN, nil, "")}
			if !sv.Port.IsNull() && !sv.Port.IsUnknown() {
				ep.Port = int(sv.Port.ValueInt64())
			}
			eps = append(eps, ep)
		}
		cfg.Endpoints = eps
		cfg.Host = "" // the list is authoritative
	}

	// server_selection maps to the library's connect-time ordering. The schema's
	// OneOf validator (provider.go) already rejects any other value, so an
	// unrecognised string never reaches here; default covers "" and "failover".
	switch str(s.ServerSelection, nil, "") {
	case "round_robin":
		cfg.Strategy = adwinrm.StrategyRoundRobin
	default:
		cfg.Strategy = adwinrm.StrategyFailover
	}

	// Validate the raw config (catches a negative concurrency) before defaults.
	if err := cfg.Validate(); err != nil {
		diags.AddAttributeError(root, "Invalid WinRM configuration", err.Error())
	}
	cfg = cfg.WithDefaults()

	if len(cfg.Endpoints) == 0 && cfg.Host == "" {
		diags.AddAttributeError(root.AtName("host"), "Missing WinRM host",
			"Set winrm.host, one-or-more winrm.server blocks, or the AD_WINRM_HOST environment variable.")
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
	timeoutDefault := defaultTransportTimeout
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

// resolveLocalWarm reads the local block into the local+warm transport config.
// It reuses resolveLocal's pwsh_path/concurrency/timeout resolution — same env
// fallbacks, same validation — and carries the shared fields across;
// adlocalwarm.New fills the warm-only ReapAfter/ReadTimeout defaults.
func resolveLocalWarm(m providerModel, getenv func(string) string) (adlocalwarm.Config, diag.Diagnostics) {
	cold, diags := resolveLocal(m, getenv)
	return adlocalwarm.Config{
		PwshPath:    cold.PwshPath,
		Concurrency: cold.Concurrency,
		Timeout:     cold.Timeout,
	}, diags
}

// resolveSSHWarm reads the ssh block into the ssh+warm transport config. The
// jump-box connection, auth and host-key handling are the ssh transport's,
// embedded verbatim; adsshwarm.New fills the `powershell` subsystem name and
// the warm-pool timings.
func resolveSSHWarm(m providerModel, getenv func(string) string) (adsshwarm.Config, diag.Diagnostics) {
	sshCfg, diags := resolveSSH(m, getenv)
	return adsshwarm.Config{SSH: sshCfg}, diags
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
				"Omit the block entirely to use the transport session's own identity.")
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

// connectionKind is which of the four mutually exclusive connection blocks the
// configuration selects.
//
// Three of them are PowerShell transports — local, ssh, winrm — and differ only
// in how pwsh is reached. connectionLDAP is not a transport: it runs no
// PowerShell at all and speaks LDAP to a domain controller directly, so the
// mode axis (warm/cold) does not apply to it.
type connectionKind int

const (
	transportUnset connectionKind = iota
	transportLocal
	transportSSH
	transportWinrm
	connectionLDAP
)

func (k connectionKind) String() string {
	switch k {
	case transportLocal:
		return "local"
	case transportSSH:
		return "ssh"
	case transportWinrm:
		return "winrm"
	case connectionLDAP:
		return "ldap"
	default:
		return "unset"
	}
}

// executionMode is how pwsh is driven once a transport channel exists: cold runs
// one `pwsh -EncodedCommand` per operation (re-importing the AD module every
// time); warm keeps a persistent PSRP runspace so startup and the module import
// are paid once per pooled shell and amortized. It is orthogonal to the
// transport (local/ssh/winrm) — a third, independent axis alongside transport
// and the `domain` AD-identity block.
type executionMode int

const (
	modeWarm executionMode = iota // default: persistent PSRP runspace
	modeCold                      // one-shot pwsh -EncodedCommand per op
)

func (e executionMode) String() string {
	if e == modeCold {
		return "cold"
	}
	return "warm"
}

// chosenMode reads the selected transport block's `mode` attribute, defaulting
// to warm (the fast path). An unrecognised value is refused against the mode
// attribute rather than silently treated as warm.
func chosenMode(m providerModel, kind connectionKind) (executionMode, diag.Diagnostics) {
	var diags diag.Diagnostics
	var raw types.String
	var root path.Path
	switch kind {
	case transportLocal:
		if m.Local != nil {
			raw = m.Local.Mode
		}
		root = path.Root("local")
	case transportSSH:
		if m.SSH != nil {
			raw = m.SSH.Mode
		}
		root = path.Root("ssh")
	case transportWinrm:
		if m.Winrm != nil {
			raw = m.Winrm.Mode
		}
		root = path.Root("winrm")
	}
	switch strings.ToLower(str(raw, nil, "")) {
	case "", "warm":
		return modeWarm, diags
	case "cold":
		return modeCold, diags
	default:
		diags.AddAttributeError(root.AtName("mode"), "Invalid execution mode",
			fmt.Sprintf("%q is not a valid mode; use \"warm\" (default) or \"cold\".", raw.ValueString()))
		return modeWarm, diags
	}
}

// chooseConnection enforces the exactly-one rule. There is deliberately no
// implicit default: defaulting to local when the block is absent turns a typo'd
// `ssh` block into silent local execution against the wrong identity, and
// defaulting to ssh or winrm turns a typo'd `local` block into a dial to
// nowhere.
func chooseConnection(m providerModel) (connectionKind, diag.Diagnostics) {
	var diags diag.Diagnostics

	const summary = "Exactly one connection block is required"
	const detail = "Set exactly one of `local` (run pwsh where Terraform runs), " +
		"`ssh` (a Windows jump box), `winrm` (WinRM/PSRP), or `ldap` (connect to a " +
		"domain controller directly over LDAPS, with no PowerShell) — not more than " +
		"one, and not none.\n\n" +
		"There is no implicit default. Guessing one would let a mistyped block run " +
		"against the wrong identity."

	blocks := []struct {
		present bool
		name    string
		kind    connectionKind
	}{
		{m.Local != nil, "local", transportLocal},
		{m.SSH != nil, "ssh", transportSSH},
		{m.Winrm != nil, "winrm", transportWinrm},
		{m.LDAP != nil, "ldap", connectionLDAP},
	}

	chosen := transportUnset
	count := 0
	for _, b := range blocks {
		if b.present {
			count++
			chosen = b.kind
		}
	}

	switch count {
	case 1:
		return chosen, diags
	case 0:
		diags.AddError(summary, detail)
		return transportUnset, diags
	default:
		// One diagnostic per offending block, so Terraform underlines each.
		for _, b := range blocks {
			if b.present {
				diags.AddAttributeError(path.Root(b.name), summary, detail)
			}
		}
		return transportUnset, diags
	}
}

// ldapModel is the `ldap` connection block: a direct LDAPS connection to a
// domain controller, with no PowerShell anywhere.
type ldapModel struct {
	Server             types.String `tfsdk:"server"`
	Port               types.Int64  `tfsdk:"port"`
	TLS                types.String `tfsdk:"tls"`
	CACertificateFile  types.String `tfsdk:"ca_certificate_file"`
	InsecureSkipVerify types.Bool   `tfsdk:"insecure_skip_verify"`
	MaxConcurrency     types.Int64  `tfsdk:"max_concurrency"`
	Timeout            types.String `tfsdk:"timeout"`

	Simple   *ldapSimpleModel `tfsdk:"simple"`
	Kerberos *kerberosModel   `tfsdk:"kerberos"`
	NTLM     *ldapNTLMModel   `tfsdk:"ntlm"`
}

type ldapSimpleModel struct {
	Username types.String `tfsdk:"username"`
	Password types.String `tfsdk:"password"`
}

type kerberosModel struct {
	CCachePath   types.String `tfsdk:"ccache_path"`
	Keytab       types.String `tfsdk:"keytab"`
	Password     types.String `tfsdk:"password"`
	Username     types.String `tfsdk:"username"`
	Realm        types.String `tfsdk:"realm"`
	Krb5ConfPath types.String `tfsdk:"krb5_conf_path"`
	SPN          types.String `tfsdk:"spn"`
}

type ldapNTLMModel struct {
	Domain   types.String `tfsdk:"domain"`
	Username types.String `tfsdk:"username"`
	Password types.String `tfsdk:"password"`
}

// resolveLDAP turns the ldap block plus the environment into the library's
// config. Configuration always wins over the environment.
func resolveLDAP(m ldapModel, getenv func(string) string, diags *diag.Diagnostics) adldap.Config {
	root := path.Root("ldap")
	cfg := adldap.Config{
		Server: str(m.Server, getenv, "AD_LDAP_SERVER"),
		Port:   int(m.Port.ValueInt64()),
		// The default is ldaps rather than empty: this backend has no plain
		// mode, so leaving it unset would only produce a validation error the
		// operator cannot act on.
		TLS:                adldap.TLSMode(strOr(str(m.TLS, getenv, "AD_LDAP_TLS"), string(adldap.TLSLDAPS))),
		CACertificateFile:  str(m.CACertificateFile, getenv, "AD_LDAP_CA_CERTIFICATE_FILE"),
		InsecureSkipVerify: boolOr(m.InsecureSkipVerify, false),
		MaxConcurrency:     int(m.MaxConcurrency.ValueInt64()),
		Timeout:            duration(m.Timeout, root.AtName("timeout"), defaultTransportTimeout, diags),
	}

	switch {
	case m.Simple != nil:
		cfg.Simple = &adldap.SimpleAuth{
			Username: str(m.Simple.Username, getenv, "AD_LDAP_USERNAME"),
			Password: adcore.NewSecret(str(m.Simple.Password, getenv, "AD_LDAP_PASSWORD")),
		}
	case m.Kerberos != nil:
		kerb := &adldap.KerberosAuth{
			Keytab:       str(m.Kerberos.Keytab, getenv, "AD_LDAP_KEYTAB"),
			Password:     adcore.NewSecret(str(m.Kerberos.Password, getenv, "AD_LDAP_PASSWORD")),
			Username:     str(m.Kerberos.Username, getenv, "AD_LDAP_USERNAME"),
			Realm:        str(m.Kerberos.Realm, getenv, "AD_LDAP_REALM"),
			Krb5ConfPath: str(m.Kerberos.Krb5ConfPath, getenv, "KRB5_CONFIG"),
			SPN:          str(m.Kerberos.SPN, getenv, "AD_LDAP_SPN"),
		}
		// An ambient KRB5CCNAME must not count as a second credential source
		// when one was configured: Config.Validate rejects two, and a developer
		// with a live ticket in their shell would see a configured password
		// fail with a message about exclusivity. Configuration wins over the
		// environment, so the ambient fallback applies only when nothing else
		// was chosen.
		if kerb.Keytab == "" && kerb.Password.IsZero() {
			kerb.CCachePath = str(m.Kerberos.CCachePath, getenv, "KRB5CCNAME")
		} else {
			kerb.CCachePath = m.Kerberos.CCachePath.ValueString()
		}
		cfg.Kerberos = kerb
	case m.NTLM != nil:
		cfg.NTLM = &adldap.NTLMAuth{
			Domain:   str(m.NTLM.Domain, getenv, "AD_LDAP_DOMAIN"),
			Username: str(m.NTLM.Username, getenv, "AD_LDAP_USERNAME"),
			Password: adcore.NewSecret(str(m.NTLM.Password, getenv, "AD_LDAP_PASSWORD")),
		}
	default:
		diags.AddAttributeError(root,
			"Exactly one authentication block is required",
			"Set exactly one of `simple`, `kerberos` or `ntlm` inside `ldap`. "+
				"There is no implicit default, for the same reason there is no default "+
				"connection block: guessing would authenticate as the wrong identity.\n\n"+
				"`kerberos {}` with no attributes uses the ticket from KRB5CCNAME — run "+
				"`kinit` before Terraform and no credential goes in configuration.")
	}
	return cfg
}

func strOr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
