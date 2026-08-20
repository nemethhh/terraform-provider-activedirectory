package provider_test

import (
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// Scenario D: failure modes. Wrong password must surface a clean diagnostic, not
// a panic; a partially-delegated principal must get the right classification
// when an operation exceeds its grant.

// The exact classification of a bad credential (KindDenied vs KindTransport) is
// what the FIRST real run against the lab pins — like the three defects the base
// suite's first run surfaced. Until then, match the family of clean diagnostics
// the provider can render, and never a raw cmdlet dump or a panic.
var e2eWrongPasswordRe = regexp.MustCompile(
	`(?s)(Access denied by Active Directory|Cannot reach Active Directory|Active Directory operation failed)`)

func TestAccE2EWrongPassword(t *testing.T) {
	cfg := e2eProviderConfig(os.Getenv(envE2EAlphaUser), "definitely-the-wrong-password-x9") +
		fmt.Sprintf(`
resource "activedirectory_ou" "never" {
  name      = %q
  container = %q
}`, accNamePrefix+"never", e2eAlphaDN())

	resource.Test(t, resource.TestCase{
		PreCheck:                 e2ePreCheck(t, envE2EAlphaUser),
		ProtoV6ProviderFactories: accFactories(),
		Steps: []resource.TestStep{{
			Config:      cfg,
			ExpectError: e2eWrongPasswordRe,
		}},
	})
}

// svc_e2e_limited may manage OUs but is denied create-child for group/user. The
// OU create succeeds; adding a group under it is denied. Because the only object
// created (the OU) is deletable by the same principal, the framework's teardown
// removes it cleanly with no leftover.
func TestAccE2ELimitedCannotCreateGroup(t *testing.T) {
	user, pass := os.Getenv(envE2ELimitedUser), os.Getenv(envE2ELimitedPass)
	e := e2eSuiteEnv(user, pass, e2eLimitedDN())
	ou := accNamePrefix + "lim-ou"
	grp := accNamePrefix + "lim-grp"

	// Protection off keeps the create within OU-management rights alone.
	okOU := e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "base" {
  name                               = %q
  container                          = %q
  protected_from_accidental_deletion = false
}`, ou, e.Container)

	denied := okOU + fmt.Sprintf(`
resource "activedirectory_group" "g" {
  name             = %q
  sam_account_name = %q
  container        = activedirectory_ou.base.dn
}`, grp, grp)

	resource.Test(t, resource.TestCase{
		PreCheck:                 e2ePreCheck(t, envE2ELimitedUser, envE2ELimitedPass),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             e2eCheckDestroy(t, user, pass),
		Steps: []resource.TestStep{
			{
				Config: okOU,
				Check:  resource.TestCheckResourceAttrSet("activedirectory_ou.base", "id"),
			},
			{
				Config:      denied,
				ExpectError: regexp.MustCompile(`Access denied by Active Directory`),
			},
		},
	})
}
