package provider_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	adpwsh "github.com/nemethhh/go-adpwsh"
)

// Scenario G: access-rule drift and non-authoritativeness. An access_rule
// manages exactly its own ACE. Out-of-band acts go through the go-adpwsh client
// as svc_e2e_alpha (who holds WriteDacl over OU=alpha), the same idiom the other
// drift scenarios use.

func aclDriftFixture(e suiteEnv, ou, grp string) string {
	return e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "t" {
  name                               = %[1]q
  container                          = %[2]q
  protected_from_accidental_deletion = false
}
resource "activedirectory_group" "helpdesk" {
  name             = %[3]q
  sam_account_name = %[3]q
  container        = activedirectory_ou.t.dn
}
resource "activedirectory_access_rule" "reset" {
  target      = activedirectory_ou.t.dn
  trustee     = activedirectory_group.helpdesk.id
  rights      = ["ExtendedRight"]
  object_type = "Reset Password"
  applies_to = {
    scope        = "descendants"
    object_class = "user"
  }
  type = "Allow"
}`, ou, e.Container, grp)
}

// TestAccE2EAccessRuleDriftRecreated: the managed ACE is revoked out of band ->
// Read finds it gone -> RemoveResource -> next apply re-grants it.
func TestAccE2EAccessRuleDriftRecreated(t *testing.T) {
	user, pass := os.Getenv(envE2EAlphaUser), os.Getenv(envE2EAlphaPass)
	e := e2eSuiteEnv(user, pass, e2eAlphaDN())
	cfg := aclDriftFixture(e, accNamePrefix+"acldrift-ou", accNamePrefix+"acldrift-grp")

	var targetDN string
	resource.Test(t, resource.TestCase{
		PreCheck:                 e2ePreCheck(t, envE2EAlphaUser, envE2EAlphaPass),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             e2eCheckDestroy(t, user, pass),
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("activedirectory_access_rule.reset", "id"),
					captureAttr("activedirectory_ou.t", "dn", &targetDN),
				),
			},
			{
				PreConfig: func() {
					cl := e2eClient(t, user, pass)
					ctx := context.Background()
					aces, err := cl.ACL.Get(ctx, adpwsh.ByDN(targetDN))
					if err != nil {
						t.Fatalf("out-of-band ACL.Get failed: %v", err)
					}
					var explicit []adpwsh.ACE
					for _, a := range aces {
						if !a.Inherited {
							explicit = append(explicit, a)
						}
					}
					if len(explicit) == 0 {
						t.Fatal("expected the managed ACE to be present before the out-of-band revoke")
					}
					if err := cl.ACL.Revoke(ctx, adpwsh.ByDN(targetDN), explicit); err != nil {
						t.Fatalf("out-of-band ACL.Revoke failed: %v", err)
					}
				},
				Config:             cfg,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true, // the revoke is detected on refresh
			},
			{Config: cfg}, // apply re-grants the ACE
			{Config: cfg, PlanOnly: true},
		},
	})
}

// TestAccE2EAccessRuleNonAuthoritative: an unrelated ACE is added out of band on
// the same target. The resource manages only its own ACE, so the plan stays
// clean — it must not try to remove the foreign ACE.
func TestAccE2EAccessRuleNonAuthoritative(t *testing.T) {
	user, pass := os.Getenv(envE2EAlphaUser), os.Getenv(envE2EAlphaPass)
	e := e2eSuiteEnv(user, pass, e2eAlphaDN())
	cfg := aclDriftFixture(e, accNamePrefix+"aclnonauth-ou", accNamePrefix+"aclnonauth-grp")

	var targetDN, trusteeSID string
	resource.Test(t, resource.TestCase{
		PreCheck:                 e2ePreCheck(t, envE2EAlphaUser, envE2EAlphaPass),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             e2eCheckDestroy(t, user, pass),
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					captureAttr("activedirectory_ou.t", "dn", &targetDN),
					captureAttr("activedirectory_group.helpdesk", "sid", &trusteeSID),
				),
			},
			{
				PreConfig: func() {
					cl := e2eClient(t, user, pass)
					// A GenericRead-on-this ACE: a distinct canonical key from the
					// managed ExtendedRight/descendants ACE, so it never collides.
					foreign := adpwsh.ACE{
						Trustee:     trusteeSID,
						Type:        adpwsh.ACEAllow,
						Rights:      []adpwsh.Right{"GenericRead"},
						Inheritance: adpwsh.InheritanceThis,
					}
					if err := cl.ACL.Grant(context.Background(), adpwsh.ByDN(targetDN),
						[]adpwsh.ACE{foreign}); err != nil {
						t.Fatalf("out-of-band ACL.Grant failed: %v", err)
					}
				},
				Config:   cfg,
				PlanOnly: true, // the managed ACE is untouched; the plan is clean
			},
		},
	})
}
