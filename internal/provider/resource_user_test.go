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

const userBase = providerConfig + `
resource "activedirectory_ou" "staff" {
  name      = "Staff"
  container = "DC=corp,DC=local"
}
`

func TestAccUserLifecycle(t *testing.T) {
	dir := fake.NewDirectory()
	resource.UnitTest(t, resource.TestCase{
		// Write-only attributes require Terraform 1.11.
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_11_0)},
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps: []resource.TestStep{
			{
				Config: userBase + `
resource "activedirectory_user" "jdoe" {
  sam_account_name    = "jdoe"
  container           = activedirectory_ou.staff.dn
  user_principal_name = "jdoe@corp.local"
  display_name        = "John Doe"
  given_name          = "John"
  surname             = "Doe"
  enabled             = true
  password            = "Correct-Horse-Battery-Staple-1"
  password_version    = 1
}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("activedirectory_user.jdoe", "id"),
					resource.TestCheckResourceAttrSet("activedirectory_user.jdoe", "sid"),
					resource.TestCheckResourceAttr("activedirectory_user.jdoe", "dn",
						"CN=jdoe,OU=Staff,DC=corp,DC=local"),
					// The name defaults to the sAMAccountName.
					resource.TestCheckResourceAttr("activedirectory_user.jdoe", "name", "jdoe"),
					resource.TestCheckResourceAttr("activedirectory_user.jdoe", "enabled", "true"),
					// The password is never in state: this is the whole point.
					resource.TestCheckNoResourceAttr("activedirectory_user.jdoe", "password"),
				),
			},
			{
				Config: userBase + `
resource "activedirectory_user" "jdoe" {
  sam_account_name    = "jdoe"
  container           = activedirectory_ou.staff.dn
  user_principal_name = "jdoe@corp.local"
  display_name        = "John Doe"
  given_name          = "John"
  surname             = "Doe"
  enabled             = true
  password            = "Correct-Horse-Battery-Staple-1"
  password_version    = 1
}`,
				PlanOnly: true,
			},
			// Bumping the version rotates; the password itself cannot be
			// diffed, because it is never stored.
			{
				Config: userBase + `
resource "activedirectory_user" "jdoe" {
  sam_account_name        = "jdoe"
  container               = activedirectory_ou.staff.dn
  user_principal_name     = "jdoe@corp.local"
  display_name            = "John Doe"
  given_name              = "John"
  surname                 = ""
  enabled                 = false
  password                = "Rotated-P4ssw0rd-2"
  password_version        = 2
  account_expiration_date = "2027-01-02T03:04:05Z"
}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("activedirectory_user.jdoe", "enabled", "false"),
					resource.TestCheckResourceAttr("activedirectory_user.jdoe", "surname", ""),
					resource.TestCheckResourceAttr("activedirectory_user.jdoe", "password_version", "2"),
					resource.TestCheckResourceAttr("activedirectory_user.jdoe",
						"account_expiration_date", "2027-01-02T03:04:05Z"),
					func(*terraform.State) error {
						// Two passwords: the one from create and the rotation.
						for _, history := range dir.Passwords {
							if len(history) == 2 {
								return nil
							}
						}
						return fmt.Errorf("expected a rotation, got %v", dir.Passwords)
					},
				),
			},
			{
				ResourceName:      "activedirectory_user.jdoe",
				ImportState:       true,
				ImportStateId:     "jdoe", // by sAMAccountName, the brownfield case
				ImportStateVerify: true,
				// A write-only attribute and its version are not readable, so
				// they cannot round-trip through import.
				ImportStateVerifyIgnore: []string{"password", "password_version"},
			},
		},
	})
}

// Correctness rule 7, surfaced at plan rather than accepted by AD and
// surprising later.
func TestAccUserRejectsContradictoryFlagsAtApply(t *testing.T) {
	dir := fake.NewDirectory()
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_11_0)},
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps: []resource.TestStep{{
			Config: userBase + `
resource "activedirectory_user" "jdoe" {
  sam_account_name         = "jdoe"
  container                = activedirectory_ou.staff.dn
  change_password_at_logon = true
  password_expires         = false
}`,
			ExpectError: regexp.MustCompile(`change_password_at_logon`),
		}},
	})
}

// password_version without password is a configuration that silently does
// nothing on rotation, so it is refused at plan.
func TestAccUserVersionRequiresPassword(t *testing.T) {
	dir := fake.NewDirectory()
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_11_0)},
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps: []resource.TestStep{{
			Config: userBase + `
resource "activedirectory_user" "jdoe" {
  sam_account_name = "jdoe"
  container        = activedirectory_ou.staff.dn
  password_version = 3
}`,
			PlanOnly:    true,
			ExpectError: regexp.MustCompile(`password`),
		}},
	})
}
