package provider_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/nemethhh/go-adpwsh/transport/fake"
)

func TestAccessRuleLifecycleAgainstTheFake(t *testing.T) {
	dir := fake.NewDirectory()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps:                    accessRuleSteps(fakeSuiteEnv()),
	})
}

// TestAccessRuleRejectsObjectClassWithScopeThis pins the config validator: with
// applies_to.scope = "this" (InheritanceThis -> .NET "None"), an
// inheritedObjectType is not meaningful and a real DC will not persist or
// report one, so Read would read back "" and mismatch the configured
// object_class forever (RemoveResource -> recreate on every apply). The
// validator must catch this at plan time, before Create ever reaches the
// fake, so an empty directory (no seeded target/trustee) is enough.
func TestAccessRuleRejectsObjectClassWithScopeThis(t *testing.T) {
	dir := fake.NewDirectory()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps: []resource.TestStep{{
			Config: fakeSuiteEnv().ProviderConfig + `
resource "activedirectory_access_rule" "bad" {
  target  = "OU=Sales,DC=corp,DC=local"
  trustee = "S-1-5-21-1-2-3-4321"
  rights  = ["GenericAll"]
  applies_to = {
    scope        = "this"
    object_class = "user"
  }
}`,
			ExpectError: regexp.MustCompile("only meaningful when"),
		}},
	})
}

func TestAccAccessRuleLifecycle(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 accPreCheck(t),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             accCheckDestroy(t),
		Steps:                    accessRuleSteps(accSuiteEnv()),
	})
}
