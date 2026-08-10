package provider_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/nemethhh/go-adpwsh/transport/fake"
)

func TestGroupLifecycleAgainstTheFake(t *testing.T) {
	dir := fake.NewDirectory()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps:                    groupLifecycleSteps(fakeSuiteEnv()),
	})
}

// The enums are validated in the schema, so a typo is a plan error naming the
// attribute rather than an opaque AD failure at apply. There is nothing a real
// domain adds to that, so this one stays fake-only.
func TestGroupRejectsAnUnknownScopeAtPlan(t *testing.T) {
	e := fakeSuiteEnv()
	dir := fake.NewDirectory()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps: []resource.TestStep{{
			Config: e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "staff" {
  name      = %q
  container = %q
}
resource "activedirectory_group" "devs" {
  name             = %q
  sam_account_name = %q
  container        = activedirectory_ou.staff.dn
  scope            = "worldwide"
}`, accNamePrefix+"grp-ou", e.Container, accNamePrefix+"grp", accNamePrefix+"grp"),
			PlanOnly:    true,
			ExpectError: regexp.MustCompile(`(?s)scope.*worldwide`),
		}},
	})
}

// What only real AD proves here: which scope conversions Active Directory
// actually permits. global to universal is the conversion this suite makes; if
// a real domain refuses it, that is the suite finding a defect in the provider's
// conversion path rather than the suite being wrong.
func TestAccGroupLifecycle(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 accPreCheck(t),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             accCheckDestroy(t),
		Steps:                    groupLifecycleSteps(accSuiteEnv()),
	})
}
