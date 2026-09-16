package provider_test

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

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

// accFactories serves the real provider: no transport hook, so the transport the
// configuration selects is the one actually exercised. The acceptance suites use
// it; so do the two Configure-level tests below, which fail before any transport
// is constructed and therefore need no domain.
func accFactories() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"activedirectory": providerserver.NewProtocol6WithError(provider.New("acc")()),
	}
}

// Configure must refuse two transport blocks before it starts a process or opens
// a socket, which is what makes this a unit test rather than an acceptance one.
func TestConfigureRefusesTwoTransportBlocks(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: accFactories(),
		Steps: []resource.TestStep{{
			Config: `
provider "activedirectory" {
  local {}
  ssh {
    host                     = "jump.corp.local"
    user                     = "svc_tf"
    password                 = "x"
    insecure_ignore_host_key = true
  }
}

resource "activedirectory_ou" "unreachable" {
  name      = "tfacc-never-created"
  container = "DC=corp,DC=local"
}`,
			ExpectError: regexp.MustCompile(`Exactly one transport block is required`),
		}},
	})
}

func TestConfigureRefusesNoTransportBlock(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: accFactories(),
		Steps: []resource.TestStep{{
			Config: `
provider "activedirectory" {}

resource "activedirectory_ou" "unreachable" {
  name      = "tfacc-never-created"
  container = "DC=corp,DC=local"
}`,
			ExpectError: regexp.MustCompile(`Exactly one transport block is required`),
		}},
	})
}

// winrm+cold is a supported cell: a fresh Windows Remote Shell per op feeding
// the script on stdin to `powershell -EncodedCommand` (no command-size limit,
// no server-side PSRP session configuration required). Its runtime is validated
// on the lab (go-adpwsh TestLiveColdStdinGetADUser and the provider's
// winrm-cold lab cell); the schema's OneOf already accepts `cold`, exercised by
// TestConfigureRejectsUnknownMode below.

// An unknown mode value is refused by the schema's OneOf validator, before
// Configure runs at all.
func TestConfigureRejectsUnknownMode(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: accFactories(),
		Steps: []resource.TestStep{{
			Config: `
provider "activedirectory" {
  local {
    mode = "tepid"
  }
}

resource "activedirectory_ou" "unreachable" {
  name      = "tfacc-never-created"
  container = "DC=corp,DC=local"
}`,
			ExpectError: regexp.MustCompile(`(?i)value must be one of`),
		}},
	})
}

// winrm cold + a warm-only knob (configuration_name / language_mode) is a
// misconfiguration Configure catches before it opens any WinRM socket: cold uses
// the default WinRS shell, not a PSRP session configuration. A unit test.
func TestConfigureRejectsWinrmColdWithWarmOnlyKnobs(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: accFactories(),
		Steps: []resource.TestStep{{
			Config: `
provider "activedirectory" {
  winrm {
    host               = "dc1.corp.local"
    mode               = "cold"
    configuration_name = "AdObjects51"
  }
}

resource "activedirectory_ou" "unreachable" {
  name      = "tfacc-never-created"
  container = "DC=corp,DC=local"
}`,
			ExpectError: regexp.MustCompile(`configuration_name does not apply to winrm cold mode`),
		}},
	})
}

func TestConfigureRejectsWinrmServersWithColdMode(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: accFactories(),
		Steps: []resource.TestStep{{
			Config: `
provider "activedirectory" {
  winrm {
    mode = "cold"
    server { host = "dc1.corp.local" }
    server { host = "dc2.corp.local" }
  }
}

resource "activedirectory_ou" "unreachable" {
  name      = "tfacc-never-created"
  container = "DC=corp,DC=local"
}`,
			ExpectError: regexp.MustCompile(`(?i)multiple.*servers.*require.*warm`),
		}},
	})
}

