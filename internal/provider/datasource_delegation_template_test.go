package provider_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/nemethhh/go-adpwsh/transport/fake"
)

func TestDelegationTemplateAgainstTheFake(t *testing.T) {
	dir := fake.NewDirectory()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps: []resource.TestStep{{
			Config: providerConfig + `
data "activedirectory_delegation_template" "reset" {
  task = "reset_user_passwords"
}
`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.activedirectory_delegation_template.reset", "rules.#", "2"),
				resource.TestCheckResourceAttr("data.activedirectory_delegation_template.reset", "rules.0.object_type", "Reset Password"),
				resource.TestCheckResourceAttr("data.activedirectory_delegation_template.reset", "rules.0.applies_to.object_class", "user"),
				resource.TestCheckResourceAttr("data.activedirectory_delegation_template.reset", "rules.0.rights.0", "ExtendedRight"),
			),
		}},
	})
}
