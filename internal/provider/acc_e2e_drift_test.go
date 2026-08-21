package provider_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	adpwsh "github.com/nemethhh/go-adpwsh"
)

// Scenario B: a managed object is deleted / modified / renamed / moved directly
// in AD (outside Terraform), then plan detects the drift and apply reconciles.
// Every out-of-band act goes through the go-adpwsh client as svc_e2e_alpha — the
// library is the only place AD behaviour lives.

func driftAlpha() (user, pass string, e suiteEnv) {
	user = os.Getenv(envE2EAlphaUser)
	pass = os.Getenv(envE2EAlphaPass)
	return user, pass, e2eSuiteEnv(user, pass, e2eAlphaDN())
}

// Deleted out of band → Read returns not-found → RemoveResource → next apply
// recreates with a NEW objectGUID.
func TestAccE2EDriftOUDeleted(t *testing.T) {
	user, pass, e := driftAlpha()
	name := accNamePrefix + "drift-del"
	cfg := e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "d" {
  name      = %q
  container = %q
}`, name, e.Container)

	var guid1, guid2 string
	resource.Test(t, resource.TestCase{
		PreCheck:                 e2ePreCheck(t, envE2EAlphaUser, envE2EAlphaPass),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             e2eCheckDestroy(t, user, pass),
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check:  captureAttr("activedirectory_ou.d", "id", &guid1),
			},
			{
				PreConfig: func() {
					cl := e2eClient(t, user, pass)
					if err := cl.OU.Delete(context.Background(), adpwsh.ByGUID(guid1),
						adpwsh.DeleteOptions{Unprotect: true}); err != nil {
						t.Fatalf("out-of-band delete failed: %v", err)
					}
				},
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("activedirectory_ou.d", "id"),
					captureAttr("activedirectory_ou.d", "id", &guid2),
					func(*terraform.State) error {
						if guid2 == "" || guid2 == guid1 {
							return fmt.Errorf("expected a new GUID after recreate, got %q (was %q)", guid2, guid1)
						}
						return nil
					},
				),
			},
			{Config: cfg, PlanOnly: true},
		},
	})
}

// Attribute modified out of band → plan shows drift → apply restores it. GUID
// stable (in-place update).
func TestAccE2EDriftOUModified(t *testing.T) {
	user, pass, e := driftAlpha()
	name := accNamePrefix + "drift-mod"
	cfg := e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "m" {
  name        = %q
  container   = %q
  description = "managed"
}`, name, e.Container)

	var guid1, guid2 string
	resource.Test(t, resource.TestCase{
		PreCheck:                 e2ePreCheck(t, envE2EAlphaUser, envE2EAlphaPass),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             e2eCheckDestroy(t, user, pass),
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					captureAttr("activedirectory_ou.m", "id", &guid1),
					resource.TestCheckResourceAttr("activedirectory_ou.m", "description", "managed"),
				),
			},
			{
				PreConfig: func() {
					cl := e2eClient(t, user, pass)
					if _, err := cl.OU.Update(context.Background(), adpwsh.ByGUID(guid1),
						adpwsh.OUSpec{Name: name, Container: e.Container,
							Description: adpwsh.String("changed out of band")}); err != nil {
						t.Fatalf("out-of-band modify failed: %v", err)
					}
				},
				Config:             cfg,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true, // the drift is detected on refresh
			},
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("activedirectory_ou.m", "description", "managed"),
					captureAttr("activedirectory_ou.m", "id", &guid2),
					func(*terraform.State) error {
						if guid2 != guid1 {
							return fmt.Errorf("modify reconcile must be in-place: GUID changed %q -> %q", guid1, guid2)
						}
						return nil
					},
				),
			},
			{Config: cfg, PlanOnly: true},
		},
	})
}

