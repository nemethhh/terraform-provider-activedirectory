package provider_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	adpwsh "github.com/nemethhh/go-adpwsh"
	"github.com/nemethhh/go-adpwsh/transport/fake"
)

func TestGroupMembershipLifecycleAgainstTheFake(t *testing.T) {
	dir := fake.NewDirectory()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps:                    groupMembershipSteps(fakeSuiteEnv()),
	})
}

// TestGroupMembershipDriftAgainstTheFake proves the authoritative resource
// removes an out-of-band addition: a second client adds a third member, and the
// next apply reconciles the group back to exactly the configured set.
func TestGroupMembershipDriftAgainstTheFake(t *testing.T) {
	dir := fake.NewDirectory()
	oob, err := adpwsh.New(context.Background(), adpwsh.Config{Transport: dir.Transport()})
	if err != nil {
		t.Fatalf("out-of-band client: %v", err)
	}
	// Seed a third user directly, to be added out of band.
	intruder := dir.Seed("user", "tfacc-gsd-intruder", "DC=corp,DC=local",
		map[string]any{"sid": "S-1-5-21-1-2-3-9001", "samAccountName": "tfacc-gsd-intruder"})

	e := fakeSuiteEnv()
	ou := accNamePrefix + "gsd-ou"
	grp := accNamePrefix + "gsd-grp"
	a := accNamePrefix + "gsd-a"
	upnA := a + "@" + e.upnSuffix()
	cfg := e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "staff" {
  name      = %q
  container = %q
}
resource "activedirectory_group" "g" {
  name             = %q
  sam_account_name = %q
  container        = activedirectory_ou.staff.dn
}
resource "activedirectory_user" "a" {
  sam_account_name    = %q
  container           = activedirectory_ou.staff.dn
  user_principal_name = %q
  password            = "Correct-Horse-Battery-Staple-1"
  password_version    = 1
}
resource "activedirectory_group_membership" "s" {
  group_id = activedirectory_group.g.id
  members  = [activedirectory_user.a.id]
}`, ou, e.Container, grp, grp, a, upnA)

	var groupID string
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					captureAttr("activedirectory_group.g", "id", &groupID),
					resource.TestCheckResourceAttr("activedirectory_group_membership.s", "members.#", "1"),
				),
			},
			{
				PreConfig: func() {
					if err := oob.Group.AddMembers(context.Background(),
						adpwsh.ByGUID(groupID), []adpwsh.Identity{adpwsh.ByGUID(intruder)}); err != nil {
						t.Fatalf("out-of-band add: %v", err)
					}
				},
				Config:             cfg,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: cfg,
				Check:  resource.TestCheckResourceAttr("activedirectory_group_membership.s", "members.#", "1"),
			},
			{Config: cfg, PlanOnly: true},
		},
	})
}

func TestAccGroupMembershipLifecycle(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 accPreCheck(t),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             accCheckDestroy(t),
		Steps:                    groupMembershipSteps(accSuiteEnv()),
	})
}
