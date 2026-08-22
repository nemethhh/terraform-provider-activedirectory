package provider_test

import (
	"fmt"
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

// TestAccessRuleFromDelegationTemplateAgainstTheFake reproduces the exact
// failure shape found on the real domain (task B6): the headline usage of
// fanning a activedirectory_delegation_template data source into
// activedirectory_access_rule via for_each — see
// examples/resources/activedirectory_access_rule/resource.tf and
// acc_e2e_delegation_test.go's TestAccE2EDelegationGrantsCapability. Each
// access_rule instance's applies_to comes from each.value.applies_to, a
// nested object read off a data source's Computed attribute; the framework
// represents that as an UNKNOWN object at the point the config decodes into
// the resource's model, before the data source itself is evaluated. Decoding
// an unknown object into a plain Go struct (the previous shape of
// accessRuleModel.AppliesTo) is exactly what terraform-plugin-framework
// cannot do. Confirmed by temporarily reverting the fix: this exact config
// fails "terraform plan" with
//
//	Error: Value Conversion Error
//	  with activedirectory_access_rule.grant, ... applies_to = each.value.applies_to
//	  Received unknown value, however the target type cannot handle unknown values.
//	  Path: applies_to  Target Type: provider.accessRuleAppliesTo  Suggested Type: basetypes.ObjectValue
//
// — byte-for-byte the error from the lab. Step 1 (no Check) is where that
// failure occurred; it must plan and apply cleanly now. Step 2 tears the
// for_each'd instances back down before the TestCase ends: resource.Test's
// own post-test state retrieval (independent of any Check) shims state
// through a legacy path that cannot address a for_each (string-keyed)
// resource instance at all ("unexpected index type ... for_each is not
// supported", state_shim.go) — unrelated to this bug, but it means a
// for_each'd resource cannot be left in final state when driving this
// version of terraform-plugin-testing, so this second step (rather than an
// instance-indexed Check) is how the test proves the two grants existed and
// then cleans them up without tripping that harness limitation.
func TestAccessRuleFromDelegationTemplateAgainstTheFake(t *testing.T) {
	dir := fake.NewDirectory()
	ou := accNamePrefix + "ar-tmpl-ou"
	grp := accNamePrefix + "ar-tmpl-grp"

	base := providerConfig + fmt.Sprintf(`
resource "activedirectory_ou" "t" {
  name      = %q
  container = "DC=corp,DC=local"
}
resource "activedirectory_group" "helpdesk" {
  name             = %q
  sam_account_name = %q
  container        = activedirectory_ou.t.dn
}

data "activedirectory_delegation_template" "t" {
  task = "reset_user_passwords"
}
`, ou, grp, grp)

	grant := `
resource "activedirectory_access_rule" "grant" {
  for_each    = { for i, x in data.activedirectory_delegation_template.t.rules : tostring(i) => x }
  target      = activedirectory_ou.t.dn
  trustee     = activedirectory_group.helpdesk.id
  rights      = each.value.rights
  object_type = each.value.object_type
  applies_to  = each.value.applies_to
  type        = each.value.type
}`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps: []resource.TestStep{
			{Config: base + grant},
			{Config: base},
		},
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
