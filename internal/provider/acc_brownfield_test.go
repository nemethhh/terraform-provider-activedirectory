package provider_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// What only real AD proves here: that `terraform plan -generate-config-out`
// round-trips. Almost no Active Directory is greenfield, so adoption is a
// first-class path; a generated configuration that plans a change means some
// attribute this provider reads cannot be expressed in configuration, and
// adoption would fight the directory on every apply.
//
// GenerateConfig with ImportBlockWithID makes the framework run
// `terraform plan -generate-config-out` and then assert the planned import is a
// no-op, which is exactly the property under test. It needs no extra plan check.
func TestAccBrownfieldOUGenerateConfigRoundTrips(t *testing.T) {
	e := accSuiteEnv()

	resource.Test(t, resource.TestCase{
		PreCheck:                 accPreCheck(t),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             accCheckDestroy(t),
		Steps: []resource.TestStep{
			{
				Config: e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "adopt" {
  name        = %q
  container   = %q
  description = "adopted by generate-config-out"
}`, accNamePrefix+"adopt-ou", e.Container),
			},
			{
				ResourceName:    "activedirectory_ou.adopt",
				ImportState:     true,
				ImportStateKind: resource.ImportBlockWithID,
				GenerateConfig:  true,
			},
		},
	})
}

// The group carries a computed SID and two attributes with empty defaults, which
// is where a generated configuration most easily plans a change it should not.
func TestAccBrownfieldGroupGenerateConfigRoundTrips(t *testing.T) {
	e := accSuiteEnv()
	ou := accNamePrefix + "adopt-grp-ou"
	group := accNamePrefix + "adopt-grp"

	resource.Test(t, resource.TestCase{
		PreCheck:                 accPreCheck(t),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             accCheckDestroy(t),
		Steps: []resource.TestStep{
			{
				Config: e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "staff" {
  name      = %q
  container = %q
}
resource "activedirectory_group" "adopt" {
  name             = %q
  sam_account_name = %q
  container        = activedirectory_ou.staff.dn
  scope            = "universal"
  description      = "adopted by generate-config-out"
}`, ou, e.Container, group, group),
			},
			{
				ResourceName:    "activedirectory_group.adopt",
				ImportState:     true,
				ImportStateKind: resource.ImportBlockWithID,
				GenerateConfig:  true,
			},
		},
	})
}

// The user is the resource where a clean round trip is least likely, because
// password and password_version are not readable. If this fails with a planned
// change to either, the fix is in the schema's write-only handling, not in the
// test.
func TestAccBrownfieldUserGenerateConfigRoundTrips(t *testing.T) {
	e := accSuiteEnv()
	ou := accNamePrefix + "adopt-usr-ou"
	sam := accNamePrefix + "adopt-usr"

	resource.Test(t, resource.TestCase{
		PreCheck:                 accPreCheck(t),
		TerraformVersionChecks:   writeOnlyPasswordChecks,
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             accCheckDestroy(t),
		Steps: []resource.TestStep{
			{
				Config: e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "staff" {
  name      = %q
  container = %q
}
resource "activedirectory_user" "adopt" {
  sam_account_name    = %q
  container           = activedirectory_ou.staff.dn
  user_principal_name = %q
  enabled             = true
  password            = "Correct-Horse-Battery-Staple-1"
  password_version    = 1
}`, ou, e.Container, sam, sam+"@"+e.upnSuffix()),
			},
			{
				ResourceName:    "activedirectory_user.adopt",
				ImportState:     true,
				ImportStateKind: resource.ImportBlockWithID,
				GenerateConfig:  true,
			},
		},
	})
}
