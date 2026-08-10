package provider_test

import (
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// accNamePrefix is on every object these suites create. The sweeper matches on
// it and deletes nothing without it, so a sweep cannot touch an object a human
// placed in the test subtree by hand.
const accNamePrefix = "tfacc-"

// suiteEnv is what a lifecycle suite needs in order to be independent of its
// backend. The same steps run against fake.Directory with no TF_ACC and against
// a real domain with it: fake-versus-real divergence is the central risk of
// having built six tasks against a fake, and one set of assertions against both
// is the only thing that detects it.
type suiteEnv struct {
	// ProviderConfig is the provider block, prepended to every step's config.
	// Against the fake it configures ssh {} with placeholder values; against a
	// real domain it configures local {} literally.
	ProviderConfig string

	// Container is the distinguished name every object in the suite is created
	// beneath. It is treated as pre-existing and is never created or destroyed.
	Container string
}

// altContainer is Container spelled in a different case. Active Directory
// matches distinguished names case-insensitively and echoes back whatever it
// stored, so writing the container differently must plan no change at all —
// that is the assertion the keepEquivalentDN plan modifier exists for.
func (e suiteEnv) altContainer() string { return strings.ToUpper(e.Container) }

// dn returns the distinguished name an object with this RDN will have.
func (e suiteEnv) dn(rdn string) string { return rdn + "," + e.Container }

// upnSuffix is the DNS domain the container sits in, assembled from the DN's DC
// components. Deriving it means the suite cannot be pointed at one domain and
// write a userPrincipalName for another. DC values never contain an escaped
// comma, so splitting on commas is safe here and only here.
func (e suiteEnv) upnSuffix() string {
	var parts []string
	for _, c := range strings.Split(e.Container, ",") {
		c = strings.TrimSpace(c)
		if strings.HasPrefix(strings.ToUpper(c), "DC=") {
			parts = append(parts, c[3:])
		}
	}
	return strings.Join(parts, ".")
}

// fakeSuiteEnv runs a suite against fake.Directory, whose naming context is
// fixed at DC=corp,DC=local.
func fakeSuiteEnv() suiteEnv {
	return suiteEnv{ProviderConfig: providerConfig, Container: "DC=corp,DC=local"}
}

// ---------------------------------------------------------------------------
// Organizational units
// ---------------------------------------------------------------------------

// ouLifecycleSteps is the create / read / update / delete / import cycle.
// Against the fake it proves the provider's plan and state mapping; against a
// real domain it proves the cmdlets accept what the fake accepted.
func ouLifecycleSteps(e suiteEnv) []resource.TestStep {
	staff := accNamePrefix + "ou"
	renamed := accNamePrefix + "ou-renamed"
	parent := accNamePrefix + "ou-parent"

	create := fmt.Sprintf(`
resource "activedirectory_ou" "staff" {
  name        = %q
  container   = %q
  description = "The staff OU"
}`, staff, e.Container)

	// The same configuration with the container spelled in a different case. A
	// no-change apply must produce no diff: this is the step that catches a DN
	// echoed back in different case or spacing.
	noDiff := fmt.Sprintf(`
resource "activedirectory_ou" "staff" {
  name        = %q
  container   = %q
  description = "The staff OU"
}`, staff, e.altContainer())

	// Rename and move in one step. The ID must survive, because deleting and
	// recreating an AD object destroys its SID and every ACL that names it.
	moved := fmt.Sprintf(`
resource "activedirectory_ou" "parent" {
  name      = %q
  container = %q
}
resource "activedirectory_ou" "staff" {
  name        = %q
  container   = activedirectory_ou.parent.dn
  description = ""
}`, parent, e.Container, renamed)

	return []resource.TestStep{
		{
			Config: e.ProviderConfig + create,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("activedirectory_ou.staff", "id"),
				resource.TestCheckResourceAttr("activedirectory_ou.staff", "dn", e.dn("OU="+staff)),
				resource.TestCheckResourceAttr("activedirectory_ou.staff", "description", "The staff OU"),
				// AD's own default, mirrored rather than silently inverted.
				resource.TestCheckResourceAttr("activedirectory_ou.staff",
					"protected_from_accidental_deletion", "true"),
			),
		},
		{
			Config:             e.ProviderConfig + noDiff,
			PlanOnly:           true,
			ExpectNonEmptyPlan: false,
		},
		{
			Config: e.ProviderConfig + moved,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("activedirectory_ou.staff", "dn",
					"OU="+renamed+",OU="+parent+","+e.Container),
				resource.TestCheckResourceAttr("activedirectory_ou.staff", "description", ""),
			),
		},
		{
			ResourceName:      "activedirectory_ou.staff",
			ImportState:       true,
			ImportStateVerify: true,
		},
	}
}

