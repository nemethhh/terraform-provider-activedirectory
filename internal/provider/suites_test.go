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

	// Protection toggled off in place, then back on. The objectGUID is stable
	// throughout: this is an attribute update, not a replace.
	movedUnprotected := fmt.Sprintf(`
resource "activedirectory_ou" "parent" {
  name      = %q
  container = %q
}
resource "activedirectory_ou" "staff" {
  name                               = %q
  container                          = activedirectory_ou.parent.dn
  description                        = ""
  protected_from_accidental_deletion = false
}`, parent, e.Container, renamed)

	movedReprotected := fmt.Sprintf(`
resource "activedirectory_ou" "parent" {
  name      = %q
  container = %q
}
resource "activedirectory_ou" "staff" {
  name                               = %q
  container                          = activedirectory_ou.parent.dn
  description                        = ""
  protected_from_accidental_deletion = true
}`, parent, e.Container, renamed)

	return []resource.TestStep{
		{
			Config: e.ProviderConfig + create,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("activedirectory_ou.staff", "id"),
				resource.TestCheckResourceAttr("activedirectory_ou.staff", "dn", e.dn("OU="+staff)),
				resource.TestCheckResourceAttr("activedirectory_ou.staff", "description", "The staff OU"),
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
			Config: e.ProviderConfig + movedUnprotected,
			Check: resource.TestCheckResourceAttr("activedirectory_ou.staff",
				"protected_from_accidental_deletion", "false"),
		},
		{
			Config: e.ProviderConfig + movedReprotected,
			Check: resource.TestCheckResourceAttr("activedirectory_ou.staff",
				"protected_from_accidental_deletion", "true"),
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

// ouDataSourceSteps creates a managed OU and reads it straight back through the
// activedirectory_ou data source in the same config, so the data source's
// resolve-by-identity and state mapping are asserted against both backends. The
// data source keys off the resource's dn, which both establishes the dependency
// (the read is deferred until the OU exists) and proves lookup by DN.
func ouDataSourceSteps(e suiteEnv) []resource.TestStep {
	name := accNamePrefix + "ds-ou"
	config := e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "src" {
  name        = %q
  container   = %q
  description = "data source read-back"
}

data "activedirectory_ou" "src" {
  dn = activedirectory_ou.src.dn
}`, name, e.Container)

	return []resource.TestStep{{
		Config: config,
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttrPair(
				"data.activedirectory_ou.src", "id", "activedirectory_ou.src", "id"),
			resource.TestCheckResourceAttr("data.activedirectory_ou.src", "name", name),
			resource.TestCheckResourceAttr("data.activedirectory_ou.src", "container", e.Container),
			resource.TestCheckResourceAttr("data.activedirectory_ou.src", "description", "data source read-back"),
			resource.TestCheckResourceAttr("data.activedirectory_ou.src",
				"protected_from_accidental_deletion", "true"),
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
  category         = "distribution"
  description      = "Everyone who writes code"
  managed_by       = activedirectory_group.mgr.dn
}`, renamed, renamed)

	// managed_by and description both carry a load-bearing empty default, so
	// setting them to "" must clear them rather than retain the prior value.
	cleared := fmt.Sprintf(`
resource "activedirectory_group" "devs" {
  name             = %q
  sam_account_name = %q
  container        = activedirectory_ou.staff.dn
  scope            = "universal"
  category         = "distribution"
  description      = ""
  managed_by       = ""
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
				resource.TestCheckResourceAttr("activedirectory_group.devs", "category", "distribution"),
				resource.TestCheckResourceAttr("activedirectory_group.devs", "sam_account_name", renamed),
				resource.TestCheckResourceAttr("activedirectory_group.devs", "dn",
					"CN="+renamed+",OU="+ou+","+e.Container),
				resource.TestCheckResourceAttr("activedirectory_group.devs", "managed_by",
					"CN="+mgr+",OU="+ou+","+e.Container),
			),
		},
		{
			Config: base + cleared,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("activedirectory_group.devs", "description", ""),
				resource.TestCheckResourceAttr("activedirectory_group.devs", "managed_by", ""),
			),
		},
		{
			ResourceName:      "activedirectory_group.devs",
			ImportState:       true,
			ImportStateVerify: true,
		},
	}
}

// groupDataSourceSteps creates a managed group and reads it back through the
// activedirectory_group data source, keyed by sAMAccountName. It asserts the
// data source resolves the security principal and projects the same id, sid,
// scope and category the resource holds.
func groupDataSourceSteps(e suiteEnv) []resource.TestStep {
	ou := accNamePrefix + "ds-grp-ou"
	name := accNamePrefix + "ds-grp"
	config := e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "src" {
  name      = %q
  container = %q
}
resource "activedirectory_group" "src" {
  name             = %q
  sam_account_name = %q
  container        = activedirectory_ou.src.dn
  description      = "data source read-back"
}

data "activedirectory_group" "src" {
  sam_account_name = activedirectory_group.src.sam_account_name
}`, ou, e.Container, name, name)

	return []resource.TestStep{{
		Config: config,
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttrPair(
				"data.activedirectory_group.src", "id", "activedirectory_group.src", "id"),
			resource.TestCheckResourceAttrPair(
				"data.activedirectory_group.src", "sid", "activedirectory_group.src", "sid"),
			resource.TestCheckResourceAttr("data.activedirectory_group.src", "name", name),
			resource.TestCheckResourceAttr("data.activedirectory_group.src", "scope", "global"),
			resource.TestCheckResourceAttr("data.activedirectory_group.src", "category", "security"),
			resource.TestCheckResourceAttr("data.activedirectory_group.src", "description", "data source read-back"),
			resource.TestCheckResourceAttr("data.activedirectory_group.src", "container", e.dn("OU="+ou)),
		),
	}}
}

