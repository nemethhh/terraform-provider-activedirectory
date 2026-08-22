package provider_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Scenario F: reorganisation. A realistic org is built in one apply, then
// reorganised. Moving objects between OUs is an in-place update in AD, so every
// objectGUID and SID must survive and the group membership and access rule that
// name the moved objects must remain intact. This is the end-to-end proof of the
// "nothing forces a replace" invariant: a delete-and-recreate would mint a new
// SID and drop every ACL naming the object.

// reorgIDs captures the identifiers a reorg must preserve.
type reorgIDs struct {
	teamGUID, devsGUID, devsSID, aliceGUID, aliceSID string
}

func reorgCaptures(ids *reorgIDs) resource.TestCheckFunc {
	return resource.ComposeAggregateTestCheckFunc(
		captureAttr("activedirectory_ou.team", "id", &ids.teamGUID),
		captureAttr("activedirectory_group.devs", "id", &ids.devsGUID),
		captureAttr("activedirectory_group.devs", "sid", &ids.devsSID),
		captureAttr("activedirectory_user.alice", "id", &ids.aliceGUID),
		captureAttr("activedirectory_user.alice", "sid", &ids.aliceSID),
	)
}

func reorgStable(ids *reorgIDs) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		checks := []struct{ name, was, addr, attr string }{
			{"team GUID", ids.teamGUID, "activedirectory_ou.team", "id"},
			{"devs GUID", ids.devsGUID, "activedirectory_group.devs", "id"},
			{"devs SID", ids.devsSID, "activedirectory_group.devs", "sid"},
			{"alice GUID", ids.aliceGUID, "activedirectory_user.alice", "id"},
			{"alice SID", ids.aliceSID, "activedirectory_user.alice", "sid"},
		}
		for _, c := range checks {
			rs, ok := s.RootModule().Resources[c.addr]
			if !ok || rs.Primary == nil {
				return fmt.Errorf("reorg: %s not in state", c.addr)
			}
			if got := rs.Primary.Attributes[c.attr]; got != c.was {
				return fmt.Errorf("reorg must be in-place: %s changed %q -> %q", c.name, c.was, got)
			}
		}
		return nil
	}
}

