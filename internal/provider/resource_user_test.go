package provider_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"

	"github.com/nemethhh/go-adpwsh/transport/fake"
)

// writeOnlyPasswordChecks gate the user suite: a write-only attribute needs
// Terraform 1.11.
var writeOnlyPasswordChecks = []tfversion.TerraformVersionCheck{
	tfversion.SkipBelow(tfversion.Version1_11_0),
}

func TestUserLifecycleAgainstTheFake(t *testing.T) {
	dir := fake.NewDirectory()
	// Only the fake can be asked what passwords were set. Two of them means the
	// rotation actually reached the directory rather than only the plan.
	rotated := func(*terraform.State) error {
		for _, history := range dir.Passwords {
			if len(history) == 2 {
				return nil
			}
		}
		return fmt.Errorf("expected a rotation, got %v", dir.Passwords)
	}
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks:   writeOnlyPasswordChecks,
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps:                    userLifecycleSteps(fakeSuiteEnv(), rotated),
	})
}

// Correctness rule 7, surfaced rather than accepted by AD and surprising later.
func TestUserRejectsContradictoryFlagsAtApply(t *testing.T) {
	e := fakeSuiteEnv()
	dir := fake.NewDirectory()
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks:   writeOnlyPasswordChecks,
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps: []resource.TestStep{{
			Config: e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "staff" {
  name      = %q
  container = %q
}
resource "activedirectory_user" "jdoe" {
  sam_account_name         = %q
  container                = activedirectory_ou.staff.dn
  change_password_at_logon = true
  password_expires         = false
}`, accNamePrefix+"usr-ou", e.Container, accNamePrefix+"usr"),
			ExpectError: regexp.MustCompile(`change_password_at_logon`),
		}},
	})
}

// password_version without password is a configuration that silently does
// nothing on rotation, so it is refused at plan.
func TestUserVersionRequiresPassword(t *testing.T) {
	e := fakeSuiteEnv()
	dir := fake.NewDirectory()
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks:   writeOnlyPasswordChecks,
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps: []resource.TestStep{{
			Config: e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "staff" {
  name      = %q
  container = %q
}
resource "activedirectory_user" "jdoe" {
  sam_account_name = %q
  container        = activedirectory_ou.staff.dn
  password_version = 3
}`, accNamePrefix+"usr-ou", e.Container, accNamePrefix+"usr"),
			PlanOnly:    true,
			ExpectError: regexp.MustCompile(`password`),
		}},
	})
}

// What only real AD proves here: the domain's password policy accepts these
// passwords, and surname really does map to sn. No password check is passed: a
// real domain will not say what it stored, and asserting on the rotation would
// mean reading a password back.
func TestAccUserLifecycle(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 accPreCheck(t),
		TerraformVersionChecks:   writeOnlyPasswordChecks,
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             accCheckDestroy(t),
		Steps:                    userLifecycleSteps(accSuiteEnv(), nil),
	})
}
