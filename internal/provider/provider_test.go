package provider_test

import (
	"context"
	"fmt"
	"regexp"
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
