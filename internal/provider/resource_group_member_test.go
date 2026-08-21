package provider_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	adpwsh "github.com/nemethhh/go-adpwsh"
	"github.com/nemethhh/go-adpwsh/transport/fake"
)

func TestGroupMemberLifecycleAgainstTheFake(t *testing.T) {
	dir := fake.NewDirectory()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps:                    groupMemberSteps(fakeSuiteEnv()),
	})
}

// TestGroupMemberDriftAgainstTheFake proves Read reconciles an edge removed out
// of band: a second client over the same directory removes the membership, and
// the next plan is non-empty (Terraform will re-add it).
func TestGroupMemberDriftAgainstTheFake(t *testing.T) {
	dir := fake.NewDirectory()
	oob, err := adpwsh.New(context.Background(), adpwsh.Config{Transport: dir.Transport()})
	if err != nil {
		t.Fatalf("out-of-band client: %v", err)
	}
	e := fakeSuiteEnv()
	ou := accNamePrefix + "gmd-ou"
	grp := accNamePrefix + "gmd-grp"
	usr := accNamePrefix + "gmd-usr"
	upn := usr + "@" + e.upnSuffix()
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
resource "activedirectory_user" "u" {
  sam_account_name    = %q
  container           = activedirectory_ou.staff.dn
  user_principal_name = %q
  password            = "Correct-Horse-Battery-Staple-1"
  password_version    = 1
}
resource "activedirectory_group_member" "m" {
  group_id  = activedirectory_group.g.id
  member_id = activedirectory_user.u.id
}`, ou, e.Container, grp, grp, usr, upn)

	var groupID, memberID string
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					captureAttr("activedirectory_group.g", "id", &groupID),
					captureAttr("activedirectory_user.u", "id", &memberID),
				),
			},
			{
				PreConfig: func() {
					if err := oob.Group.RemoveMembers(context.Background(),
						adpwsh.ByGUID(groupID), []adpwsh.Identity{adpwsh.ByGUID(memberID)}); err != nil {
						t.Fatalf("out-of-band remove: %v", err)
					}
				},
				Config:             cfg,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{Config: cfg},
			{Config: cfg, PlanOnly: true},
		},
	})
}

func TestAccGroupMemberLifecycle(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 accPreCheck(t),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             accCheckDestroy(t),
		Steps:                    groupMemberSteps(accSuiteEnv()),
	})
}

func TestGroupMemberNestedAgainstTheFake(t *testing.T) {
	dir := fake.NewDirectory()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps:                    groupMemberNestedSteps(fakeSuiteEnv()),
	})
}

func TestAccGroupMemberNested(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 accPreCheck(t),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             accCheckDestroy(t),
		Steps:                    groupMemberNestedSteps(accSuiteEnv()),
	})
}