// accTransportBlock's winrm branch emits two server{} sub-blocks instead of a
// flat host/spn when AD_ACC_WINRM_HOST2 is set, so the acceptance harness can
// exercise the failover schema (Task 4) against a real two-DC lab.
func TestProviderConfigWinrmTwoServers(t *testing.T) {
	t.Setenv("AD_ACC_TRANSPORT", "winrm")
	t.Setenv("AD_ACC_WINRM_HOST", "dc1.corp.local")
	t.Setenv("AD_ACC_WINRM_HOST2", "dc2.corp.local")
	t.Setenv("AD_ACC_WINRM_USER", "svc")
	got := accProviderConfig()
	for _, want := range []string{"server {", `host = "dc1.corp.local"`, `host = "dc2.corp.local"`} {
		if !strings.Contains(got, want) {
			t.Errorf("config missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "    host = \"dc1.corp.local\"\n") && strings.Contains(got, "  winrm {\n    host") {
		t.Error("two-host mode must not also emit a flat winrm host")
	}
}

func TestProviderConfigWinrmServerSelection(t *testing.T) {
	t.Setenv("AD_ACC_TRANSPORT", "winrm")
	t.Setenv("AD_ACC_WINRM_HOST", "dc1.corp.local")
	t.Setenv("AD_ACC_WINRM_HOST2", "dc2.corp.local")
	t.Setenv("AD_ACC_WINRM_USER", "svc")
	t.Setenv("AD_ACC_WINRM_SERVER_SELECTION", "round_robin")
	got := accProviderConfig()
	if !strings.Contains(got, `server_selection = "round_robin"`) {
		t.Errorf("winrm block missing server_selection line:\n%s", got)
	}
}

// AD_ACC_DIALECT puts the dialect into the generated provider block, so setting
// one variable runs every existing suite against the other script set. Unset
// emits nothing, which is what keeps a run that predates the dialect axis
// behaving exactly as it did.
func TestProviderConfigDialect(t *testing.T) {
	t.Setenv("AD_ACC_TRANSPORT", "local")
	t.Setenv("AD_ACC_DIALECT", "psopenad")
	if got := accProviderConfig(); !strings.Contains(got, `dialect = "psopenad"`) {
		t.Errorf("provider block missing the dialect line:\n%s", got)
	}
}

func TestProviderConfigDialectOmittedWhenUnset(t *testing.T) {
	t.Setenv("AD_ACC_TRANSPORT", "local")
	t.Setenv("AD_ACC_DIALECT", "")
	if got := accProviderConfig(); strings.Contains(got, "dialect") {
		t.Errorf("provider block emitted a dialect line with the variable unset:\n%s", got)
	}
}

func factoriesWith(dir *fake.Directory) map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"activedirectory": providerserver.NewProtocol6WithError(provider.NewWithTransport(dir.Transport())),
	}
}

// checkImportedAttr asserts one attribute of a single imported resource.
// Import steps that adopt a seeded object have no prior state to compare
// against, so ImportStateVerify has nothing to do and the attributes are
// asserted directly instead.
func checkImportedAttr(name, want string) resource.ImportStateCheckFunc {
	return func(states []*terraform.InstanceState) error {
		if len(states) != 1 {
			return fmt.Errorf("expected one imported resource, got %d", len(states))
		}
		if got := states[0].Attributes[name]; got != want {
			return fmt.Errorf("imported %s = %q, want %q", name, got, want)
		}
		return nil
	}
}

func composeImportStateCheck(checks ...resource.ImportStateCheckFunc) resource.ImportStateCheckFunc {
	return func(states []*terraform.InstanceState) error {
		for _, c := range checks {
			if err := c(states); err != nil {
				return err
			}
		}
		return nil
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

// providerConfigPSOpenAD is providerConfig with the dialect switched. The
// transport is still faked: what the dialect decides is which script text the
// provider hands that transport, and that is the only thing this proves.
const providerConfigPSOpenAD = `
provider "activedirectory" {
  dialect = "psopenad"

  ssh {
    host                     = "jump.corp.local"
    user                     = "svc_tf"
    password                 = "unused-because-the-transport-is-faked"
    insecure_ignore_host_key = true
  }
}
`

// The fake answers on the payload's `op` field, which is identical in both
// dialects, so it would serve a run that selected the wrong script set just as
// happily as the right one. The script text is the only evidence that selection
// actually happened, which is why this asserts on it rather than on state.
func TestPSOpenADDialectRunsThePSOpenADScriptsAgainstTheFake(t *testing.T) {
	dir := fake.NewDirectory()
	// Hold the transport: dir.Transport() returns a NEW recorder every call, so
	// asking for a second one would inspect a transport nothing ever ran on.
	tr := dir.Transport()
	factories := map[string]func() (tfprotov6.ProviderServer, error){
		"activedirectory": providerserver.NewProtocol6WithError(provider.NewWithTransport(tr)),
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{{
			Config: providerConfigPSOpenAD + `
resource "activedirectory_ou" "dialect" {
  name      = "tfacc-dialect"
  container = "DC=corp,DC=local"
}`,
		}},
	})

	calls := tr.Calls()
	if len(calls) == 0 {
		t.Fatal("the fake recorded no calls")
	}
	for _, c := range calls {
		if !strings.Contains(c.Script, "Import-Module PSOpenAD") {
			t.Errorf("op %q did not import PSOpenAD", c.Op)
		}
		if !strings.Contains(c.Script, "New-OpenADSession") {
			t.Errorf("op %q opened no OpenAD session", c.Op)
		}
		if strings.Contains(c.Script, "Import-Module ActiveDirectory") {
			t.Errorf("op %q ran the adws script set", c.Op)
		}
	}
}

// The mirror image, and the guard on the default: an unset dialect must still
// run the ActiveDirectory module, because every configuration written before
// the attribute existed depends on it.
func TestDefaultDialectRunsTheADWSScriptsAgainstTheFake(t *testing.T) {
	dir := fake.NewDirectory()
	tr := dir.Transport()
	factories := map[string]func() (tfprotov6.ProviderServer, error){
		"activedirectory": providerserver.NewProtocol6WithError(provider.NewWithTransport(tr)),
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{{
			Config: providerConfig + `
resource "activedirectory_ou" "dialect" {
  name      = "tfacc-dialect-default"
  container = "DC=corp,DC=local"
}`,
		}},
	})

	calls := tr.Calls()
	if len(calls) == 0 {
		t.Fatal("the fake recorded no calls")
	}
	for _, c := range calls {
		if !strings.Contains(c.Script, "Import-Module ActiveDirectory") {
			t.Errorf("op %q did not import ActiveDirectory", c.Op)
		}
		if strings.Contains(c.Script, "Import-Module PSOpenAD") {
			t.Errorf("op %q ran the psopenad script set by default", c.Op)
		}
	}
}