// ---------------------------------------------------------------------------
// Users
// ---------------------------------------------------------------------------

// userLifecycleSteps covers create with a write-only password, the no-diff
// replan, a rotation driven by password_version alongside attribute clears, and
// import by sAMAccountName — the brownfield form.
//
// passwordCheck is appended to the rotation step and may be nil: only the fake
// can be asked what passwords were set, and a real domain will not tell anyone.
func userLifecycleSteps(e suiteEnv, passwordCheck resource.TestCheckFunc) []resource.TestStep {
	ou := accNamePrefix + "usr-ou"
	sam := accNamePrefix + "usr"
	upn := sam + "@" + e.upnSuffix()

	base := e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "staff" {
  name      = %q
  container = %q
}
`, ou, e.Container)

	create := fmt.Sprintf(`
resource "activedirectory_user" "jdoe" {
  sam_account_name    = %q
  container           = activedirectory_ou.staff.dn
  user_principal_name = %q
  display_name        = "John Doe"
  given_name          = "John"
  surname             = "Doe"
  enabled             = true
  password            = "Correct-Horse-Battery-Staple-1"
  password_version    = 1
}`, sam, upn)

	// Bumping the version rotates; the password itself cannot be diffed,
	// because it is never stored. Clearing surname is the row that catches a
	// wrong LDAP mapping: its attribute is sn, not surname.
	rotated := fmt.Sprintf(`
resource "activedirectory_user" "jdoe" {
  sam_account_name        = %q
  container               = activedirectory_ou.staff.dn
  user_principal_name     = %q
  display_name            = "John Doe"
  given_name              = "John"
  surname                 = ""
  enabled                 = false
  password                = "Rotated-P4ssw0rd-2"
  password_version        = 2
  account_expiration_date = "2027-01-02T03:04:05Z"
}`, sam, upn)

	rotationChecks := []resource.TestCheckFunc{
		resource.TestCheckResourceAttr("activedirectory_user.jdoe", "enabled", "false"),
		resource.TestCheckResourceAttr("activedirectory_user.jdoe", "surname", ""),
		resource.TestCheckResourceAttr("activedirectory_user.jdoe", "password_version", "2"),
		resource.TestCheckResourceAttr("activedirectory_user.jdoe",
			"account_expiration_date", "2027-01-02T03:04:05Z"),
	}
	if passwordCheck != nil {
		rotationChecks = append(rotationChecks, passwordCheck)
	}

	return []resource.TestStep{
		{
			Config: base + create,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("activedirectory_user.jdoe", "id"),
				resource.TestCheckResourceAttrSet("activedirectory_user.jdoe", "sid"),
				resource.TestCheckResourceAttr("activedirectory_user.jdoe", "dn",
					"CN="+sam+",OU="+ou+","+e.Container),
				// The CN defaults to the sAMAccountName.
				resource.TestCheckResourceAttr("activedirectory_user.jdoe", "name", sam),
				resource.TestCheckResourceAttr("activedirectory_user.jdoe", "enabled", "true"),
				// The password is never in state: this is the whole point.
				resource.TestCheckNoResourceAttr("activedirectory_user.jdoe", "password"),
			),
		},
		{
			Config:   base + create,
			PlanOnly: true,
		},
		{
			Config: base + rotated,
			Check:  resource.ComposeAggregateTestCheckFunc(rotationChecks...),
		},
		{
			ResourceName:      "activedirectory_user.jdoe",
			ImportState:       true,
			ImportStateId:     sam, // by sAMAccountName, the brownfield case
			ImportStateVerify: true,
			// A write-only attribute and its version are not readable, so they
			// cannot round-trip through import.
			ImportStateVerifyIgnore: []string{"password", "password_version"},
		},
	}
}

// userDataSourceSteps creates a managed user and reads it back through the
// activedirectory_user data source, keyed by objectGUID. It asserts the data
// source resolves the account and projects the positively-stated flags and the
// account expiry (the RFC 3339 formatting path) the same way Get-ADUser reads
// them.
func userDataSourceSteps(e suiteEnv) []resource.TestStep {
	ou := accNamePrefix + "ds-usr-ou"
	sam := accNamePrefix + "ds-usr"
	upn := sam + "@" + e.upnSuffix()
	config := e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "src" {
  name      = %q
  container = %q
}
resource "activedirectory_user" "src" {
  sam_account_name        = %q
  container               = activedirectory_ou.src.dn
  user_principal_name     = %q
  display_name            = "Jane Roe"
  given_name              = "Jane"
  surname                 = "Roe"
  enabled                 = true
  password                = "Correct-Horse-Battery-Staple-9"
  password_version        = 1
  account_expiration_date = "2027-06-01T12:00:00Z"
}

data "activedirectory_user" "src" {
  guid = activedirectory_user.src.id
}`, ou, e.Container, sam, upn)

	return []resource.TestStep{{
		Config: config,
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttrPair(
				"data.activedirectory_user.src", "id", "activedirectory_user.src", "id"),
			resource.TestCheckResourceAttrPair(
				"data.activedirectory_user.src", "sid", "activedirectory_user.src", "sid"),
			resource.TestCheckResourceAttr("data.activedirectory_user.src", "sam_account_name", sam),
			resource.TestCheckResourceAttr("data.activedirectory_user.src", "display_name", "Jane Roe"),
			resource.TestCheckResourceAttr("data.activedirectory_user.src", "surname", "Roe"),
			resource.TestCheckResourceAttr("data.activedirectory_user.src", "enabled", "true"),
			resource.TestCheckResourceAttr("data.activedirectory_user.src", "can_change_password", "true"),
			resource.TestCheckResourceAttr("data.activedirectory_user.src", "password_expires", "true"),
			resource.TestCheckResourceAttr("data.activedirectory_user.src",
				"account_expiration_date", "2027-06-01T12:00:00Z"),
			resource.TestCheckResourceAttr("data.activedirectory_user.src", "container", e.dn("OU="+ou)),
		),
	}}
}