// reorgOrg renders the org fixture. teamContainer is the HCL expression for the
// team OU's parent, and objContainer is the expression for the devs/alice
// parent — passing these lets one template express both the leaf-move and the
// subtree-move shapes.
func reorgOrg(e suiteEnv, dept, dept2, team, bench, devs, alice string, teamContainer, objContainer string) string {
	upn := alice + "@" + e.upnSuffix()
	return e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "dept" {
  name                               = %[1]q
  container                          = %[2]q
  protected_from_accidental_deletion = false
}
resource "activedirectory_ou" "dept2" {
  name                               = %[3]q
  container                          = %[2]q
  protected_from_accidental_deletion = false
}
resource "activedirectory_ou" "team" {
  name                               = %[4]q
  container                          = %[5]s
  protected_from_accidental_deletion = false
}
resource "activedirectory_ou" "bench" {
  name                               = %[6]q
  container                          = activedirectory_ou.dept.dn
  protected_from_accidental_deletion = false
}
resource "activedirectory_group" "devs" {
  name             = %[7]q
  sam_account_name = %[7]q
  container        = %[8]s
}
resource "activedirectory_user" "alice" {
  sam_account_name    = %[9]q
  container           = %[8]s
  user_principal_name = %[10]q
  password            = "Correct-Horse-Battery-Staple-1"
  password_version    = 1
}
resource "activedirectory_group_membership" "devs" {
  group_id = activedirectory_group.devs.id
  members  = [activedirectory_user.alice.id]
}
resource "activedirectory_access_rule" "helpdesk" {
  target      = activedirectory_ou.dept.dn
  trustee     = activedirectory_group.devs.id
  rights      = ["ExtendedRight"]
  object_type = "Reset Password"
  applies_to = {
    scope        = "descendants"
    object_class = "user"
  }
  type = "Allow"
}`, dept, e.Container, dept2, team, teamContainer, bench, devs, objContainer, alice, upn)
}

// TestAccE2EReorgLeafMove moves the group and user directly from team into a
// sibling OU (bench). team stays put; only the two leaf objects move. This is
// the robust core proof — no parent-cascade ordering is involved.
func TestAccE2EReorgLeafMove(t *testing.T) {
	user, pass := os.Getenv(envE2EAlphaUser), os.Getenv(envE2EAlphaPass)
	e := e2eSuiteEnv(user, pass, e2eAlphaDN())
	dept := accNamePrefix + "reorg-dept"
	dept2 := accNamePrefix + "reorg-dept2"
	team := accNamePrefix + "reorg-team"
	bench := accNamePrefix + "reorg-bench"
	devs := accNamePrefix + "reorg-devs"
	alice := accNamePrefix + "reorg-alice"

	// Step 1: devs/alice live under team.
	before := reorgOrg(e, dept, dept2, team, bench, devs, alice,
		"activedirectory_ou.dept.dn", "activedirectory_ou.team.dn")
	// Step 2: devs/alice move under bench (team unchanged).
	after := reorgOrg(e, dept, dept2, team, bench, devs, alice,
		"activedirectory_ou.dept.dn", "activedirectory_ou.bench.dn")

	var ids reorgIDs
	resource.Test(t, resource.TestCase{
		PreCheck:                 e2ePreCheck(t, envE2EAlphaUser, envE2EAlphaPass),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             e2eCheckDestroy(t, user, pass),
		Steps: []resource.TestStep{
			{
				Config: before,
				Check: resource.ComposeAggregateTestCheckFunc(
					reorgCaptures(&ids),
					resource.TestCheckResourceAttr("activedirectory_group_membership.devs", "members.#", "1"),
					resource.TestCheckResourceAttrSet("activedirectory_access_rule.helpdesk", "trustee_sid"),
				),
			},
			{
				Config: after,
				Check: resource.ComposeAggregateTestCheckFunc(
					reorgStable(&ids),
					// bench sits under dept, so its DN is not e.dn("OU="+bench);
					// compare against the OU's own computed dn instead.
					resource.TestCheckResourceAttrPair("activedirectory_group.devs", "container", "activedirectory_ou.bench", "dn"),
					resource.TestCheckResourceAttrPair("activedirectory_user.alice", "container", "activedirectory_ou.bench", "dn"),
					resource.TestCheckResourceAttr("activedirectory_group_membership.devs", "members.#", "1"),
					resource.TestCheckResourceAttrSet("activedirectory_access_rule.helpdesk", "trustee_sid"),
				),
			},
			{Config: after, PlanOnly: true},
		},
	})
}

// TestAccE2EReorgSubtree moves the team OU itself under dept2, so the group and
// user (whose container references team.dn) cascade with it. This is the
// ambitious case: AD relocates team's children automatically, and Terraform's
// own move of devs/alice to the new team.dn must be idempotent. If this proves
// flaky on the lab, TestAccE2EReorgLeafMove is the fallback proof; see the spec
// risk note.
func TestAccE2EReorgSubtree(t *testing.T) {
	user, pass := os.Getenv(envE2EAlphaUser), os.Getenv(envE2EAlphaPass)
	e := e2eSuiteEnv(user, pass, e2eAlphaDN())
	dept := accNamePrefix + "reorgs-dept"
	dept2 := accNamePrefix + "reorgs-dept2"
	team := accNamePrefix + "reorgs-team"
	bench := accNamePrefix + "reorgs-bench"
	devs := accNamePrefix + "reorgs-devs"
	alice := accNamePrefix + "reorgs-alice"

	// devs/alice always reference team.dn; only team's parent changes.
	before := reorgOrg(e, dept, dept2, team, bench, devs, alice,
		"activedirectory_ou.dept.dn", "activedirectory_ou.team.dn")
	after := reorgOrg(e, dept, dept2, team, bench, devs, alice,
		"activedirectory_ou.dept2.dn", "activedirectory_ou.team.dn")

	var ids reorgIDs
	resource.Test(t, resource.TestCase{
		PreCheck:                 e2ePreCheck(t, envE2EAlphaUser, envE2EAlphaPass),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             e2eCheckDestroy(t, user, pass),
		Steps: []resource.TestStep{
			{
				Config: before,
				Check:  reorgCaptures(&ids),
			},
			{
				Config: after,
				Check: resource.ComposeAggregateTestCheckFunc(
					reorgStable(&ids),
					resource.TestCheckResourceAttr("activedirectory_ou.team", "container", e.dn("OU="+dept2)),
					resource.TestCheckResourceAttr("activedirectory_group_membership.devs", "members.#", "1"),
					resource.TestCheckResourceAttrSet("activedirectory_access_rule.helpdesk", "trustee_sid"),
				),
			},
			{Config: after, PlanOnly: true},
		},
	})
}
