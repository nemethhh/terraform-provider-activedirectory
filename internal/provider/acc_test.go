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
	adssh "github.com/nemethhh/go-adpwsh/transport/ssh"
	adwinrm "github.com/nemethhh/go-adpwsh/transport/winrm"
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

	// envTransport selects the deployment under test: "local" (the default, and
	// the only one that can run on the host itself), "ssh", or "winrm". A 5.1
	// endpoint is reached over winrm from wherever the suite runs.
	envTransport = "AD_ACC_TRANSPORT"

	// envMode selects the execution mode emitted into the transport block:
	// "cold" or "warm". Empty leaves the attribute out, so the provider's own
	// default (warm) applies — which is what the pre-two-axis runs expect.
	envMode = "AD_ACC_MODE"

	// The ssh transport's own settings.
	envSSHHost    = "AD_ACC_SSH_HOST"
	envSSHUser    = "AD_ACC_SSH_USER"
	envSSHKeyPath = "AD_ACC_SSH_KEY_PATH"

	// The winrm transport's own settings. Kerberos files come from the ambient
	// KRB5_CONFIG and KRB5CCNAME, which the library already reads.
	envWinrmHost         = "AD_ACC_WINRM_HOST"
	envWinrmUser         = "AD_ACC_WINRM_USER"
	envWinrmPassword     = "AD_ACC_WINRM_PASSWORD"
	envWinrmSPN          = "AD_ACC_WINRM_SPN"
	envWinrmConfigName   = "AD_ACC_WINRM_CONFIGURATION_NAME"
	envWinrmLanguageMode = "AD_ACC_WINRM_LANGUAGE_MODE"

	// envWinrmHost2/envWinrmSPN2 select a second endpoint for the failover
	// schema (Task 4): when host2 is set, accTransportBlock emits two server{}
	// sub-blocks (host1/spn1, host2/spn2) instead of the flat host/spn lines.
	// Everything else in the winrm block stays shared between the two DCs.
	envWinrmHost2 = "AD_ACC_WINRM_HOST2"
	envWinrmSPN2  = "AD_ACC_WINRM_SPN2"
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
		// A remote transport with no host authenticates against nothing and
		// fails at the first cmdlet with an opaque message, so refuse here.
		switch accTransportName() {
		case "ssh":
			if os.Getenv(envSSHHost) == "" {
				t.Fatalf("%s=ssh requires %s", envTransport, envSSHHost)
			}
		case "winrm":
			if os.Getenv(envWinrmHost) == "" {
				t.Fatalf("%s=winrm requires %s", envTransport, envWinrmHost)
			}
			// go-psrp needs the principal name even with an ambient ticket
			// cache: Linux has no SSPI single sign-on to infer it from.
			if os.Getenv(envWinrmUser) == "" {
				t.Fatalf("%s=winrm requires %s", envTransport, envWinrmUser)
			}
		}
	}
}

// accTransportName is the deployment the suite exercises. Empty means local, so
// every existing invocation behaves exactly as it did before.
func accTransportName() string {
	switch v := strings.ToLower(strings.TrimSpace(os.Getenv(envTransport))); v {
	case "", "local":
		return "local"
	case "ssh", "winrm":
		return v
	default:
		panic(fmt.Sprintf("%s=%q: want local, ssh or winrm", envTransport, v))
	}
}

// accTransportBlock renders the selected transport literally. Every branch emits
// the line "    max_concurrency = 4" verbatim, because
// accProviderConfigWithConcurrency and accProviderConfigWithTimeout rewrite it.
func accTransportBlock() string {
	var b strings.Builder
	switch accTransportName() {
	case "ssh":
		b.WriteString("  ssh {\n")
		fmt.Fprintf(&b, "    host = %q\n", os.Getenv(envSSHHost))
		if v := os.Getenv(envSSHUser); v != "" {
			fmt.Fprintf(&b, "    user = %q\n", v)
		}
		if v := os.Getenv(envSSHKeyPath); v != "" {
			fmt.Fprintf(&b, "    private_key_path = %q\n", v)
		}
		// A lab jump box has no host key in a known_hosts file on a throwaway
		// CI agent. This is a test harness, not a deployment recommendation.
		b.WriteString("    insecure_ignore_host_key = true\n")
		b.WriteString(accModeLine())
		b.WriteString("    max_concurrency = 4\n")
		b.WriteString("  }\n")
	case "winrm":
		b.WriteString("  winrm {\n")
		if h2 := os.Getenv(envWinrmHost2); h2 != "" {
			emitServer := func(host, spn string) {
				fmt.Fprintf(&b, "    server {\n      host = %q\n", host)
				if spn != "" {
					fmt.Fprintf(&b, "      spn = %q\n", spn)
				}
				b.WriteString("    }\n")
			}
			emitServer(os.Getenv(envWinrmHost), os.Getenv(envWinrmSPN))
			emitServer(h2, os.Getenv(envWinrmSPN2))
		} else {
			fmt.Fprintf(&b, "    host = %q\n", os.Getenv(envWinrmHost))
			if v := os.Getenv(envWinrmSPN); v != "" {
				fmt.Fprintf(&b, "    spn = %q\n", v)
			}
		}
		if v := os.Getenv(envWinrmUser); v != "" {
			fmt.Fprintf(&b, "    user = %q\n", v)
		}
		if v := os.Getenv(envWinrmPassword); v != "" {
			fmt.Fprintf(&b, "    password = %q\n", v)
		}
		if v := os.Getenv(envWinrmConfigName); v != "" {
			fmt.Fprintf(&b, "    configuration_name = %q\n", v)
		}
		if v := os.Getenv(envWinrmLanguageMode); v != "" {
			fmt.Fprintf(&b, "    language_mode = %q\n", v)
		}
		b.WriteString(accModeLine())
		b.WriteString("    max_concurrency = 4\n")
		b.WriteString("  }\n")
	default:
		b.WriteString("  local {\n")
		if v := os.Getenv(envPwshPath); v != "" {
			fmt.Fprintf(&b, "    pwsh_path = %q\n", v)
		}
		b.WriteString(accModeLine())
		b.WriteString("    max_concurrency = 4\n")
		b.WriteString("  }\n")
	}
	return b.String()
}