// ---------------------------------------------------------------------------
// Plural search data sources
// ---------------------------------------------------------------------------

// usersDataSourceSteps creates two users under a dedicated OU, then searches for
// them through activedirectory_users with a one_level scope and a filter_by
// equality term. depends_on defers the search until both users exist. Scoping
// under a freshly created OU keeps the result set to exactly the suite's own
// objects on a shared domain, so the count assertion is stable against both
// backends.
func usersDataSourceSteps(e suiteEnv) []resource.TestStep {
	ou := accNamePrefix + "dss-usr-ou"
	a := accNamePrefix + "dss-usr-a"
	b := accNamePrefix + "dss-usr-b"
	upnA := a + "@" + e.upnSuffix()
	upnB := b + "@" + e.upnSuffix()
	config := e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "src" {
  name      = %q
  container = %q
}
resource "activedirectory_user" "a" {
  sam_account_name    = %q
  container           = activedirectory_ou.src.dn
  user_principal_name = %q
  description         = "tfacc-search-target"
  password            = "Correct-Horse-Battery-Staple-1"
  password_version    = 1
}
resource "activedirectory_user" "b" {
  sam_account_name    = %q
  container           = activedirectory_ou.src.dn
  user_principal_name = %q
  description         = "tfacc-search-target"
  password            = "Correct-Horse-Battery-Staple-1"
  password_version    = 1
}

data "activedirectory_users" "src" {
  container  = activedirectory_ou.src.dn
  scope      = "one_level"
  filter_by  = { description = "tfacc-search-target" }
  depends_on = [activedirectory_user.a, activedirectory_user.b]
}`, ou, e.Container, a, upnA, b, upnB)

	return []resource.TestStep{{
		Config: config,
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("data.activedirectory_users.src", "users.#", "2"),
			resource.TestCheckTypeSetElemNestedAttrs(
				"data.activedirectory_users.src", "users.*",
				map[string]string{"sam_account_name": a}),
			resource.TestCheckTypeSetElemNestedAttrs(
				"data.activedirectory_users.src", "users.*",
				map[string]string{"sam_account_name": b, "description": "tfacc-search-target"}),
		),
	}}
}

// groupsDataSourceSteps creates two groups under a dedicated OU whose names share
// a prefix, then searches for them through activedirectory_groups with a raw
// ldap_filter wildcard. The wildcard exercises the fake evaluator's substring
// path and, against a real domain, Get-ADGroup's -LDAPFilter.
func groupsDataSourceSteps(e suiteEnv) []resource.TestStep {
	ou := accNamePrefix + "dss-grp-ou"
	a := accNamePrefix + "dss-grp-app-a"
	b := accNamePrefix + "dss-grp-app-b"
	config := e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "src" {
  name      = %q
  container = %q
}
resource "activedirectory_group" "a" {
  name             = %q
  sam_account_name = %q
  container        = activedirectory_ou.src.dn
}
resource "activedirectory_group" "b" {
  name             = %q
  sam_account_name = %q
  container        = activedirectory_ou.src.dn
}

data "activedirectory_groups" "src" {
  container   = activedirectory_ou.src.dn
  scope       = "one_level"
  ldap_filter = "(name=%s*)"
  depends_on  = [activedirectory_group.a, activedirectory_group.b]
}`, ou, e.Container, a, a, b, b, accNamePrefix+"dss-grp-app-")

	return []resource.TestStep{{
		Config: config,
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("data.activedirectory_groups.src", "groups.#", "2"),
			resource.TestCheckTypeSetElemNestedAttrs(
				"data.activedirectory_groups.src", "groups.*",
				map[string]string{"sam_account_name": a, "scope": "global", "category": "security"}),
		),
	}}
}