// The refusal is a configure-time diagnostic, not a runtime failure: nothing
// dials, and the message points at the attribute the user can change.
func TestProviderPSOpenADRejectsForcedReplication(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: accFactories(),
		Steps: []resource.TestStep{{
			Config: `
provider "activedirectory" {
  dialect = "psopenad"

  local {}

  replication {
    wait       = true
    targets    = ["all"]
    force_sync = true
  }
}

resource "activedirectory_ou" "unreachable" {
  name      = "tfacc-never-created"
  container = "DC=corp,DC=local"
}`,
			ExpectError: regexp.MustCompile(`(?i)force_sync is not supported`),
		}},
	})
}

// The pair is refused before anything dials, so this needs no reachable host.
func TestProviderPSOpenADRejectsConstrainedLanguage(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: accFactories(),
		Steps: []resource.TestStep{{
			Config: `
provider "activedirectory" {
  dialect = "psopenad"

  winrm {
    host               = "mgmt.corp.local"
    configuration_name = "AdSandbox"
    language_mode      = "constrained"
  }
}

resource "activedirectory_ou" "unreachable" {
  name      = "tfacc-never-created"
  container = "DC=corp,DC=local"
}`,
			ExpectError: regexp.MustCompile(`(?i)cannot run the psopenad dialect`),
		}},
	})
}

// The out-of-band clients — CheckDestroy's accClient, the sweeper, the e2e
// layer — are built from adpwsh.Config directly rather than from the generated
// provider block, so accDialectLine() does not reach them. A Config without a
// Dialect is DialectADWS, which on the psopenad cell means Linux trying to
// Import-Module ActiveDirectory: the suite's own verification fails even though
// every resource operation succeeded.
//
// This gate is a source scan because the omission is invisible at runtime until
// a real domain is on the other end, and it cost a 13-minute lab run to find.
func TestRealDomainClientsCarryTheDialect(t *testing.T) {
	for _, name := range []string{
		"acc_test.go", "acc_sweeper_test.go", "acc_e2e_common_test.go", "acc_large_group_test.go",
	} {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		src := string(b)
		for i, decl := range strings.Split(src, "adpwsh.Config{")[1:] {
			// The literal ends at its closing brace; a Dialect line must appear
			// inside it, or be assigned to the cfg before adpwsh.New is called.
			end := strings.Index(decl, "}")
			if end < 0 {
				t.Fatalf("%s: unterminated adpwsh.Config literal #%d", name, i+1)
			}
			body := decl[:end]
			// A multi-line literal that sets fields after construction is still
			// fine, so widen to the following adpwsh.New call.
			if n := strings.Index(decl, "adpwsh.New("); n > end {
				body = decl[:n]
			}
			if !strings.Contains(body, "Dialect") {
				t.Errorf("%s: adpwsh.Config #%d is built without a Dialect, so it runs adws "+
					"regardless of AD_ACC_DIALECT:\n\t%s", name, i+1,
					strings.TrimSpace(strings.SplitN(body, "\n", 2)[0]))
			}
		}
	}
}
