package provider

import (
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func env(pairs map[string]string) func(string) string {
	return func(k string) string { return pairs[k] }
}

func sshOnly(s sshModel) providerModel { return providerModel{SSH: &s} }

func TestResolveSSHUsesConfigThenEnvironment(t *testing.T) {
	m := sshOnly(sshModel{
		Host:                  types.StringValue("jump.corp.local"),
		User:                  types.StringNull(), // falls back to AD_SSH_USER
		Password:              types.StringNull(), // falls back to AD_SSH_PASSWORD
		InsecureIgnoreHostKey: types.BoolValue(true),
	})
	got, diags := resolveSSH(m, env(map[string]string{
		"AD_SSH_USER":     "svc_tf",
		"AD_SSH_PASSWORD": "hunter2",
		"AD_SSH_PORT":     "2222",
	}))
	if diags.HasError() {
		t.Fatalf("resolveSSH: %v", diags)
	}
	if got.Host != "jump.corp.local" || got.User != "svc_tf" || got.Password != "hunter2" || got.Port != 2222 {
		t.Errorf("resolved = %+v", got)
	}
}

// Explicit configuration always wins over the environment.
func TestResolveSSHConfigBeatsEnvironment(t *testing.T) {
	m := sshOnly(sshModel{
		Host: types.StringValue("configured"), User: types.StringValue("cfg-user"),
		Password: types.StringValue("cfg-pass"), InsecureIgnoreHostKey: types.BoolValue(true),
	})
	got, _ := resolveSSH(m, env(map[string]string{
		"AD_SSH_HOST": "env", "AD_SSH_USER": "env-user", "AD_SSH_PASSWORD": "env-pass",
	}))
	if got.Host != "configured" || got.User != "cfg-user" || got.Password != "cfg-pass" {
		t.Errorf("environment overrode configuration: %+v", got)
	}
}

// Setting more than one host-key source is a validation error, not a silent
// precedence surprise — and the diagnostic points at the attribute.
func TestResolveSSHRejectsTwoHostKeySources(t *testing.T) {
	m := sshOnly(sshModel{
		Host: types.StringValue("jump"), User: types.StringValue("svc_tf"),
		Password:       types.StringValue("x"),
		HostKey:        types.StringValue("ssh-ed25519 AAAA"),
		KnownHostsFile: types.StringValue("~/.ssh/known_hosts"),
	})
	_, diags := resolveSSH(m, env(nil))
	if !diags.HasError() {
		t.Fatal("expected an error")
	}
	first := diags.Errors()[0]
	if !strings.Contains(first.Detail(), "host_key") {
		t.Errorf("detail = %q, should name host_key", first.Detail())
	}
	// Terraform underlines the offending line only when the diagnostic knows
	// which attribute it belongs to.
	if _, ok := first.(diag.DiagnosticWithPath); !ok {
		t.Error("the diagnostic must carry an attribute path")
	}
}

func TestResolveSSHRejectsAmbiguousAuth(t *testing.T) {
	tests := []struct {
		name  string
		model sshModel
		env   map[string]string
	}{
		{"none", sshModel{Host: types.StringValue("j"), User: types.StringValue("u"),
			InsecureIgnoreHostKey: types.BoolValue(true)}, nil},
		{"password and agent", sshModel{Host: types.StringValue("j"), User: types.StringValue("u"),
			Password: types.StringValue("x"), UseAgent: types.BoolValue(true),
			InsecureIgnoreHostKey: types.BoolValue(true)}, nil},
		{"config password and env key", sshModel{Host: types.StringValue("j"), User: types.StringValue("u"),
			Password: types.StringValue("x"), InsecureIgnoreHostKey: types.BoolValue(true)},
			map[string]string{"AD_SSH_PRIVATE_KEY_PATH": "/tmp/k"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, diags := resolveSSH(sshOnly(tt.model), env(tt.env)); !diags.HasError() {
				t.Fatal("expected exactly-one-credential to be enforced")
			}
		})
	}
}

func TestResolveSSHDefaults(t *testing.T) {
	m := sshOnly(sshModel{
		Host: types.StringValue("j"), User: types.StringValue("u"),
		Password: types.StringValue("x"), InsecureIgnoreHostKey: types.BoolValue(true),
	})
	got, _ := resolveSSH(m, env(nil))
	if got.Port != 22 || got.Concurrency != 4 || got.Timeout != defaultTransportTimeout {
		t.Errorf("defaults = %+v", got)
	}
}

func TestResolveSSHRejectsABadDuration(t *testing.T) {
	m := sshOnly(sshModel{
		Host: types.StringValue("j"), User: types.StringValue("u"),
		Password: types.StringValue("x"), InsecureIgnoreHostKey: types.BoolValue(true),
		Timeout: types.StringValue("one minute"),
	})
	if _, diags := resolveSSH(m, env(nil)); !diags.HasError() {
		t.Fatal("an unparseable timeout must be an attribute error")
	}
}

func TestResolveDomainCredential(t *testing.T) {
	m := providerModel{Domain: &domainModel{
		Server: types.StringValue("dc09.corp.local"),
		Credential: &credentialModel{
			Username: types.StringValue(`CORP\svc_tf`),
			Password: types.StringValue("hunter2"),
		},
	}}
	server, cred, diags := resolveDomain(m)
	if diags.HasError() {
		t.Fatal(diags)
	}
	if server != "dc09.corp.local" || cred == nil || cred.Username != `CORP\svc_tf` {
		t.Errorf("server = %q, cred = %+v", server, cred)
	}
	// The password must not be printable, even here.
	if got := cred.Password.String(); got != "REDACTED" {
		t.Errorf("credential password prints as %q", got)
	}
}

// A username with no password (or the reverse) is a half-configured credential
// and would fail at the first cmdlet with an opaque message.
func TestResolveDomainRejectsAHalfCredential(t *testing.T) {
	m := providerModel{Domain: &domainModel{
		Credential: &credentialModel{Username: types.StringValue("svc_tf"), Password: types.StringNull()},
	}}
	if _, _, diags := resolveDomain(m); !diags.HasError() {
		t.Fatal("expected an error naming the missing half")
	}
}

func TestResolvePSRPDefaultsAndEnv(t *testing.T) {
	env := map[string]string{
		"AD_PSRP_HOST": "dc.corp.local",
		"KRB5CCNAME":   "FILE:/tmp/krb5cc",
	}
	getenv := func(k string) string { return env[k] }

	cfg, diags := resolvePSRP(providerModel{}, getenv)
	if diags.HasError() {
		t.Fatalf("unexpected diags: %v", diags)
	}
	if cfg.Host != "dc.corp.local" {
		t.Errorf("Host = %q, want from AD_PSRP_HOST", cfg.Host)
	}
	if cfg.SPN != "HTTP/dc.corp.local" {
		t.Errorf("SPN = %q, want HTTP/dc.corp.local", cfg.SPN)
	}
	if cfg.Port != 5985 {
		t.Errorf("Port = %d, want 5985", cfg.Port)
	}
	if cfg.ConfigurationName != "PowerShell.7" {
		t.Errorf("ConfigurationName = %q", cfg.ConfigurationName)
	}
	if cfg.CCachePath != "/tmp/krb5cc" {
		t.Errorf("CCachePath = %q, want ambient KRB5CCNAME stripped of FILE:", cfg.CCachePath)
	}
}

func TestResolvePSRPConfigWinsOverEnv(t *testing.T) {
	getenv := func(k string) string {
		if k == "AD_PSRP_HOST" {
			return "env-host"
		}
		return ""
	}
	m := providerModel{PSRP: &psrpModel{Host: types.StringValue("cfg-host")}}
	cfg, _ := resolvePSRP(m, getenv)
	if cfg.Host != "cfg-host" {
		t.Errorf("Host = %q, want cfg-host (config beats env)", cfg.Host)
	}
}

func TestResolvePSRPMissingHost(t *testing.T) {
	cfg, diags := resolvePSRP(providerModel{}, func(string) string { return "" })
	if !diags.HasError() {
		t.Error("missing host: want a diagnostic")
	}
	_ = cfg
}

func localOnly(l localModel) providerModel { return providerModel{Local: &l} }

func TestResolveLocalDefaults(t *testing.T) {
	got, diags := resolveLocal(localOnly(localModel{}), env(nil))
	if diags.HasError() {
		t.Fatalf("resolveLocal: %v", diags)
	}
	if got.PwshPath != "pwsh" || got.Concurrency != 4 || got.Timeout != defaultTransportTimeout {
		t.Errorf("resolved = %+v", got)
	}
	// The library's WorkingDir is deliberately not exposed, so it is never set.
	if got.WorkingDir != "" {
		t.Errorf("WorkingDir = %q, want empty", got.WorkingDir)
	}
}

// One path per transport: local.pwsh_path beats the top-level pwsh_path, which
// beats the environment. Documenting one attribute per transport is clearer than
// one attribute whose meaning depends on a sibling block.
func TestResolveLocalPwshPathPrecedence(t *testing.T) {
	tests := []struct {
		name string
		m    providerModel
		env  map[string]string
		want string
	}{
		{
			name: "the local block wins",
			m: providerModel{
				PwshPath: types.StringValue("/top/pwsh"),
				Local:    &localModel{PwshPath: types.StringValue("/local/pwsh")},
			},
			env:  map[string]string{"AD_PWSH_PATH": "/env/pwsh"},
			want: "/local/pwsh",
		},
		{
			name: "the top-level attribute is next",
			m:    providerModel{PwshPath: types.StringValue("/top/pwsh"), Local: &localModel{}},
			env:  map[string]string{"AD_PWSH_PATH": "/env/pwsh"},
			want: "/top/pwsh",
		},
		{
			name: "the environment is the last resort",
			m:    providerModel{Local: &localModel{}},
			env:  map[string]string{"AD_PWSH_PATH": "/env/pwsh"},
			want: "/env/pwsh",
		},
		{
			name: "and then the library's default",
			m:    providerModel{Local: &localModel{}},
			want: "pwsh",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, diags := resolveLocal(tt.m, env(tt.env))
			if diags.HasError() {
				t.Fatalf("resolveLocal: %v", diags)
			}
			if got.PwshPath != tt.want {
				t.Errorf("PwshPath = %q, want %q", got.PwshPath, tt.want)
			}
		})
	}
}