// ousDataSourceSteps creates two child OUs under a dedicated parent OU, then
// searches for them through activedirectory_ous with a one_level scope. The
// parent is the search base, so the two children are exactly the result set.
func ousDataSourceSteps(e suiteEnv) []resource.TestStep {
	parent := accNamePrefix + "dss-ou-parent"
	a := accNamePrefix + "dss-ou-a"
	b := accNamePrefix + "dss-ou-b"
	config := e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "parent" {
  name      = %q
  container = %q
}
resource "activedirectory_ou" "a" {
  name      = %q
  container = activedirectory_ou.parent.dn
}
resource "activedirectory_ou" "b" {
  name      = %q
  container = activedirectory_ou.parent.dn
}

data "activedirectory_ous" "src" {
  container  = activedirectory_ou.parent.dn
  scope      = "one_level"
  depends_on = [activedirectory_ou.a, activedirectory_ou.b]
}`, parent, e.Container, a, b)

	return []resource.TestStep{{
		Config: config,
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("data.activedirectory_ous.src", "ous.#", "2"),
			resource.TestCheckTypeSetElemNestedAttrs(
				"data.activedirectory_ous.src", "ous.*",
				map[string]string{"name": a}),
		),
	}}
}

// ---------------------------------------------------------------------------
// Hostile input
// ---------------------------------------------------------------------------

// hostileValues each broke the archived provider at least once. They travel as
// JSON on stdin and are splatted into the cmdlet, so there is no escaping layer
// to get wrong — against the fake that exercises the payload path, and against a
// real domain it exercises the cmdlet layer too.
var hostileValues = []struct{ Name, Value string }{
	{"underscore", "under_score"},
	{"double_quote", `has "quotes"`},
	{"single_quote", `O'Brien`},
	{"dollar", `$env:PATH`},
	{"backtick", "back`tick"},
	{"semicolon", "semi;colon"},
	{"ampersand", "amper&sand"},
	{"pipe", "pipe|char"},
	{"comma", "Smith, John"},
	{"non_ascii", "söüäß-éòñ"},
	{"subexpression", `$(Get-Process)`},
}

