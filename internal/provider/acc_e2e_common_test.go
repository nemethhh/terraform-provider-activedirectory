package provider_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	adpwsh "github.com/nemethhh/go-adpwsh"
	adlocal "github.com/nemethhh/go-adpwsh/transport/local"
)

// The e2e layer's environment. It is a SEPARATELY provisioned environment from
// the base acceptance suite (three extra delegated principals and their OUs),
// so AD_E2E_CONTAINER is an opt-in gate: unset, the e2e suites skip; set, every
// other AD_E2E_ variable is fatal-if-missing. This single scoped skip is the
// only skip in the suite and is documented in CLAUDE.md.
const (
	envE2EContainer   = "AD_E2E_CONTAINER" // DN of OU=e2e; also the opt-in gate
	envE2EAlphaUser   = "AD_E2E_ALPHA_USERNAME"
	envE2EAlphaPass   = "AD_E2E_ALPHA_PASSWORD"
	envE2EBetaUser    = "AD_E2E_BETA_USERNAME"
	envE2EBetaPass    = "AD_E2E_BETA_PASSWORD"
	envE2ELimitedUser = "AD_E2E_LIMITED_USERNAME"
	envE2ELimitedPass = "AD_E2E_LIMITED_PASSWORD"
)

// e2eActive reports whether the e2e layer is configured in this environment.
func e2eActive() bool { return os.Getenv(envE2EContainer) != "" }

// e2ePreCheck skips when the layer is not provisioned here, and otherwise fails
// loud on a missing required variable — the same fail-loud posture as
// accPreCheck, gated behind the opt-in.
func e2ePreCheck(t *testing.T, alsoRequired ...string) func() {
	t.Helper()
	return func() {
		if !e2eActive() {
			t.Skipf("%s is not set; the e2e layer is a separately provisioned "+
				"environment (see LAB.md, \"E2E fixtures\"). Skipping.", envE2EContainer)
		}
		for _, name := range alsoRequired {
			if os.Getenv(name) == "" {
				t.Fatalf("%s must be set once %s is set (see LAB.md, "+
					"\"Running the e2e suite\")", name, envE2EContainer)
			}
		}
	}
}

// e2eProviderConfig is the provider block a scenario runs against: local {}
// written literally (the deployment under test), and a credential {} for the
// principal this scenario authenticates as. Unlike accProviderConfig the
// credential is never optional — the whole point of the layer is that each
// scenario runs as a specific delegated user.
func e2eProviderConfig(user, pass string) string {
	var b strings.Builder
	b.WriteString("provider \"activedirectory\" {\n")
	b.WriteString("  local {\n")
	if v := os.Getenv(envPwshPath); v != "" {
		fmt.Fprintf(&b, "    pwsh_path = %q\n", v)
	}
	b.WriteString("    max_concurrency = 4\n")
	b.WriteString("  }\n\n")
	b.WriteString("  domain {\n")
	if v := os.Getenv(envServer); v != "" {
		fmt.Fprintf(&b, "    server = %q\n", v)
	}
	fmt.Fprintf(&b, "    credential {\n      username = %q\n      password = %q\n    }\n", user, pass)
	b.WriteString("  }\n")
	b.WriteString("}\n")
	return b.String()
}

// e2eSuiteEnv drives the shared suites_test.go builders as a chosen principal
// against a chosen container.
func e2eSuiteEnv(user, pass, container string) suiteEnv {
	return suiteEnv{ProviderConfig: e2eProviderConfig(user, pass), Container: container}
}

