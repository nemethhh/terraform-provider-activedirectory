package provider_test

import (
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// What only real AD proves here: the delegation boundary, and how KindDenied
// renders. The service account holds Full Control over AD_ACC_CONTAINER and
// nothing outside it, so it is genuinely powerless in AD_ACC_DENIED_CONTAINER —
// which is what makes this a test of the delegation rather than of a message.
//
// There is no CheckDestroy: nothing is created. If Active Directory did create
// the object, the step fails because the expected error never arrived, and the
// framework destroys what it made.
func TestAccDeniedOutsideTheDelegatedSubtree(t *testing.T) {
	e := accSuiteEnv()
	denied := os.Getenv(envDeniedContainer)

	resource.Test(t, resource.TestCase{
		PreCheck:                 accPreCheck(t, envDeniedContainer),
		ProtoV6ProviderFactories: accFactories(),
		Steps: []resource.TestStep{{
			Config: e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "outside" {
  name      = %q
  container = %q
}`, accNamePrefix+"outside", denied),
			// The exact summary the KindDenied branch renders. Matching it
			// pins the classification, not merely the failure: a constraint or
			// not-found would render something else and fail here.
			ExpectError: regexp.MustCompile(`Access denied by Active Directory`),
		}},
	})
}

// Reading an object the account cannot see must also be a denial rather than a
// silent "not found", because a not-found during Read drops the resource from
// state and the object becomes unmanaged and invisible.
func TestAccDeniedImportOutsideTheDelegatedSubtree(t *testing.T) {
	e := accSuiteEnv()
	denied := os.Getenv(envDeniedContainer)

	resource.Test(t, resource.TestCase{
		PreCheck:                 accPreCheck(t, envDeniedContainer),
		ProtoV6ProviderFactories: accFactories(),
		Steps: []resource.TestStep{{
			Config: e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "outside" {
  name      = %q
  container = %q
}`, accNamePrefix+"outside", denied),
			ResourceName:  "activedirectory_ou.outside",
			ImportState:   true,
			ImportStateId: "OU=" + accNamePrefix + "outside," + denied,
			// Either classification is a correct outcome here and which one
			// appears depends on the DACL: an account denied read sees
			// not-found, an account denied only write sees denied. What must
			// never happen is a successful import of an object the delegation
			// does not cover.
			ExpectError: regexp.MustCompile(`(?s)(Access denied by Active Directory|Object not found in Active Directory)`),
		}},
	})
}