func TestResolveLocalReadsTheEnvironmentForConcurrencyAndTimeout(t *testing.T) {
	got, diags := resolveLocal(localOnly(localModel{}), env(map[string]string{
		"AD_LOCAL_MAX_CONCURRENCY": "2",
		"AD_LOCAL_TIMEOUT":         "90s",
	}))
	if diags.HasError() {
		t.Fatalf("resolveLocal: %v", diags)
	}
	if got.Concurrency != 2 || got.Timeout != 90*time.Second {
		t.Errorf("resolved = %+v", got)
	}
}

func TestResolveLocalConfigBeatsEnvironment(t *testing.T) {
	got, _ := resolveLocal(localOnly(localModel{
		MaxConcurrency: types.Int64Value(6),
		Timeout:        types.StringValue("10s"),
	}), env(map[string]string{
		"AD_LOCAL_MAX_CONCURRENCY": "2",
		"AD_LOCAL_TIMEOUT":         "90s",
	}))
	if got.Concurrency != 6 || got.Timeout != 10*time.Second {
		t.Errorf("environment overrode configuration: %+v", got)
	}
}

// A value the environment supplies is still refused, and the diagnostic points
// at the attribute the operator can change rather than at the variable.
func TestResolveLocalRejectsUnusableEnvironmentValues(t *testing.T) {
	tests := []struct {
		name, wantDetail string
		env              map[string]string
	}{
		{"not a number", "AD_LOCAL_MAX_CONCURRENCY", map[string]string{"AD_LOCAL_MAX_CONCURRENCY": "four"}},
		{"not a duration", "AD_LOCAL_TIMEOUT", map[string]string{"AD_LOCAL_TIMEOUT": "one minute"}},
		{"negative concurrency", "concurrency", map[string]string{"AD_LOCAL_MAX_CONCURRENCY": "-1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, diags := resolveLocal(localOnly(localModel{}), env(tt.env))
			if !diags.HasError() {
				t.Fatal("expected an error")
			}
			first := diags.Errors()[0]
			if !strings.Contains(first.Summary()+first.Detail(), tt.wantDetail) {
				t.Errorf("diagnostic = %q / %q, should name %q",
					first.Summary(), first.Detail(), tt.wantDetail)
			}
			if _, ok := first.(diag.DiagnosticWithPath); !ok {
				t.Error("the diagnostic must carry an attribute path")
			}
		})
	}
}