func hostileDescriptionSteps(e suiteEnv, value string) []resource.TestStep {
	ou := accNamePrefix + "hostile-ou"
	return []resource.TestStep{{
		Config: e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "staff" {
  name        = %q
  container   = %q
  description = %q
}`, ou, e.Container, value),
		Check: resource.TestCheckResourceAttr("activedirectory_ou.staff", "description", value),
	}}
}

// hostileEscapedCommaSteps is where a string-splitting DN parser fails: an RDN
// containing an escaped comma must survive being used as another resource's
// container.
func hostileEscapedCommaSteps(e suiteEnv) []resource.TestStep {
	ouName := accNamePrefix + "hostile, EMEA"
	group := accNamePrefix + "hostile-grp"
	// The RDN's comma is escaped in the distinguished name, and the space after
	// it is significant to nothing but must survive verbatim.
	wantOUDN := `OU=` + accNamePrefix + `hostile\, EMEA,` + e.Container

	return []resource.TestStep{{
		Config: e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "sales" {
  name      = %q
  container = %q
}
resource "activedirectory_group" "reps" {
  name             = %q
  sam_account_name = %q
  container        = activedirectory_ou.sales.dn
}`, ouName, e.Container, group, group),
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("activedirectory_ou.sales", "dn", wantOUDN),
			resource.TestCheckResourceAttr("activedirectory_group.reps", "container", wantOUDN),
			resource.TestCheckResourceAttr("activedirectory_group.reps", "dn",
				"CN="+group+","+wantOUDN),
		),
	}}
}

// ---------------------------------------------------------------------------
// Group membership — non-authoritative (activedirectory_group_member)
// ---------------------------------------------------------------------------

// groupMemberSteps covers one membership edge: create, a no-diff replan (Read via
// IsMember must plan nothing), delete, and import by the composite id. It seeds a
// group and one user via the existing resources, then joins them.
func groupMemberSteps(e suiteEnv) []resource.TestStep {
	ou := accNamePrefix + "gm-ou"
	grp := accNamePrefix + "gm-grp"
	usr := accNamePrefix + "gm-usr"
	upn := usr + "@" + e.upnSuffix()

	base := e.ProviderConfig + fmt.Sprintf(`
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
`, ou, e.Container, grp, grp, usr, upn)

	edge := `
resource "activedirectory_group_member" "m" {
  group_id  = activedirectory_group.g.id
  member_id = activedirectory_user.u.id
}`

	return []resource.TestStep{
		{
			Config: base + edge,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("activedirectory_group_member.m", "id"),
				resource.TestCheckResourceAttrPair(
					"activedirectory_group_member.m", "group_id", "activedirectory_group.g", "id"),
				resource.TestCheckResourceAttrPair(
					"activedirectory_group_member.m", "member_id", "activedirectory_user.u", "id"),
			),
		},
		{Config: base + edge, PlanOnly: true},
		{
			ResourceName:      "activedirectory_group_member.m",
			ImportState:       true,
			ImportStateVerify: true,
		},
	}
}

