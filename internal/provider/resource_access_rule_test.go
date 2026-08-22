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
// activedirectory_access_rule — see
// examples/resources/activedirectory_access_rule/resource.tf and
// acc_e2e_delegation_test.go's TestAccE2EDelegationGrantsCapability. Each
// access_rule's applies_to comes from a rule's applies_to, a nested object
// read off a data source's Computed attribute; the framework represents that
// as an UNKNOWN object at the point the config decodes into the resource's
// model, before the data source itself is evaluated. Decoding an unknown
// object into a plain Go struct (the previous shape of
// accessRuleModel.AppliesTo) is exactly what terraform-plugin-framework
// cannot do. Confirmed by temporarily reverting the fix: this exact config
// fails "terraform plan" with
//
//	Error: Value Conversion Error
//	  with activedirectory_access_rule.grant0, ... applies_to = data.activedirectory_delegation_template.t.rules[0].applies_to
//	  Received unknown value, however the target type cannot handle unknown values.
//	  Path: applies_to  Target Type: provider.accessRuleAppliesTo  Suggested Type: basetypes.ObjectValue
//
// — byte-for-byte the error from the lab (originally reproduced via
// for_each's each.value.applies_to; the unknown-object shape is identical
// however the per-rule value reaches the resource). This version assigns
// rules[0]/rules[1] to two explicitly-indexed resources instead of fanning
// out with for_each: terraform-plugin-testing v1.16.0's legacy post-step
// state shim (state_shim.go's shimResourceStateKey) cannot address a
// string-keyed for_each instance at all ("unexpected index type (string) ...
// for_each is not supported") — a harness limitation, not a provider bug
// (confirmed on the lab, where the real apply succeeded). Two ordinary,
// non-indexed resource labels never hit that code path (res.Index stays
// nil), so a single step — plan, apply, and the TestCase's own final
// destroy — is enough.
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

	// reset_user_passwords expands to exactly two ACE specs (go-adpwsh's
	// delegation.go), so rules[0] and rules[1] cover the template in full.
	grant := `
resource "activedirectory_access_rule" "grant0" {
  target      = activedirectory_ou.t.dn
  trustee     = activedirectory_group.helpdesk.id
  rights      = data.activedirectory_delegation_template.t.rules[0].rights
  object_type = data.activedirectory_delegation_template.t.rules[0].object_type
  applies_to  = data.activedirectory_delegation_template.t.rules[0].applies_to
  type        = data.activedirectory_delegation_template.t.rules[0].type
}

resource "activedirectory_access_rule" "grant1" {
  target      = activedirectory_ou.t.dn
  trustee     = activedirectory_group.helpdesk.id
  rights      = data.activedirectory_delegation_template.t.rules[1].rights
  object_type = data.activedirectory_delegation_template.t.rules[1].object_type
  applies_to  = data.activedirectory_delegation_template.t.rules[1].applies_to
  type        = data.activedirectory_delegation_template.t.rules[1].type
}`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps: []resource.TestStep{{
			Config: base + grant,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("activedirectory_access_rule.grant0", "trustee_sid"),
				resource.TestCheckResourceAttrSet("activedirectory_access_rule.grant1", "trustee_sid"),
				resource.TestCheckResourceAttr("activedirectory_access_rule.grant0", "object_type", "Reset Password"),
				resource.TestCheckResourceAttr("activedirectory_access_rule.grant1", "object_type", "pwdLastSet"),
				resource.TestCheckResourceAttr("activedirectory_access_rule.grant0", "applies_to.scope", "descendants"),
				resource.TestCheckResourceAttr("activedirectory_access_rule.grant0", "applies_to.object_class", "user"),
				resource.TestCheckResourceAttr("activedirectory_access_rule.grant1", "applies_to.scope", "descendants"),
				resource.TestCheckResourceAttr("activedirectory_access_rule.grant1", "applies_to.object_class", "user"),
			),
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
