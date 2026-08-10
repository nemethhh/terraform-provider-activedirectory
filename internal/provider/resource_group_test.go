package provider_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/nemethhh/go-adpwsh/transport/fake"
)

const groupBase = providerConfig + `
resource "activedirectory_ou" "staff" {
  name      = "Staff"
  container = "DC=corp,DC=local"
}
`

func TestAccGroupLifecycle(t *testing.T) {
	dir := fake.NewDirectory()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps: []resource.TestStep{
			{
				Config: groupBase + `
resource "activedirectory_group" "devs" {
  name             = "Developers"
  sam_account_name = "developers"
  container        = activedirectory_ou.staff.dn
}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("activedirectory_group.devs", "id"),
					resource.TestCheckResourceAttrSet("activedirectory_group.devs", "sid"),
					resource.TestCheckResourceAttr("activedirectory_group.devs", "dn",
						"CN=Developers,OU=Staff,DC=corp,DC=local"),
					// The defaults mirror the cmdlet's own.
					resource.TestCheckResourceAttr("activedirectory_group.devs", "scope", "global"),
					resource.TestCheckResourceAttr("activedirectory_group.devs", "category", "security"),
				),
			},
			{
				Config: groupBase + `
resource "activedirectory_group" "devs" {
  name             = "Developers"
  sam_account_name = "developers"
  container        = activedirectory_ou.staff.dn
}`,
				PlanOnly: true,
			},
			{
				Config: groupBase + `
resource "activedirectory_group" "devs" {
  name             = "Engineers"
  sam_account_name = "engineers"
  container        = activedirectory_ou.staff.dn
  scope            = "universal"
  description      = "Everyone who writes code"
  managed_by       = "CN=Alice,OU=Staff,DC=corp,DC=local"
}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("activedirectory_group.devs", "scope", "universal"),
					resource.TestCheckResourceAttr("activedirectory_group.devs", "sam_account_name", "engineers"),
					resource.TestCheckResourceAttr("activedirectory_group.devs", "dn",
						"CN=Engineers,OU=Staff,DC=corp,DC=local"),
				),
			},
			{
				ResourceName:      "activedirectory_group.devs",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

// The enums are validated in the schema, so a typo is a plan error naming the
// attribute rather than an opaque AD failure at apply.
func TestAccGroupRejectsAnUnknownScopeAtPlan(t *testing.T) {
	dir := fake.NewDirectory()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps: []resource.TestStep{{
			Config: groupBase + `
resource "activedirectory_group" "devs" {
  name             = "Developers"
  sam_account_name = "developers"
  container        = activedirectory_ou.staff.dn
  scope            = "worldwide"
}`,
			PlanOnly:    true,
			ExpectError: regexp.MustCompile(`(?s)scope.*worldwide`),
		}},
	})
}