// ---------------------------------------------------------------------------
// Group membership — authoritative (activedirectory_group_membership)
// ---------------------------------------------------------------------------

// groupMembershipSteps owns a group's whole member set: start with one member,
// grow to two, shrink to one, then empty. Each apply reconciles the group to
// exactly the configured set, and a no-diff replan follows the grow step.
func groupMembershipSteps(e suiteEnv) []resource.TestStep {
	ou := accNamePrefix + "gs-ou"
	grp := accNamePrefix + "gs-grp"
	a := accNamePrefix + "gs-a"
	b := accNamePrefix + "gs-b"
	upnA := a + "@" + e.upnSuffix()
	upnB := b + "@" + e.upnSuffix()

	base := e.ProviderConfig + fmt.Sprintf(`
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
`, ou, e.Container, grp, grp, a, upnA, b, upnB)

	one := `
resource "activedirectory_group_membership" "s" {
  group_id = activedirectory_group.g.id
  members  = [activedirectory_user.a.id]
}`
	two := `
resource "activedirectory_group_membership" "s" {
  group_id = activedirectory_group.g.id
  members  = [activedirectory_user.a.id, activedirectory_user.b.id]
}`
	empty := `
resource "activedirectory_group_membership" "s" {
  group_id = activedirectory_group.g.id
  members  = []
}`

	return []resource.TestStep{
		{
			Config: base + one,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrPair(
					"activedirectory_group_membership.s", "id", "activedirectory_group.g", "id"),
				resource.TestCheckResourceAttr("activedirectory_group_membership.s", "members.#", "1"),
			),
		},
		{
			Config: base + two,
			Check:  resource.TestCheckResourceAttr("activedirectory_group_membership.s", "members.#", "2"),
		},
		{Config: base + two, PlanOnly: true},
		{
			Config: base + one,
			Check:  resource.TestCheckResourceAttr("activedirectory_group_membership.s", "members.#", "1"),
		},
		{
			Config: base + empty,
			Check:  resource.TestCheckResourceAttr("activedirectory_group_membership.s", "members.#", "0"),
		},
		{
			ResourceName:      "activedirectory_group_membership.s",
			ImportState:       true,
			ImportStateVerify: true,
		},
	}
}

// groupMembersDataSourceSteps grows a group to two members through the
// authoritative membership resource, then reads them back through the
// activedirectory_group_members data source. depends_on defers the data source
// read until the membership apply has run, so the read sees the full set. The
// class assertion uses the set form because a member list has no guaranteed
// order on a real domain. Large-group paging past the 1500-entry boundary is
// proved directly against the library by TestAccGroupMembershipLargeSet and by
// TestAccGroupMembersDataSourceLargeSet through this data source.
func groupMembersDataSourceSteps(e suiteEnv) []resource.TestStep {
	ou := accNamePrefix + "dsm-ou"
	grp := accNamePrefix + "dsm-grp"
	a := accNamePrefix + "dsm-a"
	b := accNamePrefix + "dsm-b"
	upnA := a + "@" + e.upnSuffix()
	upnB := b + "@" + e.upnSuffix()

	config := e.ProviderConfig + fmt.Sprintf(`
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
  members  = [activedirectory_user.a.id, activedirectory_user.b.id]
}

data "activedirectory_group_members" "g" {
  sam_account_name = activedirectory_group.g.sam_account_name
  depends_on       = [activedirectory_group_membership.s]
}`, ou, e.Container, grp, grp, a, upnA, b, upnB)

	return []resource.TestStep{{
		Config: config,
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("data.activedirectory_group_members.g", "members.#", "2"),
			// Order is not guaranteed on a real domain, so assert by set membership.
			resource.TestCheckTypeSetElemNestedAttrs(
				"data.activedirectory_group_members.g", "members.*",
				map[string]string{"class": "user"}),
		),
	}}
}

// ---------------------------------------------------------------------------
// Group membership — nested (a group is a member of another group)
// ---------------------------------------------------------------------------

