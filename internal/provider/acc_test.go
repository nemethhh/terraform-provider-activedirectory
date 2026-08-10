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

// The acceptance suite's environment. TF_ACC is Terraform's own gate and is
// checked by resource.Test; everything below is ours.
const (
	// envContainer is the DN of the delegated test subtree. Every object the
	// suite creates lives beneath it, and it is treated as pre-existing: the
	// suite never creates or destroys it.
	envContainer = "AD_ACC_CONTAINER"

	// envDeniedContainer is a DN the account has no rights over. It is what
	// makes the denial suite a test of the delegation rather than of a message.
	envDeniedContainer = "AD_ACC_DENIED_CONTAINER"

	// envSecondDC is a second domain controller, for the replication suite only.
	envSecondDC = "AD_ACC_SECOND_DC"

	// The rest configure the provider the way an operator would.
	envServer   = "AD_ACC_SERVER"   // the DC to pin; omit to discover one
	envUsername = "AD_ACC_USERNAME" // omit to run as the account that launched the test
	envPassword = "AD_ACC_PASSWORD"
	envPwshPath = "AD_ACC_PWSH_PATH" // omit to use pwsh on PATH
)

// accPreCheck fails the test when a required variable is missing. t.Fatal, not
// t.Skip: resource.Test has already decided the suite is meant to run, so a
// missing variable is a broken configuration, and a half-configured CI that
// reports green is worse than one that reports red.
func accPreCheck(t *testing.T, alsoRequired ...string) func() {
	t.Helper()
	return func() {
		required := append([]string{envContainer}, alsoRequired...)
		for _, name := range required {
			if os.Getenv(name) == "" {
				t.Fatalf("%s must be set to run the acceptance suite. See README.md, "+
					"\"Running the acceptance suite\".", name)
			}
		}
		// A half-set credential authenticates as nobody and fails at the first
		// cmdlet with an opaque message, so it is refused here instead.
		user, pass := os.Getenv(envUsername), os.Getenv(envPassword)
		if (user == "") != (pass == "") {
			t.Fatalf("set both %s and %s, or neither (neither means the suite runs as the "+
				"account that launched it)", envUsername, envPassword)
		}
	}
}

// accProviderConfig is the provider block the acceptance suite runs against.
//
// The local block is written literally: it is the deployment being tested, and
// selecting it by environment variable would leave the suite able to pass
// without ever exercising the local transport. Everything inside it, and the
// optional credential, comes from the AD_ACC_ variables, so the provider is
// configured exactly the way an operator would configure it.
//
// extraBlocks are appended inside the provider block, which is how the
// replication suite adds a replication block without a second copy of the rest.
func accProviderConfig(extraBlocks ...string) string {
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
	if u, p := os.Getenv(envUsername), os.Getenv(envPassword); u != "" && p != "" {
		// Terraform writes this configuration to a file in a temporary working
		// directory, so a credential used here is on disk for the run's
		// duration. Omitting both variables — the documented default — runs as
		// the account that launched the suite and puts no secret anywhere.
		fmt.Fprintf(&b, "    credential {\n      username = %q\n      password = %q\n    }\n", u, p)
	}
	b.WriteString("  }\n")

	for _, block := range extraBlocks {
		b.WriteString("\n")
		b.WriteString(block)
		b.WriteString("\n")
	}
	b.WriteString("}\n")
	return b.String()
}

// accSuiteEnv is the environment for a suite run against a real domain. It reads
// the environment without checking it: accPreCheck is what fails a
// half-configured run, and it runs after resource.Test has decided whether the
// suite runs at all — so with TF_ACC unset these values are never used.
func accSuiteEnv() suiteEnv {
	return suiteEnv{ProviderConfig: accProviderConfig(), Container: os.Getenv(envContainer)}
}

// accClient builds a library client over the local transport, configured from
// the same environment the provider reads. CheckDestroy and the sweeper both
// need to ask the directory questions that Terraform state cannot answer.
func accClient(t *testing.T) *adpwsh.Client {
	t.Helper()
	tr, err := adlocal.New(adlocal.Config{PwshPath: os.Getenv(envPwshPath)})
	if err != nil {
		t.Fatalf("acceptance: cannot start PowerShell: %v", err)
	}
	cfg := adpwsh.Config{Transport: tr, Server: os.Getenv(envServer)}
	if u, p := os.Getenv(envUsername), os.Getenv(envPassword); u != "" && p != "" {
		cfg.Credential = &adpwsh.Credential{Username: u, Password: adpwsh.NewSecret(p)}
	}
	client, err := adpwsh.New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("acceptance: cannot configure the Active Directory client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// accCheckDestroy asserts every object the test managed is actually gone from
// the directory. A resource missing from state proves nothing: the object may
// still exist and now be unmanaged and invisible, which is the failure this
// catches. The state it receives is the last one before the destroy.
func accCheckDestroy(t *testing.T) resource.TestCheckFunc {
	t.Helper()
	return func(state *terraform.State) error {
		client := accClient(t)
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
