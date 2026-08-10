package provider_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/nemethhh/go-adpwsh/transport/fake"
)

// Each value below broke the archived provider at least once. They travel as
// JSON on stdin and are splatted into the cmdlet, so there is no escaping
// layer to get wrong.
func TestAccHostileInputRoundTrips(t *testing.T) {
	cases := []struct{ name, value string }{
		{"underscore", "under_score"},
		{"double_quote", `has "quotes"`},
		{"single_quote", `O'Brien`},
		{"dollar", `$env:PATH`},
		{"backtick", "back`tick"},
		{"semicolon", "semi;colon"},
		{"ampersand", "amper&sand"},
		{"pipe", "pipe|char"},
		{"comma", "Smith, John"},
		{"non_ascii", "söüäß-éòñ"},
		{"subexpression", `$(Get-Process)`},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			dir := fake.NewDirectory()
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factoriesWith(dir),
				Steps: []resource.TestStep{{
					Config: fmt.Sprintf(providerConfig+`
resource "activedirectory_ou" "staff" {
  name        = "Staff"
  container   = "DC=corp,DC=local"
  description = %q
}`, tt.value),
					Check: resource.TestCheckResourceAttr("activedirectory_ou.staff", "description", tt.value),
				}},
			})
		})
	}
}

// A DN whose RDN contains an escaped comma must survive being used as another
// resource's container, because that is where a string-splitting parser fails.
func TestAccHostileInputEscapedCommaInADN(t *testing.T) {
	dir := fake.NewDirectory()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps: []resource.TestStep{{
			Config: providerConfig + `
resource "activedirectory_ou" "sales" {
  name      = "Sales, EMEA"
  container = "DC=corp,DC=local"
}
resource "activedirectory_group" "reps" {
  name             = "Reps"
  sam_account_name = "reps"
  container        = activedirectory_ou.sales.dn
}`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("activedirectory_ou.sales", "dn",
					`OU=Sales\, EMEA,DC=corp,DC=local`),
				resource.TestCheckResourceAttr("activedirectory_group.reps", "container",
					`OU=Sales\, EMEA,DC=corp,DC=local`),
			),
		}},
	})
}