// nestedBase is the OU, a parent and child group, and a user — the fixtures both
// nested builders below share. Kept as one function so the two builders stay in
// step and neither is managed by both membership resources at once.
func nestedBase(e suiteEnv, ou, parent, child, usr, upn string) string {
	return e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "staff" {
  name      = %q
  container = %q
}
resource "activedirectory_group" "parent" {
  name             = %q
  sam_account_name = %q
  container        = activedirectory_ou.staff.dn
}
resource "activedirectory_group" "child" {
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
`, ou, e.Container, parent, parent, child, child, usr, upn)
}

// groupMemberNestedSteps proves a non-authoritative edge's member need not be a
// user: a child group is added to a parent group. The member_id space is any
// objectGUID, so nothing special is needed — this pins that it stays so.
func groupMemberNestedSteps(e suiteEnv) []resource.TestStep {
	usr := accNamePrefix + "gn-usr"
	base := nestedBase(e, accNamePrefix+"gn-ou", accNamePrefix+"gn-parent",
		accNamePrefix+"gn-child", usr, usr+"@"+e.upnSuffix())

	edge := `
resource "activedirectory_group_member" "nested" {
  group_id  = activedirectory_group.parent.id
  member_id = activedirectory_group.child.id
}`

	return []resource.TestStep{
		{
			Config: base + edge,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("activedirectory_group_member.nested", "id"),
				resource.TestCheckResourceAttrPair(
					"activedirectory_group_member.nested", "member_id", "activedirectory_group.child", "id"),
			),
		},
		{Config: base + edge, PlanOnly: true},
		{
			ResourceName:      "activedirectory_group_member.nested",
			ImportState:       true,
			ImportStateVerify: true,
		},
	}
}

// groupMembershipNestedSteps proves an authoritative set may mix member types: a
// user and a nested group are owned together, reconciled to exactly that pair.
func groupMembershipNestedSteps(e suiteEnv) []resource.TestStep {
	usr := accNamePrefix + "gnm-usr"
	base := nestedBase(e, accNamePrefix+"gnm-ou", accNamePrefix+"gnm-parent",
		accNamePrefix+"gnm-child", usr, usr+"@"+e.upnSuffix())

	mixed := `
resource "activedirectory_group_membership" "s" {
  group_id = activedirectory_group.parent.id
  members  = [activedirectory_user.u.id, activedirectory_group.child.id]
}`

	return []resource.TestStep{
		{
			Config: base + mixed,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("activedirectory_group_membership.s", "members.#", "2"),
				resource.TestCheckResourceAttrPair(
					"activedirectory_group_membership.s", "id", "activedirectory_group.parent", "id"),
			),
		},
		{Config: base + mixed, PlanOnly: true},
		{
			ResourceName:      "activedirectory_group_membership.s",
			ImportState:       true,
			ImportStateVerify: true,
		},
	}
}

// accessRuleSteps proves a single access_rule creates one ACE, replans clean,
// and round-trips through import: the ACE's friendly names (object_type
// "Reset Password", applies_to.object_class "user") resolve to GUIDs and the
// trustee resolves to a SID, none of which are echoed back verbatim on import
// (hence the ImportStateVerifyIgnore below).
func accessRuleSteps(e suiteEnv) []resource.TestStep {
	ou := accNamePrefix + "ar-ou"
	grp := accNamePrefix + "ar-grp"

	base := e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "t" {
  name      = %q
  container = %q
}
resource "activedirectory_group" "helpdesk" {
  name             = %q
  sam_account_name = %q
  container        = activedirectory_ou.t.dn
}
`, ou, e.Container, grp, grp)

	rule := `
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
}`

	return []resource.TestStep{
		{
			Config: base + rule,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("activedirectory_access_rule.reset", "id"),
				resource.TestCheckResourceAttrSet("activedirectory_access_rule.reset", "trustee_sid"),
				resource.TestCheckResourceAttr("activedirectory_access_rule.reset", "type", "Allow"),
			),
		},
		{Config: base + rule, PlanOnly: true},
		{
			ResourceName:            "activedirectory_access_rule.reset",
			ImportState:             true,
			ImportStateVerify:       true,
			ImportStateVerifyIgnore: []string{"target", "trustee", "object_type", "applies_to"},
		},
	}
}