func TestResolveLocalRejectsABadDuration(t *testing.T) {
	_, diags := resolveLocal(localOnly(localModel{Timeout: types.StringValue("one minute")}), env(nil))
	if !diags.HasError() {
		t.Fatal("an unparseable timeout must be an attribute error")
	}
}

// There is deliberately no implicit default. Defaulting to local when the block
// is absent turns a typo'd `ssh` block into silent local execution against the
// wrong identity; defaulting to ssh turns a typo'd `local` block into a dial to
// nowhere. Both are worse than a refusal.
func TestChooseTransportRequiresExactlyOneBlock(t *testing.T) {
	tests := []struct {
		name      string
		m         providerModel
		want      transportKind
		wantPaths []string // attribute paths the diagnostics must carry, in order
	}{
		{
			name: "local alone",
			m:    providerModel{Local: &localModel{}},
			want: transportLocal,
		},
		{
			name: "ssh alone",
			m:    providerModel{SSH: &sshModel{}},
			want: transportSSH,
		},
		{
			name:      "both",
			m:         providerModel{Local: &localModel{}, SSH: &sshModel{}},
			want:      transportUnset,
			wantPaths: []string{"local", "ssh"},
		},
		{
			name:      "neither",
			m:         providerModel{},
			want:      transportUnset,
			wantPaths: nil, // no block is written, so there is no line to underline
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, diags := chooseTransport(tt.m)
			if got != tt.want {
				t.Errorf("chooseTransport = %v, want %v", got, tt.want)
			}
			if tt.want != transportUnset {
				if diags.HasError() {
					t.Fatalf("unexpected diagnostics: %v", diags)
				}
				return
			}
			if !diags.HasError() {
				t.Fatal("expected an error")
			}
			for _, d := range diags.Errors() {
				// Whichever form it takes, the message must name both blocks so
				// the operator can see which one to remove.
				if !strings.Contains(d.Detail(), "local") || !strings.Contains(d.Detail(), "ssh") {
					t.Errorf("detail = %q, must name both blocks", d.Detail())
				}
			}
			if len(tt.wantPaths) == 0 {
				return
			}
			if len(diags.Errors()) != len(tt.wantPaths) {
				t.Fatalf("got %d diagnostics, want %d (one per offending block)",
					len(diags.Errors()), len(tt.wantPaths))
			}
			for i, want := range tt.wantPaths {
				withPath, ok := diags.Errors()[i].(diag.DiagnosticWithPath)
				if !ok {
					t.Fatalf("diagnostic %d carries no attribute path", i)
				}
				if got := withPath.Path().String(); got != want {
					t.Errorf("diagnostic %d path = %q, want %q", i, got, want)
				}
			}
		})
	}
}

func TestChooseTransportPSRP(t *testing.T) {
	k, d := chooseTransport(providerModel{PSRP: &psrpModel{}})
	if d.HasError() || k != transportPSRP {
		t.Fatalf("psrp-only: kind=%v diags=%v", k, d)
	}
}

func TestChooseTransportThreeWayConflict(t *testing.T) {
	_, d := chooseTransport(providerModel{Local: &localModel{}, PSRP: &psrpModel{}})
	if !d.HasError() {
		t.Error("local+psrp: want a conflict diagnostic")
	}
}

func TestChooseTransportNone(t *testing.T) {
	_, d := chooseTransport(providerModel{})
	if !d.HasError() {
		t.Error("no block: want a diagnostic")
	}
}
