package provider_test

import (
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// Scenario C: isolation + concurrency + boundary. TestAccE2EIsolationAlpha and
// TestAccE2EIsolationBeta both call t.Parallel(), so Go runs them at the same
// time under one `go test -run TestAccE2E` invocation — two Terraform working
// dirs, two credentials, disjoint subtrees.

const e2eIsolationFan = 3

func e2eIsolationSteps(e suiteEnv, label string) []resource.TestStep {
	cfg := e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "fan" {
  count     = %d
  name      = "%s%s-${count.index}"
  container = %q
}`, e2eIsolationFan, accNamePrefix, label, e.Container)

	checks := make([]resource.TestCheckFunc, 0, e2eIsolationFan)
	for i := 0; i < e2eIsolationFan; i++ {
		checks = append(checks, resource.TestCheckResourceAttr(
			fmt.Sprintf("activedirectory_ou.fan.%d", i), "dn",
			fmt.Sprintf("OU=%s%s-%d,%s", accNamePrefix, label, i, e.Container)))
	}
	return []resource.TestStep{
		{Config: cfg, Check: resource.ComposeAggregateTestCheckFunc(checks...)},
		{Config: cfg, PlanOnly: true},
	}
}

func TestAccE2EIsolationAlpha(t *testing.T) {
	t.Parallel()
	user, pass := os.Getenv(envE2EAlphaUser), os.Getenv(envE2EAlphaPass)
	e := e2eSuiteEnv(user, pass, e2eAlphaDN())
	resource.Test(t, resource.TestCase{
		PreCheck:                 e2ePreCheck(t, envE2EAlphaUser, envE2EAlphaPass),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             e2eCheckDestroy(t, user, pass),
		Steps:                    e2eIsolationSteps(e, "iso-a"),
	})
}

func TestAccE2EIsolationBeta(t *testing.T) {
	t.Parallel()
	user, pass := os.Getenv(envE2EBetaUser), os.Getenv(envE2EBetaPass)
	e := e2eSuiteEnv(user, pass, e2eBetaDN())
	resource.Test(t, resource.TestCase{
		PreCheck:                 e2ePreCheck(t, envE2EBetaUser, envE2EBetaPass),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             e2eCheckDestroy(t, user, pass),
		Steps:                    e2eIsolationSteps(e, "iso-b"),
	})
}

// alpha has no rights under OU=beta, so a create there must be a clean denial —
// the exact summary the KindDenied branch renders — not a crash.
func TestAccE2EDeniedCrossSubtree(t *testing.T) {
	user, pass := os.Getenv(envE2EAlphaUser), os.Getenv(envE2EAlphaPass)
	e := e2eSuiteEnv(user, pass, e2eBetaDN()) // alpha creds, beta container
	resource.Test(t, resource.TestCase{
		PreCheck:                 e2ePreCheck(t, envE2EAlphaUser, envE2EAlphaPass, envE2EBetaUser),
		ProtoV6ProviderFactories: accFactories(),
		Steps: []resource.TestStep{{
			Config: e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "outside" {
  name      = %q
  container = %q
}`, accNamePrefix+"cross", e.Container),
			ExpectError: regexp.MustCompile(`Access denied by Active Directory`),
		}},
	})
}

// Reading across the boundary must be a denial or a not-found, never a silent
// successful import of an object the delegation does not cover.
func TestAccE2EDeniedImportCrossSubtree(t *testing.T) {
	user, pass := os.Getenv(envE2EAlphaUser), os.Getenv(envE2EAlphaPass)
	e := e2eSuiteEnv(user, pass, e2eBetaDN())
	resource.Test(t, resource.TestCase{
		PreCheck:                 e2ePreCheck(t, envE2EAlphaUser, envE2EAlphaPass, envE2EBetaUser),
		ProtoV6ProviderFactories: accFactories(),
		Steps: []resource.TestStep{{
			Config: e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "outside" {
  name      = %q
  container = %q
}`, accNamePrefix+"cross", e.Container),
			ResourceName:  "activedirectory_ou.outside",
			ImportState:   true,
			ImportStateId: "OU=" + accNamePrefix + "cross," + e2eBetaDN(),
			ExpectError:   regexp.MustCompile(`(?s)(Access denied by Active Directory|Object not found in Active Directory)`),
		}},
	})
}
