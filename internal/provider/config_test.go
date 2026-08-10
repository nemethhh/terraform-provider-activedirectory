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
	if got.Port != 22 || got.Concurrency != 4 || got.Timeout != 60*time.Second {
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