// e2eClient is an out-of-band client authenticated as a chosen principal, for
// the drift scenarios and for CheckDestroy. Building it as the principal keeps
// a mutation performed by an identity that actually holds the right, and keeps
// the whole run free of admin credentials.
func e2eClient(t *testing.T, user, pass string) *adpwsh.Client {
	t.Helper()
	tr, err := adlocal.New(adlocal.Config{PwshPath: os.Getenv(envPwshPath)})
	if err != nil {
		t.Fatalf("e2e: cannot start PowerShell: %v", err)
	}
	cfg := adpwsh.Config{
		Transport:  tr,
		Server:     os.Getenv(envServer),
		Credential: &adpwsh.Credential{Username: user, Password: adpwsh.NewSecret(pass)},
	}
	client, err := adpwsh.New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("e2e: cannot configure the Active Directory client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// e2eCheckDestroy asserts every managed object is gone from the directory,
// verified as the scenario's own principal (which can read its subtree). It
// mirrors accCheckDestroy but takes an explicit credential so the run needs no
// admin identity.
func e2eCheckDestroy(t *testing.T, user, pass string) resource.TestCheckFunc {
	t.Helper()
	return func(state *terraform.State) error {
		client := e2eClient(t, user, pass)
		ctx := context.Background()
		for address, rs := range state.RootModule().Resources {
			if rs.Primary == nil {
				continue
			}
			id := rs.Primary.Attributes["id"]
			if id == "" {
				continue
			}
			var err error
			switch rs.Type {
			case "activedirectory_ou":
				_, err = client.OU.Get(ctx, adpwsh.ByGUID(id))
			case "activedirectory_group":
				_, err = client.Group.Get(ctx, adpwsh.ByGUID(id))
			case "activedirectory_user":
				_, err = client.User.Get(ctx, adpwsh.ByGUID(id))
			default:
				continue
			}
			if err == nil {
				return fmt.Errorf("%s (%s) still exists in the directory after destroy", address, id)
			}
			if !errors.Is(err, adpwsh.ErrNotFound) {
				return fmt.Errorf("cannot verify %s (%s) was destroyed: %w", address, id, err)
			}
		}
		return nil
	}
}

// The three delegated sub-OUs live directly beneath OU=e2e. Deriving their DNs
// means the suite cannot be pointed at one root and write objects under another.
func e2eSub(rdn string) string { return rdn + "," + os.Getenv(envE2EContainer) }
func e2eAlphaDN() string       { return e2eSub("OU=alpha") }
func e2eBetaDN() string        { return e2eSub("OU=beta") }
func e2eLimitedDN() string     { return e2eSub("OU=limited") }

// e2eBareSAM strips a possible "DOMAIN\" prefix from an AD_E2E_*_USERNAME
// value. Those variables are Windows credentials, written like AD_ACC_USERNAME
// in LAB.md ("CORP\svc_tfacc"), but an access_rule's trustee is an AD identity:
// Get-ADUser -Identity does not accept the "DOMAIN\sam" form. The delegation
// scenarios need the bare sAMAccountName to name a principal as a trustee, so
// the two uses of the same env var need different derivations.
func e2eBareSAM(cred string) string {
	if i := strings.LastIndex(cred, `\`); i >= 0 {
		return cred[i+1:]
	}
	return cred
}

// captureAttr stores one attribute of a resource into dst, so a later step's
// PreConfig can mutate that object out of band by its GUID/DN.
func captureAttr(resourceName, attr string, dst *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok || rs.Primary == nil {
			return fmt.Errorf("captureAttr: resource %s not in state", resourceName)
		}
		*dst = rs.Primary.Attributes[attr]
		return nil
	}
}

// importIDFromAttr resolves an import step's ID from live state, so an object
// can be imported by its DN or SID without hardcoding a value the run computes.
func importIDFromAttr(resourceName, attr string) resource.ImportStateIdFunc {
	return func(s *terraform.State) (string, error) {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok || rs.Primary == nil {
			return "", fmt.Errorf("importIDFromAttr: resource %s not in state", resourceName)
		}
		v := rs.Primary.Attributes[attr]
		if v == "" {
			return "", fmt.Errorf("importIDFromAttr: %s.%s is empty", resourceName, attr)
		}
		return v, nil
	}
}

// TestE2EProviderConfigComposition runs without a domain: the block must be
// local (never ssh) and must carry a credential.
func TestE2EProviderConfigComposition(t *testing.T) {
	got := e2eProviderConfig("svc_e2e_alpha", "pw")
	if !strings.Contains(got, "local {") || !strings.Contains(got, "max_concurrency = 4") {
		t.Errorf("the local block must be written literally:\n%s", got)
	}
	if strings.Contains(got, "ssh {") {
		t.Errorf("the e2e provider block must not configure ssh:\n%s", got)
	}
	if !strings.Contains(got, "credential {") || !strings.Contains(got, `username = "svc_e2e_alpha"`) {
		t.Errorf("the credential block must be present and populated:\n%s", got)
	}
	if n := strings.Count(got, `provider "activedirectory"`); n != 1 {
		t.Errorf("%d provider blocks, want exactly 1", n)
	}
}

// TestE2EActiveGate pins the opt-in gate that keeps the e2e suites out of a
// plain acceptance run.
func TestE2EActiveGate(t *testing.T) {
	t.Setenv(envE2EContainer, "")
	if e2eActive() {
		t.Error("e2eActive must be false when AD_E2E_CONTAINER is empty")
	}
	t.Setenv(envE2EContainer, "OU=e2e,DC=corp,DC=local")
	if !e2eActive() {
		t.Error("e2eActive must be true when AD_E2E_CONTAINER is set")
	}
	if got := e2eAlphaDN(); got != "OU=alpha,OU=e2e,DC=corp,DC=local" {
		t.Errorf("e2eAlphaDN() = %q", got)
	}
}
