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

// Pinned to what the first real lab run produced (2026-08-21): a bad credential
// is caught at provider-configure time and rendered as the clean summary
// "Cannot configure the Active Directory client" whose detail carries the
// authentication failure from ADWS — never a raw cmdlet dump or a panic.
var e2eWrongPasswordRe = regexp.MustCompile(
	`(?s)Cannot configure the Active Directory client.*Authentication failed`)

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

// A group created global, then asked to convert straight to domainlocal — a
// conversion AD refuses directly (it must pass through universal). The provider
// must surface the clean "operation failed" diagnostic, never a panic or a raw
// cmdlet dump, and must never replace the group to force the change. Real-only:
// the fake has no opinion on which conversions AD permits.
func TestAccE2EIllegalScopeConversion(t *testing.T) {
	user, pass := os.Getenv(envE2EAlphaUser), os.Getenv(envE2EAlphaPass)
	e := e2eSuiteEnv(user, pass, e2eAlphaDN())
	ou := accNamePrefix + "scope-ou"
	grp := accNamePrefix + "scope-grp"

	base := e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "staff" {
  name      = %q
  container = %q
}
`, ou, e.Container)
	global := base + fmt.Sprintf(`
resource "activedirectory_group" "g" {
  name             = %q
  sam_account_name = %q
  container        = activedirectory_ou.staff.dn
  scope            = "global"
}`, grp, grp)
	toDomainLocal := base + fmt.Sprintf(`
resource "activedirectory_group" "g" {
  name             = %q
  sam_account_name = %q
  container        = activedirectory_ou.staff.dn
  scope            = "domainlocal"
}`, grp, grp)

	resource.Test(t, resource.TestCase{
		PreCheck:                 e2ePreCheck(t, envE2EAlphaUser, envE2EAlphaPass),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             e2eCheckDestroy(t, user, pass),
		Steps: []resource.TestStep{
			{
				Config: global,
				Check:  resource.TestCheckResourceAttr("activedirectory_group.g", "scope", "global"),
			},
			{
				Config: toDomainLocal,
				// The library surfaces AD's own refusal. The exact wording is
				// AD's; assert on the stable diagnostic frame the provider adds.
				ExpectError: regexp.MustCompile(`(?s)(Active Directory|Group\.Update|operation failed)`),
			},
		},
	})
}