// Renamed out of band (RDN changed) → plan shows drift → apply renames back.
// GUID stable.
func TestAccE2EDriftOURenamed(t *testing.T) {
	user, pass, e := driftAlpha()
	name := accNamePrefix + "drift-ren"
	cfg := e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "r" {
  name      = %q
  container = %q
}`, name, e.Container)

	var guid1, guid2 string
	resource.Test(t, resource.TestCase{
		PreCheck:                 e2ePreCheck(t, envE2EAlphaUser, envE2EAlphaPass),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             e2eCheckDestroy(t, user, pass),
		Steps: []resource.TestStep{
			{Config: cfg, Check: captureAttr("activedirectory_ou.r", "id", &guid1)},
			{
				PreConfig: func() {
					cl := e2eClient(t, user, pass)
					if _, err := cl.OU.Update(context.Background(), adpwsh.ByGUID(guid1),
						adpwsh.OUSpec{Name: name + "-oob", Container: e.Container}); err != nil {
						t.Fatalf("out-of-band rename failed: %v", err)
					}
				},
				Config:             cfg,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("activedirectory_ou.r", "name", name),
					resource.TestCheckResourceAttr("activedirectory_ou.r", "dn", "OU="+name+","+e.Container),
					// The GUID is captured and compared at check-time: guid1 is
					// still "" when the TestStep slice is built, so it cannot be
					// passed to TestCheckResourceAttr directly (that reads it eagerly).
					captureAttr("activedirectory_ou.r", "id", &guid2),
					func(*terraform.State) error {
						if guid2 != guid1 {
							return fmt.Errorf("rename reconcile must be in-place: GUID changed %q -> %q", guid1, guid2)
						}
						return nil
					},
				),
			},
			{Config: cfg, PlanOnly: true},
		},
	})
}

// Moved out of band (parent changed, within the delegated subtree) → plan shows
// drift → apply moves it back. GUID stable.
func TestAccE2EDriftOUMoved(t *testing.T) {
	user, pass, e := driftAlpha()
	parent := accNamePrefix + "drift-parent"
	child := accNamePrefix + "drift-child"
	cfg := e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "parent" {
  name      = %q
  container = %q
}
resource "activedirectory_ou" "child" {
  name      = %q
  container = %q
}`, parent, e.Container, child, e.Container)

	var childGUID, childGUID2, parentDN string
	resource.Test(t, resource.TestCase{
		PreCheck:                 e2ePreCheck(t, envE2EAlphaUser, envE2EAlphaPass),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             e2eCheckDestroy(t, user, pass),
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					captureAttr("activedirectory_ou.child", "id", &childGUID),
					captureAttr("activedirectory_ou.parent", "dn", &parentDN),
				),
			},
			{
				PreConfig: func() {
					cl := e2eClient(t, user, pass)
					// Move child under parent; config still wants it under the root.
					if _, err := cl.OU.Update(context.Background(), adpwsh.ByGUID(childGUID),
						adpwsh.OUSpec{Name: child, Container: parentDN}); err != nil {
						t.Fatalf("out-of-band move failed: %v", err)
					}
				},
				Config:             cfg,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("activedirectory_ou.child", "dn", "OU="+child+","+e.Container),
					// See TestAccE2EDriftOURenamed: compare the GUID at check-time,
					// because childGUID is empty when the TestStep slice is built.
					captureAttr("activedirectory_ou.child", "id", &childGUID2),
					func(*terraform.State) error {
						if childGUID2 != childGUID {
							return fmt.Errorf("move reconcile must be in-place: GUID changed %q -> %q", childGUID, childGUID2)
						}
						return nil
					},
				),
			},
			{Config: cfg, PlanOnly: true},
		},
	})
}

// A group attribute modified out of band → reconciled. Proves the group read-
// back mapping under drift, not only the OU's.
func TestAccE2EDriftGroupModified(t *testing.T) {
	user, pass, e := driftAlpha()
	ou := accNamePrefix + "drift-grp-ou"
	grp := accNamePrefix + "drift-grp"
	cfg := e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "staff" {
  name      = %q
  container = %q
}
resource "activedirectory_group" "g" {
  name             = %q
  sam_account_name = %q
  container        = activedirectory_ou.staff.dn
  description      = "managed"
}`, ou, e.Container, grp, grp)

	var guid1 string
	resource.Test(t, resource.TestCase{
		PreCheck:                 e2ePreCheck(t, envE2EAlphaUser, envE2EAlphaPass),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             e2eCheckDestroy(t, user, pass),
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					captureAttr("activedirectory_group.g", "id", &guid1),
					resource.TestCheckResourceAttr("activedirectory_group.g", "description", "managed"),
				),
			},
			{
				PreConfig: func() {
					cl := e2eClient(t, user, pass)
					ctx := context.Background()
					cur, err := cl.Group.Get(ctx, adpwsh.ByGUID(guid1))
					if err != nil {
						t.Fatalf("out-of-band get failed: %v", err)
					}
					if _, err := cl.Group.Update(ctx, adpwsh.ByGUID(guid1), adpwsh.GroupSpec{
						Name: cur.Name, SamAccountName: cur.SamAccountName, Container: cur.Container,
						Scope: cur.Scope, Category: cur.Category,
						Description: adpwsh.String("changed out of band"),
					}); err != nil {
						t.Fatalf("out-of-band group modify failed: %v", err)
					}
				},
				Config:             cfg,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: cfg,
				Check:  resource.TestCheckResourceAttr("activedirectory_group.g", "description", "managed"),
			},
			{Config: cfg, PlanOnly: true},
		},
	})
}

// A user attribute modified out of band → reconciled.
func TestAccE2EDriftUserModified(t *testing.T) {
	user, pass, e := driftAlpha()
	ou := accNamePrefix + "drift-usr-ou"
	sam := accNamePrefix + "drift-usr"
	upn := sam + "@" + e.upnSuffix()
	cfg := e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "staff" {
  name      = %q
  container = %q
}
resource "activedirectory_user" "u" {
  sam_account_name    = %q
  container           = activedirectory_ou.staff.dn
  user_principal_name = %q
  display_name        = "Managed Name"
  password            = "Correct-Horse-Battery-Staple-1"
  password_version    = 1
}`, ou, e.Container, sam, upn)

	var guid1 string
	resource.Test(t, resource.TestCase{
		PreCheck:                 e2ePreCheck(t, envE2EAlphaUser, envE2EAlphaPass),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             e2eCheckDestroy(t, user, pass),
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					captureAttr("activedirectory_user.u", "id", &guid1),
					resource.TestCheckResourceAttr("activedirectory_user.u", "display_name", "Managed Name"),
				),
			},
			{
				PreConfig: func() {
					cl := e2eClient(t, user, pass)
					ctx := context.Background()
					cur, err := cl.User.Get(ctx, adpwsh.ByGUID(guid1))
					if err != nil {
						t.Fatalf("out-of-band get failed: %v", err)
					}
					if _, err := cl.User.Update(ctx, adpwsh.ByGUID(guid1), adpwsh.UserSpec{
						SamAccountName: cur.SamAccountName, Container: cur.Container,
						DisplayName: adpwsh.String("Changed Out Of Band"),
					}); err != nil {
						t.Fatalf("out-of-band user modify failed: %v", err)
					}
				},
				Config:             cfg,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config: cfg,
				Check:  resource.TestCheckResourceAttr("activedirectory_user.u", "display_name", "Managed Name"),
			},
			{Config: cfg, PlanOnly: true},
		},
	})
}

