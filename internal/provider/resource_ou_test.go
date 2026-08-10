package provider_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/nemethhh/go-adpwsh/transport/fake"
)

func TestAccOULifecycle(t *testing.T) {
	dir := fake.NewDirectory()
	factories := factoriesWith(dir)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{
				Config: providerConfig + `
resource "activedirectory_ou" "staff" {
  name        = "Staff"
  container   = "DC=corp,DC=local"
  description = "The staff OU"
}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("activedirectory_ou.staff", "id"),
					resource.TestCheckResourceAttr("activedirectory_ou.staff", "dn", "OU=Staff,DC=corp,DC=local"),
					resource.TestCheckResourceAttr("activedirectory_ou.staff", "description", "The staff OU"),
					// AD's own default, mirrored rather than silently inverted.
					resource.TestCheckResourceAttr("activedirectory_ou.staff",
						"protected_from_accidental_deletion", "true"),
				),
			},
			// A no-change apply must produce no diff. This is the check that
			// catches a DN echoed back in different case or spacing.
			{
				Config: providerConfig + `
resource "activedirectory_ou" "staff" {
  name        = "Staff"
  container   = "dc=CORP,dc=local"
  description = "The staff OU"
}`,
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
			},
			// Rename and move in place: the ID must survive, because deleting
			// and recreating an AD object destroys its SID.
			{
				Config: providerConfig + `
resource "activedirectory_ou" "parent" {
  name      = "HQ"
  container = "DC=corp,DC=local"
}
resource "activedirectory_ou" "staff" {
  name        = "People"
  container   = activedirectory_ou.parent.dn
  description = ""
}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("activedirectory_ou.staff", "dn",
						"OU=People,OU=HQ,DC=corp,DC=local"),
					resource.TestCheckResourceAttr("activedirectory_ou.staff", "description", ""),
				),
			},
			{
				ResourceName:      "activedirectory_ou.staff",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

// Importing by DN must work as well as importing by GUID. The object is
// seeded rather than applied, which is the brownfield case: there is no prior
// state to verify against, so the imported attributes are asserted directly.
func TestAccOUImportByDN(t *testing.T) {
	dir := fake.NewDirectory()
	guid := dir.Seed("organizationalUnit", "Existing", "DC=corp,DC=local", map[string]any{
		"description": "adopted", "protected": true,
	})
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps: []resource.TestStep{
			{
				Config: providerConfig + `
resource "activedirectory_ou" "existing" {
  name        = "Existing"
  container   = "DC=corp,DC=local"
  description = "adopted"
}`,
				ResourceName:       "activedirectory_ou.existing",
				ImportState:        true,
				ImportStateId:      "OU=Existing,DC=corp,DC=local",
				ImportStatePersist: true,
				ImportStateCheck: composeImportStateCheck(
					// The DN went in; the GUID is what state holds.
					checkImportedAttr("id", guid),
					checkImportedAttr("dn", "OU=Existing,DC=corp,DC=local"),
					checkImportedAttr("name", "Existing"),
					checkImportedAttr("container", "DC=corp,DC=local"),
					checkImportedAttr("description", "adopted"),
					checkImportedAttr("protected_from_accidental_deletion", "true"),
				),
			},
		},
	})
}

// Creating over an existing object must hand back a ready-to-paste import
// block rather than only naming the conflict.
func TestAccOUAlreadyExistsSuggestsImport(t *testing.T) {
	dir := fake.NewDirectory()
	dir.Seed("organizationalUnit", "Staff", "DC=corp,DC=local", map[string]any{
		"description": "", "protected": true,
	})
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps: []resource.TestStep{{
			Config: providerConfig + `
resource "activedirectory_ou" "staff" {
  name      = "Staff"
  container = "DC=corp,DC=local"
}`,
			ExpectError: regexp.MustCompile(`import \{`),
		}},
	})
}
