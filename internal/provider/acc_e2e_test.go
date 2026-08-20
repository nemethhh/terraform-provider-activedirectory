package provider_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// Scenario A: the full feature set run as svc_e2e_alpha, a full-control
// delegated non-admin, against OU=alpha. These drive the SAME container-
// parameterised builders the fake-backed and base-acceptance suites use, so a
// principal-versus-principal divergence is caught rather than hidden.

func e2eAlphaEnv() suiteEnv {
	return e2eSuiteEnv(os.Getenv(envE2EAlphaUser), os.Getenv(envE2EAlphaPass), e2eAlphaDN())
}

func TestAccE2EOULifecycle(t *testing.T) {
	e := e2eAlphaEnv()
	resource.Test(t, resource.TestCase{
		PreCheck:                 e2ePreCheck(t, envE2EAlphaUser, envE2EAlphaPass),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             e2eCheckDestroy(t, os.Getenv(envE2EAlphaUser), os.Getenv(envE2EAlphaPass)),
		Steps:                    ouLifecycleSteps(e),
	})
}

func TestAccE2EGroupLifecycle(t *testing.T) {
	e := e2eAlphaEnv()
	resource.Test(t, resource.TestCase{
		PreCheck:                 e2ePreCheck(t, envE2EAlphaUser, envE2EAlphaPass),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             e2eCheckDestroy(t, os.Getenv(envE2EAlphaUser), os.Getenv(envE2EAlphaPass)),
		Steps:                    groupLifecycleSteps(e),
	})
}

func TestAccE2EUserLifecycle(t *testing.T) {
	e := e2eAlphaEnv()
	resource.Test(t, resource.TestCase{
		PreCheck:                 e2ePreCheck(t, envE2EAlphaUser, envE2EAlphaPass),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             e2eCheckDestroy(t, os.Getenv(envE2EAlphaUser), os.Getenv(envE2EAlphaPass)),
		// nil password check: a real domain will not tell anyone what password was set.
		Steps: userLifecycleSteps(e, nil),
	})
}

// The hostile values survive the cmdlet layer as a delegated user too — the
// path a real operator's data takes.
func TestAccE2EHostileDescriptions(t *testing.T) {
	e := e2eAlphaEnv()
	for _, hv := range hostileValues {
		hv := hv
		t.Run(hv.Name, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				PreCheck:                 e2ePreCheck(t, envE2EAlphaUser, envE2EAlphaPass),
				ProtoV6ProviderFactories: accFactories(),
				CheckDestroy:             e2eCheckDestroy(t, os.Getenv(envE2EAlphaUser), os.Getenv(envE2EAlphaPass)),
				Steps:                    hostileDescriptionSteps(e, hv.Value),
			})
		})
	}
}

func TestAccE2EHostileEscapedComma(t *testing.T) {
	e := e2eAlphaEnv()
	resource.Test(t, resource.TestCase{
		PreCheck:                 e2ePreCheck(t, envE2EAlphaUser, envE2EAlphaPass),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             e2eCheckDestroy(t, os.Getenv(envE2EAlphaUser), os.Getenv(envE2EAlphaPass)),
		Steps:                    hostileEscapedCommaSteps(e),
	})
}

// Import forms: GUID (ou/group lifecycle) and sAMAccountName (user lifecycle)
// are already covered by the builders above. This adds the remaining two — DN
// and SID — on a group, resolving each ID from live state.
func TestAccE2EImportForms(t *testing.T) {
	e := e2eAlphaEnv()
	ou := accNamePrefix + "imp-ou"
	grp := accNamePrefix + "imp-grp"
	cfg := e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "staff" {
  name      = %q
  container = %q
}
resource "activedirectory_group" "g" {
  name             = %q
  sam_account_name = %q
  container        = activedirectory_ou.staff.dn
}`, ou, e.Container, grp, grp)

	resource.Test(t, resource.TestCase{
		PreCheck:                 e2ePreCheck(t, envE2EAlphaUser, envE2EAlphaPass),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             e2eCheckDestroy(t, os.Getenv(envE2EAlphaUser), os.Getenv(envE2EAlphaPass)),
		Steps: []resource.TestStep{
			{Config: cfg},
			{
				Config:            cfg,
				ResourceName:      "activedirectory_group.g",
				ImportState:       true,
				ImportStateIdFunc: importIDFromAttr("activedirectory_group.g", "dn"),
				ImportStateVerify: true,
			},
			{
				Config:            cfg,
				ResourceName:      "activedirectory_group.g",
				ImportState:       true,
				ImportStateIdFunc: importIDFromAttr("activedirectory_group.g", "sid"),
				ImportStateVerify: true,
			},
		},
	})
}