// A managed membership edge removed out of band → Read (IsMember) returns false
// → RemoveResource → next apply re-adds it.
func TestAccE2EDriftGroupMemberRemoved(t *testing.T) {
	user, pass, e := driftAlpha()
	ou := accNamePrefix + "drift-gm-ou"
	grp := accNamePrefix + "drift-gm-grp"
	sam := accNamePrefix + "drift-gm-usr"
	upn := sam + "@" + e.upnSuffix()
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
}`, ou, e.Container, grp, grp, sam, upn)

	var groupID, memberID string
	resource.Test(t, resource.TestCase{
		PreCheck:                 e2ePreCheck(t, envE2EAlphaUser, envE2EAlphaPass),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             e2eCheckDestroy(t, user, pass),
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
					cl := e2eClient(t, user, pass)
					if err := cl.Group.RemoveMembers(context.Background(),
						adpwsh.ByGUID(groupID), []adpwsh.Identity{adpwsh.ByGUID(memberID)}); err != nil {
						t.Fatalf("out-of-band remove failed: %v", err)
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

// A member added out of band to an authoritatively-managed group → apply removes
// it, reconciling the group to exactly the configured set.
func TestAccE2EDriftGroupMembershipReconciled(t *testing.T) {
	user, pass, e := driftAlpha()
	ou := accNamePrefix + "drift-gs-ou"
	grp := accNamePrefix + "drift-gs-grp"
	a := accNamePrefix + "drift-gs-a"
	b := accNamePrefix + "drift-gs-b"
	upnA := a + "@" + e.upnSuffix()
	upnB := b + "@" + e.upnSuffix()
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
resource "activedirectory_user" "b" {
  sam_account_name    = %q
  container           = activedirectory_ou.staff.dn
  user_principal_name = %q
  password            = "Correct-Horse-Battery-Staple-1"
  password_version    = 1
}
resource "activedirectory_group_membership" "s" {
  group_id = activedirectory_group.g.id
  members  = [activedirectory_user.a.id]
}`, ou, e.Container, grp, grp, a, upnA, b, upnB)

	var groupID, intruderID string
	resource.Test(t, resource.TestCase{
		PreCheck:                 e2ePreCheck(t, envE2EAlphaUser, envE2EAlphaPass),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             e2eCheckDestroy(t, user, pass),
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					captureAttr("activedirectory_group.g", "id", &groupID),
					captureAttr("activedirectory_user.b", "id", &intruderID),
					resource.TestCheckResourceAttr("activedirectory_group_membership.s", "members.#", "1"),
				),
			},
			{
				PreConfig: func() {
					cl := e2eClient(t, user, pass)
					if err := cl.Group.AddMembers(context.Background(),
						adpwsh.ByGUID(groupID), []adpwsh.Identity{adpwsh.ByGUID(intruderID)}); err != nil {
						t.Fatalf("out-of-band add failed: %v", err)
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