// ouImportByDNSteps adopts an object that already exists. Importing by DN must
// work as well as importing by GUID, and there is no prior state to verify
// against, so the imported attributes are asserted directly. wantGUID is the
// object's objectGUID: the DN goes in, and the GUID is what state holds.
func ouImportByDNSteps(e suiteEnv, wantGUID string) []resource.TestStep {
	existing := accNamePrefix + "ou-adopted"
	return []resource.TestStep{{
		Config: e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "existing" {
  name        = %q
  container   = %q
  description = "adopted"
}`, existing, e.Container),
		ResourceName:       "activedirectory_ou.existing",
		ImportState:        true,
		ImportStateId:      e.dn("OU=" + existing),
		ImportStatePersist: true,
		ImportStateCheck: composeImportStateCheck(
			checkImportedAttr("id", wantGUID),
			checkImportedAttr("dn", e.dn("OU="+existing)),
			checkImportedAttr("name", existing),
			checkImportedAttr("container", e.Container),
			checkImportedAttr("description", "adopted"),
			checkImportedAttr("protected_from_accidental_deletion", "true"),
		),
	}}
}

// ---------------------------------------------------------------------------
// Groups
// ---------------------------------------------------------------------------

// groupLifecycleSteps covers create, the no-diff replan, rename plus
// sAMAccountName change plus a scope conversion, and import. managed_by points
// at a group the suite creates rather than an invented DN, because Active
// Directory requires the referenced object to exist and the fake does not.
func groupLifecycleSteps(e suiteEnv) []resource.TestStep {
	ou := accNamePrefix + "grp-ou"
	devs := accNamePrefix + "grp"
	renamed := accNamePrefix + "grp-renamed"
	mgr := accNamePrefix + "grp-mgr"

	base := e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "staff" {
  name      = %q
  container = %q
}
resource "activedirectory_group" "mgr" {
  name             = %q
  sam_account_name = %q
  container        = activedirectory_ou.staff.dn
}
`, ou, e.Container, mgr, mgr)

	create := fmt.Sprintf(`
resource "activedirectory_group" "devs" {
  name             = %q
  sam_account_name = %q
  container        = activedirectory_ou.staff.dn
}`, devs, devs)

	// global to universal is a conversion AD permits. Which conversions it
	// refuses is exactly what running this suite against a real domain reveals,
	// and the fake has no opinion on any of them.
	updated := fmt.Sprintf(`
resource "activedirectory_group" "devs" {
  name             = %q
  sam_account_name = %q
  container        = activedirectory_ou.staff.dn
  scope            = "universal"
  description      = "Everyone who writes code"
  managed_by       = activedirectory_group.mgr.dn
}`, renamed, renamed)

	return []resource.TestStep{
		{
			Config: base + create,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("activedirectory_group.devs", "id"),
				resource.TestCheckResourceAttrSet("activedirectory_group.devs", "sid"),
				resource.TestCheckResourceAttr("activedirectory_group.devs", "dn",
					"CN="+devs+",OU="+ou+","+e.Container),
				// The defaults mirror the cmdlet's own.
				resource.TestCheckResourceAttr("activedirectory_group.devs", "scope", "global"),
				resource.TestCheckResourceAttr("activedirectory_group.devs", "category", "security"),
			),
		},
		{
			Config:   base + create,
			PlanOnly: true,
		},
		{
			Config: base + updated,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("activedirectory_group.devs", "scope", "universal"),
				resource.TestCheckResourceAttr("activedirectory_group.devs", "sam_account_name", renamed),
				resource.TestCheckResourceAttr("activedirectory_group.devs", "dn",
					"CN="+renamed+",OU="+ou+","+e.Container),
				resource.TestCheckResourceAttr("activedirectory_group.devs", "managed_by",
					"CN="+mgr+",OU="+ou+","+e.Container),
			),
		},
		{
			ResourceName:      "activedirectory_group.devs",
			ImportState:       true,
			ImportStateVerify: true,
		},
	}
}