// accModeLine renders the transport block's `mode` attribute from AD_ACC_MODE.
// Empty emits nothing, leaving the provider's default (warm) in force — so a
// run that predates the two-axis config behaves exactly as it did before.
func accModeLine() string {
	if v := os.Getenv(envMode); v != "" {
		return fmt.Sprintf("    mode = %q\n", v)
	}
	return ""
}

// accProviderConfig is the provider block the acceptance suite runs against.
//
// The transport block is written literally: it is the deployment being tested,
// and selecting it inside the provider from the environment would leave the
// suite able to pass without ever exercising it. Everything inside it, and the
// optional credential, comes from the AD_ACC_ variables, so the provider is
// configured exactly the way an operator would configure it.
//
// extraBlocks are appended inside the provider block, which is how the
// replication suite adds a replication block without a second copy of the rest.
func accProviderConfig(extraBlocks ...string) string {
	var b strings.Builder
	b.WriteString("provider \"activedirectory\" {\n")
	// The ssh transport reads the top-level pwsh_path (the ssh block has none),
	// so emitting it here selects the jump box's PowerShell — Windows PowerShell
	// 5.1 vs 7 — for the cold path. Warm ssh ignores it: the sshd `powershell`
	// subsystem launches pwsh 7 itself.
	if accTransportName() == "ssh" {
		if v := os.Getenv(envPwshPath); v != "" {
			fmt.Fprintf(&b, "  pwsh_path = %q\n", v)
		}
	}
	b.WriteString(accTransportBlock())
	b.WriteString("\n")

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

// accProviderConfigWithConcurrency is accProviderConfig with an explicit
// max_concurrency. A configuration has exactly one provider block, so a suite
// that needs a different bound needs the whole block rebuilt rather than a
// second one appended.
func accProviderConfigWithConcurrency(n int) string {
	return strings.Replace(accProviderConfig(),
		"    max_concurrency = 4\n",
		fmt.Sprintf("    max_concurrency = %d\n", n), 1)
}

// accProviderConfigWithTimeout is accProviderConfig with an explicit local
// transport timeout, for suites whose single operations legitimately exceed the
// 60s default — e.g. reading a group with thousands of members, where the
// per-member read-back is a long sequence of directory calls.
func accProviderConfigWithTimeout(d string) string {
	return strings.Replace(accProviderConfig(),
		"    max_concurrency = 4\n",
		fmt.Sprintf("    max_concurrency = 4\n    timeout = %q\n", d), 1)
}

// accSuiteEnv is the environment for a suite run against a real domain. It reads
// the environment without checking it: accPreCheck is what fails a
// half-configured run, and it runs after resource.Test has decided whether the
// suite runs at all — so with TF_ACC unset these values are never used.
func accSuiteEnv() suiteEnv {
	return suiteEnv{ProviderConfig: accProviderConfig(), Container: os.Getenv(envContainer)}
}

// accTransport builds the transport the suite's own verification client uses.
// CheckDestroy asks the directory questions Terraform state cannot answer, so it
// must reach AD the same way the provider under test does.
func accTransport(t *testing.T) adpwsh.Transport {
	t.Helper()
	switch accTransportName() {
	case "ssh":
		tr, err := adssh.New(adssh.Config{
			Host:                  os.Getenv(envSSHHost),
			User:                  os.Getenv(envSSHUser),
			PrivateKeyPath:        os.Getenv(envSSHKeyPath),
			InsecureIgnoreHostKey: true,
			PwshPath:              os.Getenv(envPwshPath),
		})
		if err != nil {
			t.Fatalf("acceptance: cannot open the ssh transport: %v", err)
		}
		return tr
	case "winrm":
		// The verification client must reach AD the same way the provider does,
		// including the execution mode: cold opens a WinRS shell (no PSRP session
		// configuration), warm a PSRP runspace. Building warm for a cold cell would
		// try a PSRP endpoint the cold transport account cannot open.
		cfg := adwinrm.Config{
			Host:         os.Getenv(envWinrmHost),
			Username:     os.Getenv(envWinrmUser),
			Password:     os.Getenv(envWinrmPassword),
			SPN:          os.Getenv(envWinrmSPN),
			Krb5ConfPath: os.Getenv("KRB5_CONFIG"),
			CCachePath:   strings.TrimPrefix(os.Getenv("KRB5CCNAME"), "FILE:"),
			Concurrency:  1,
		}
		// Mirror the provider's failover config: when a second host is
		// configured, the verification client must probe the same ordered
		// endpoint list, or a run whose primary is down would fail here
		// (in the verifier) rather than exercising the provider's failover.
		if h2 := os.Getenv(envWinrmHost2); h2 != "" {
			cfg.Endpoints = []adwinrm.Endpoint{
				{Host: os.Getenv(envWinrmHost), SPN: os.Getenv(envWinrmSPN)},
				{Host: h2, SPN: os.Getenv(envWinrmSPN2)},
			}
			cfg.Host, cfg.SPN = "", "" // the endpoint list is authoritative (Validate requires Host empty when Endpoints set)
		}
		var tr adpwsh.Transport
		var err error
		if strings.EqualFold(os.Getenv(envMode), "cold") {
			tr, err = adwinrm.NewCold(cfg) // default WinRS shell; config_name/language_mode don't apply
		} else {
			// language_mode matters for warm: against a constrained sandbox endpoint
			// a full-language wrapper's [Console]::SetIn is rejected by CLM.
			cfg.ConfigurationName = os.Getenv(envWinrmConfigName)
			cfg.LanguageMode = os.Getenv(envWinrmLanguageMode)
			tr, err = adwinrm.New(cfg)
		}
		if err != nil {
			t.Fatalf("acceptance: cannot open the winrm transport: %v", err)
		}
		return tr
	default:
		tr, err := adlocal.New(adlocal.Config{PwshPath: os.Getenv(envPwshPath)})
		if err != nil {
			t.Fatalf("acceptance: cannot start PowerShell: %v", err)
		}
		return tr
	}
}

// accClient builds a library client over the transport under test, configured
// from the same environment the provider reads. CheckDestroy and the sweeper
// both need to ask the directory questions that Terraform state cannot answer.
func accClient(t *testing.T) *adpwsh.Client {
	t.Helper()
	tr := accTransport(t)
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
			case "activedirectory_gmsa":
				_, err = client.ServiceAccount.Get(ctx, adpwsh.ByGUID(id))
			case "activedirectory_computer":
				_, err = client.Computer.Get(ctx, adpwsh.ByGUID(id))
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

func TestProviderConfigComposition(t *testing.T) {
	// The transport block is written literally, because it is the deployment
	// under test: selecting it inside the provider from the environment would
	// let the suite pass without ever exercising the block.
	for _, tc := range []struct {
		transport string
		wants     []string
		rejects   []string
	}{
		{"", []string{"local {"}, []string{"ssh {", "winrm {"}},
		{"local", []string{"local {"}, []string{"ssh {", "winrm {"}},
		{"ssh", []string{"ssh {", `host = "jump.example.com"`}, []string{"local {", "winrm {"}},
		{"winrm", []string{"winrm {", `configuration_name = "AdObjects51"`}, []string{"local {", "ssh {"}},
	} {
		t.Run("transport="+tc.transport, func(t *testing.T) {
			t.Setenv(envTransport, tc.transport)
			t.Setenv(envSSHHost, "jump.example.com")
			t.Setenv(envWinrmHost, "mgmt.example.com")
			t.Setenv(envWinrmConfigName, "AdObjects51")

			got := accProviderConfig()
			for _, w := range tc.wants {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q:\n%s", w, got)
				}
			}
			for _, r := range tc.rejects {
				if strings.Contains(got, r) {
					t.Errorf("must not configure %q:\n%s", r, got)
				}
			}
			if n := strings.Count(got, "provider \"activedirectory\""); n != 1 {
				t.Errorf("%d provider blocks, want 1", n)
			}
			// The concurrency and timeout helpers rewrite this exact line.
			if !strings.Contains(got, "    max_concurrency = 4\n") {
				t.Errorf("every transport block must emit the literal max_concurrency line:\n%s", got)
			}
		})
	}

	withExtra := accProviderConfig("  replication {\n    wait = true\n  }")
	if !strings.Contains(withExtra, "replication {") {
		t.Errorf("the extra block was dropped:\n%s", withExtra)
	}
	if !strings.HasSuffix(strings.TrimSpace(withExtra), "}") ||
		strings.Index(withExtra, "replication {") > strings.LastIndex(withExtra, "}") {
		t.Errorf("the extra block escaped the provider block:\n%s", withExtra)
	}
	if got := accProviderConfigWithConcurrency(1); !strings.Contains(got, "max_concurrency = 1") {
		t.Errorf("the concurrency override did not take:\n%s", got)
	}
}
