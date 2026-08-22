package provider_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	adpwsh "github.com/nemethhh/go-adpwsh"
)

// Scenario E: delegation. Everything Terraform manages in this file runs as a
// single principal per TestCase, like every other e2e scenario — the delegation
// half of each proof goes through an out-of-band client (e2eClient), the same
// idiom acc_e2e_drift_test.go uses for actions taken as someone other than the
// TestCase's own provider identity.

// TestAccE2EDelegationGrantsCapability proves an activedirectory_access_rule
// grant actually changes what a principal can do, not merely that it plans
// clean. svc_e2e_alpha holds Full Control — including WriteDacl — over OU=alpha
// and everything beneath it (LAB.md's fixture table), which is what lets alpha
// play the delegating owner here without an admin credential, mirroring
// LAB.md's "make lab-e2e needs no admin credentials". svc_e2e_limited starts
// with no rights whatsoever over OU=alpha.
//
// The capability itself is proved with client.User.SetPassword — the exact
// call resource_user.go's Update makes for a password_version bump — invoked
// directly as svc_e2e_limited before and after the grant. This suite has no
// precedent for switching a TestStep's provider credential mid-TestCase (every
// existing e2e scenario runs Terraform as one identity throughout), so the
// out-of-band call is used instead of a second Terraform apply run as the
// limited principal; see the B4 report for why, and consider upgrading this to
// a literal Terraform-apply-as-limited step once it has run on the lab.
func TestAccE2EDelegationGrantsCapability(t *testing.T) {
	alphaUser, alphaPass := os.Getenv(envE2EAlphaUser), os.Getenv(envE2EAlphaPass)
	limitedUser, limitedPass := os.Getenv(envE2ELimitedUser), os.Getenv(envE2ELimitedPass)
	e := e2eSuiteEnv(alphaUser, alphaPass, e2eAlphaDN())
	trustee := e2eBareSAM(limitedUser)

	ou := accNamePrefix + "deleg-ou"
	usr := accNamePrefix + "deleg-usr"
	upn := usr + "@" + e.upnSuffix()

	base := e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "cap" {
  name      = %q
  container = %q
}
resource "activedirectory_user" "target" {
  sam_account_name    = %q
  container           = activedirectory_ou.cap.dn
  user_principal_name = %q
  password            = "Correct-Horse-Battery-Staple-1"
  password_version    = 1
}`, ou, e.Container, usr, upn)

	// Feeds the reset_user_passwords template into access_rule by explicit,
	// indexed blocks rather than for_each. for_each is documented (and works
	// fine against a real Terraform apply — datasource_delegation_template.go's
	// schema doc still recommends it), but terraform-plugin-testing v1.16.0's
	// legacy post-step state shim (state_shim.go's shimResourceStateKey) cannot
	// address a string-keyed for_each instance at all: "unexpected index type
	// (string) for ... for_each is not supported". That is a harness
	// limitation, not a provider bug — confirmed on the lab, where the apply
	// itself succeeded. reset_user_passwords expands to exactly two ACE specs
	// (go-adpwsh's delegation.go), so two indexed resources cover the template
	// in full while keeping res.Index nil for both (see state_shim.go: the
	// error path only triggers when Index is non-nil). This still exercises
	// the applies_to-from-computed-data (unknown-at-plan) path the 6544b45 fix
	// handles: rules[N] is known at plan (the data source is pure computation
	// with no unknown inputs), but each rule's applies_to is itself a nested
	// object read off a data source's Computed attribute, decoded into the
	// resource's types.Object model field exactly as for_each's
	// each.value.applies_to was.
	grant := fmt.Sprintf(`
data "activedirectory_delegation_template" "reset" {
  task = "reset_user_passwords"
}

resource "activedirectory_access_rule" "grant0" {
  target      = activedirectory_ou.cap.dn
  trustee     = %[1]q
  rights      = data.activedirectory_delegation_template.reset.rules[0].rights
  object_type = data.activedirectory_delegation_template.reset.rules[0].object_type
  applies_to  = data.activedirectory_delegation_template.reset.rules[0].applies_to
  type        = data.activedirectory_delegation_template.reset.rules[0].type
}

resource "activedirectory_access_rule" "grant1" {
  target      = activedirectory_ou.cap.dn
  trustee     = %[1]q
  rights      = data.activedirectory_delegation_template.reset.rules[1].rights
  object_type = data.activedirectory_delegation_template.reset.rules[1].object_type
  applies_to  = data.activedirectory_delegation_template.reset.rules[1].applies_to
  type        = data.activedirectory_delegation_template.reset.rules[1].type
}`, trustee)

	var userGUID string
	newPassword := adpwsh.NewSecret("Correct-Horse-Battery-Staple-2")

	resource.Test(t, resource.TestCase{
		PreCheck: e2ePreCheck(t, envE2EAlphaUser, envE2EAlphaPass,
			envE2ELimitedUser, envE2ELimitedPass),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             e2eCheckDestroy(t, alphaUser, alphaPass),
		Steps: []resource.TestStep{
			{
				Config: base,
				Check:  captureAttr("activedirectory_user.target", "id", &userGUID),
			},
			{
				// Before the grant exists: svc_e2e_limited holds no rights
				// over OU=alpha, so the exact call a password_version bump
				// makes must be denied.
				PreConfig: func() {
					limited := e2eClient(t, limitedUser, limitedPass)
					err := limited.User.SetPassword(context.Background(), adpwsh.ByGUID(userGUID), newPassword)
					if err == nil {
						t.Fatal("svc_e2e_limited reset the password before any grant existed; " +
							"the lab fixture no longer isolates OU=alpha as expected")
					}
					if !errors.Is(err, adpwsh.ErrDenied) {
						t.Fatalf("expected a denied classification before the grant, got: %v", err)
					}
				},
				Config: base + grant,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("activedirectory_access_rule.grant0", "trustee_sid"),
					resource.TestCheckResourceAttrSet("activedirectory_access_rule.grant1", "trustee_sid"),
				),
			},
			{
				// After the grant: the identical call now succeeds, because the
				// ACE — not a new identity, not a new client — is the only
				// thing that changed.
				PreConfig: func() {
					limited := e2eClient(t, limitedUser, limitedPass)
					if err := limited.User.SetPassword(context.Background(),
						adpwsh.ByGUID(userGUID), newPassword); err != nil {
						t.Fatalf("svc_e2e_limited could not reset the password after the "+
							"reset_user_passwords grant: %v", err)
					}
				},
				Config:   base + grant,
				PlanOnly: true,
			},
		},
	})
}

// TestAccE2EAccessRuleDenied asserts that a Grant attempted on an object
// outside the principal's delegated subtree surfaces the diagnostics.go
// KindDenied branch, not a crash or a different classification — mirroring
// TestAccE2EDeniedCrossSubtree, for activedirectory_access_rule instead of
// activedirectory_ou. svc_e2e_limited has no WriteDacl (or any right at all)
// over OU=beta.
func TestAccE2EAccessRuleDenied(t *testing.T) {
	user, pass := os.Getenv(envE2ELimitedUser), os.Getenv(envE2ELimitedPass)
	e := e2eSuiteEnv(user, pass, e2eBetaDN()) // limited creds, beta's OU as target
	trustee := e2eBareSAM(user)               // any visible trustee; the target is what's denied

	resource.Test(t, resource.TestCase{
		PreCheck:                 e2ePreCheck(t, envE2ELimitedUser, envE2ELimitedPass, envE2EBetaUser),
		ProtoV6ProviderFactories: accFactories(),
		Steps: []resource.TestStep{{
			Config: e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_access_rule" "denied" {
  target      = %q
  trustee     = %q
  rights      = ["ExtendedRight"]
  object_type = "Reset Password"
  applies_to = {
    scope        = "descendants"
    object_class = "user"
  }
  type = "Allow"
}`, e.Container, trustee),
			// The exact summary the KindDenied branch renders (diagnostics.go).
			ExpectError: regexp.MustCompile(`Access denied by Active Directory`),
		}},
	})
}
