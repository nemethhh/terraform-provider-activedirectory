package provider_test

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
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
	upn2 := sam + "-renamed@" + e.upnSuffix() // UPN rename target
	cn := accNamePrefix + "usr-cn"            // explicit CN override, distinct from sam

	base := e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "staff" {
  name      = %q
  container = %q
}
`, ou, e.Container)

	// Create with an explicit, AD-legal flag set plus an expiry to clear later.
	// change_password_at_logon=true is legal only because password_expires=true
	// (AD cannot require a change of a password that never expires).
	create := fmt.Sprintf(`
resource "activedirectory_user" "jdoe" {
  sam_account_name         = %q
  container                = activedirectory_ou.staff.dn
  user_principal_name      = %q
  display_name             = "John Doe"
  given_name               = "John"
  surname                  = "Doe"
  enabled                  = true
  description              = "initial"
  can_change_password      = true
  password_expires         = true
  change_password_at_logon = true
  account_expiration_date  = "2027-06-01T12:00:00Z"
  password                 = "Correct-Horse-Battery-Staple-1"
  password_version         = 1
}`, sam, upn)

	// Rotate: bump the version, flip every manageable field in place, and CLEAR
	// description + account_expiration_date. surname="" catches the sn mapping.
	rotated := fmt.Sprintf(`
resource "activedirectory_user" "jdoe" {
  sam_account_name         = %q
  container                = activedirectory_ou.staff.dn
  user_principal_name      = %q
  display_name             = "Johnathan Doe"
  given_name               = "John"
  surname                  = ""
  enabled                  = false
  description              = ""
  can_change_password      = false
  password_expires         = false
  change_password_at_logon = false
  account_expiration_date  = ""
  password                 = "Rotated-P4ssw0rd-2"
  password_version         = 2
}`, sam, upn2)

	// CN override: an explicit name (CN) distinct from the sAMAccountName. A CN
	// rename is an in-place update; the objectGUID must survive.
	cnOverride := fmt.Sprintf(`
resource "activedirectory_user" "jdoe" {
  sam_account_name         = %q
  container                = activedirectory_ou.staff.dn
  name                     = %q
  user_principal_name      = %q
  display_name             = "Johnathan Doe"
  given_name               = "John"
  surname                  = ""
  enabled                  = false
  description              = ""
  can_change_password      = false
  password_expires         = false
  change_password_at_logon = false
  account_expiration_date  = ""
  password                 = "Rotated-P4ssw0rd-2"
  password_version         = 2
}`, sam, cn, upn2)

	var createID, cnID string

	rotationChecks := []resource.TestCheckFunc{
		resource.TestCheckResourceAttr("activedirectory_user.jdoe", "enabled", "false"),
		resource.TestCheckResourceAttr("activedirectory_user.jdoe", "surname", ""),
		resource.TestCheckResourceAttr("activedirectory_user.jdoe", "user_principal_name", upn2),
		resource.TestCheckResourceAttr("activedirectory_user.jdoe", "display_name", "Johnathan Doe"),
		resource.TestCheckResourceAttr("activedirectory_user.jdoe", "description", ""),
		resource.TestCheckResourceAttr("activedirectory_user.jdoe", "can_change_password", "false"),
		resource.TestCheckResourceAttr("activedirectory_user.jdoe", "password_expires", "false"),
		resource.TestCheckResourceAttr("activedirectory_user.jdoe", "change_password_at_logon", "false"),
		resource.TestCheckResourceAttr("activedirectory_user.jdoe", "account_expiration_date", ""),
		resource.TestCheckResourceAttr("activedirectory_user.jdoe", "password_version", "2"),
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
				resource.TestCheckResourceAttr("activedirectory_user.jdoe", "name", sam),
				resource.TestCheckResourceAttr("activedirectory_user.jdoe", "enabled", "true"),
				resource.TestCheckResourceAttr("activedirectory_user.jdoe", "description", "initial"),
				resource.TestCheckResourceAttr("activedirectory_user.jdoe", "can_change_password", "true"),
				resource.TestCheckResourceAttr("activedirectory_user.jdoe", "password_expires", "true"),
				resource.TestCheckResourceAttr("activedirectory_user.jdoe", "change_password_at_logon", "true"),
				resource.TestCheckResourceAttr("activedirectory_user.jdoe",
					"account_expiration_date", "2027-06-01T12:00:00Z"),
				resource.TestCheckNoResourceAttr("activedirectory_user.jdoe", "password"),
				captureAttr("activedirectory_user.jdoe", "id", &createID),
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
			Config: base + cnOverride,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("activedirectory_user.jdoe", "name", cn),
				resource.TestCheckResourceAttr("activedirectory_user.jdoe", "dn",
					"CN="+cn+",OU="+ou+","+e.Container),
				captureAttr("activedirectory_user.jdoe", "id", &cnID),
				func(*terraform.State) error {
					if cnID != createID {
						return fmt.Errorf("CN override must be in-place: objectGUID changed %q -> %q", createID, cnID)
					}
					return nil
				},
			),
		},
		{
			ResourceName:            "activedirectory_user.jdoe",
			ImportState:             true,
			ImportStateId:           sam, // by sAMAccountName, the brownfield case
			ImportStateVerify:       true,
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
// Group Managed Service Accounts (gMSA)
// ---------------------------------------------------------------------------

// gmsaGUIDPattern is what activedirectory_gmsa's id always holds: an
// objectGUID. Every other lifecycle builder in this file asserts "id" only
// with TestCheckResourceAttrSet; gMSA additionally pins the shape, because the
// controller ruling on sam_account_name (below) turns on exactly this
// resource minting real identity values rather than echoing configuration.
var gmsaGUIDPattern = regexp.MustCompile(
	`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// gmsaLifecycleSteps covers create with a non-default Kerberos encryption set
// and a non-default password-rotation interval, an attribute update that
// touches every mutable field including clearing description, rename plus
// move in one step, and import by objectGUID.
//
// sam_account_name is never set in configuration: leaving it to default from
// name is what exercises the controller ruling that the attribute holds the
// un-suffixed base ("name", not "name$") — Active Directory appends "$" to a
// gMSA's sAMAccountName on every read, but apply() (resource_gmsa.go) strips
// it before the value ever reaches state, so the Terraform-visible attribute
// and name agree exactly.
//
// A group is created alongside the gMSA and referenced by
// principals_allowed_to_retrieve_managed_password on every step from create
// onward, so the attribute is never left untested (previously it appeared
// nowhere but an ImportStateVerifyIgnore). The same group is kept across
// every subsequent config so the set never churns.
func gmsaLifecycleSteps(e suiteEnv) []resource.TestStep {
	ou := accNamePrefix + "gmsa-ou"
	movedOU := accNamePrefix + "gmsa-ou2"
	name := accNamePrefix + "gmsa"
	renamed := accNamePrefix + "gmsa2"
	principal := accNamePrefix + "gmsa-principal"
	host1 := name + "01." + e.upnSuffix()
	host2 := name + "02." + e.upnSuffix()
	spn1 := "HTTP/" + host1
	spn2a := "HTTP/" + host2
	spn2b := "WSMAN/" + host2

	base := e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "staff" {
  name      = %q
  container = %q
}
resource "activedirectory_group" "principal" {
  name             = %q
  sam_account_name = %q
  container        = activedirectory_ou.staff.dn
}
`, ou, e.Container, principal, principal)

	create := fmt.Sprintf(`
resource "activedirectory_gmsa" "svc" {
  name                                             = %q
  container                                        = activedirectory_ou.staff.dn
  dns_hostname                                     = %q
  description                                      = "initial"
  service_principal_names                          = [%q]
  kerberos_encryption_type                         = ["AES256"]
  managed_password_interval_in_days                = 45
  principals_allowed_to_retrieve_managed_password  = [activedirectory_group.principal.id]
}`, name, host1, spn1)

	// Identical to create except managed_password_interval_in_days is omitted
	// entirely. UseStateForUnknown (resource_gmsa.go) resolves the omitted
	// plan value to the prior state (45), so immutableAfterCreate sees plan
	// == state and this must produce an empty plan: omission carries the
	// existing value forward rather than resetting to a client-side default
	// and erroring against it.
	createOmitInterval := fmt.Sprintf(`
resource "activedirectory_gmsa" "svc" {
  name                                             = %q
  container                                        = activedirectory_ou.staff.dn
  dns_hostname                                     = %q
  description                                      = "initial"
  service_principal_names                          = [%q]
  kerberos_encryption_type                         = ["AES256"]
  principals_allowed_to_retrieve_managed_password  = [activedirectory_group.principal.id]
}`, name, host1, spn1)

	// Every mutable attribute changed in one apply: description, dns_hostname,
	// the Kerberos set grown from one value to two, and a full SPN replacement
	// (both new values, not an addition to the old ones).
	updated := fmt.Sprintf(`
resource "activedirectory_gmsa" "svc" {
  name                                             = %q
  container                                        = activedirectory_ou.staff.dn
  dns_hostname                                     = %q
  description                                      = "updated"
  service_principal_names                          = [%q, %q]
  kerberos_encryption_type                         = ["AES128", "AES256"]
  managed_password_interval_in_days                = 45
  principals_allowed_to_retrieve_managed_password  = [activedirectory_group.principal.id]
}`, name, host2, spn2a, spn2b)

	// description carries a load-bearing empty default, so setting it to ""
	// must clear it rather than retain the prior value.
	cleared := fmt.Sprintf(`
resource "activedirectory_gmsa" "svc" {
  name                                             = %q
  container                                        = activedirectory_ou.staff.dn
  dns_hostname                                     = %q
  description                                      = ""
  service_principal_names                          = [%q, %q]
  kerberos_encryption_type                         = ["AES128", "AES256"]
  managed_password_interval_in_days                = 45
  principals_allowed_to_retrieve_managed_password  = [activedirectory_group.principal.id]
}`, name, host2, spn2a, spn2b)

	// Rename and move in one step, into a freshly introduced OU. The original
	// fixture OU stays declared (and still protected_from_accidental_deletion
	// by default), so this apply is never also asked to destroy it.
	moved := fmt.Sprintf(`
resource "activedirectory_ou" "moved" {
  name      = %q
  container = %q
}
resource "activedirectory_gmsa" "svc" {
  name                                             = %q
  container                                        = activedirectory_ou.moved.dn
  dns_hostname                                     = %q
  description                                      = ""
  service_principal_names                          = [%q, %q]
  kerberos_encryption_type                         = ["AES128", "AES256"]
  managed_password_interval_in_days                = 45
  principals_allowed_to_retrieve_managed_password  = [activedirectory_group.principal.id]
}`, movedOU, e.Container, renamed, host2, spn2a, spn2b)

	var createID, movedID string

	return []resource.TestStep{
		{
			Config: base + create,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestMatchResourceAttr("activedirectory_gmsa.svc", "id", gmsaGUIDPattern),
				resource.TestCheckResourceAttrSet("activedirectory_gmsa.svc", "sid"),
				resource.TestCheckResourceAttr("activedirectory_gmsa.svc", "dn",
					"CN="+name+",OU="+ou+","+e.Container),
				// Controller ruling: the Terraform attribute holds the
				// un-suffixed base; Active Directory's own trailing "$" is
				// stripped by apply() before it ever reaches state.
				resource.TestCheckResourceAttr("activedirectory_gmsa.svc", "sam_account_name", name),
				resource.TestCheckResourceAttr("activedirectory_gmsa.svc", "description", "initial"),
				resource.TestCheckResourceAttr("activedirectory_gmsa.svc", "dns_hostname", host1),
				resource.TestCheckResourceAttr("activedirectory_gmsa.svc",
					"managed_password_interval_in_days", "45"),
				resource.TestCheckResourceAttr("activedirectory_gmsa.svc", "kerberos_encryption_type.#", "1"),
				resource.TestCheckTypeSetElemAttr("activedirectory_gmsa.svc",
					"kerberos_encryption_type.*", "AES256"),
				resource.TestCheckResourceAttr("activedirectory_gmsa.svc", "service_principal_names.#", "1"),
				resource.TestCheckTypeSetElemAttr("activedirectory_gmsa.svc",
					"service_principal_names.*", spn1),
				resource.TestCheckResourceAttr("activedirectory_gmsa.svc",
					"principals_allowed_to_retrieve_managed_password.#", "1"),
				resource.TestCheckTypeSetElemAttrPair("activedirectory_gmsa.svc",
					"principals_allowed_to_retrieve_managed_password.*",
					"activedirectory_group.principal", "id"),
				captureAttr("activedirectory_gmsa.svc", "id", &createID),
			),
		},
		{Config: base + create, PlanOnly: true},
		// Fix for the spurious-error regression: omitting a non-default
		// managed_password_interval_in_days (45, set above) after create must
		// carry the value forward, not error and not diff.
		{Config: base + createOmitInterval, PlanOnly: true},
		{
			Config: base + updated,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("activedirectory_gmsa.svc", "description", "updated"),
				resource.TestCheckResourceAttr("activedirectory_gmsa.svc", "dns_hostname", host2),
				resource.TestCheckResourceAttr("activedirectory_gmsa.svc", "kerberos_encryption_type.#", "2"),
				resource.TestCheckTypeSetElemAttr("activedirectory_gmsa.svc",
					"kerberos_encryption_type.*", "AES128"),
				resource.TestCheckTypeSetElemAttr("activedirectory_gmsa.svc",
					"kerberos_encryption_type.*", "AES256"),
				resource.TestCheckResourceAttr("activedirectory_gmsa.svc", "service_principal_names.#", "2"),
				resource.TestCheckTypeSetElemAttr("activedirectory_gmsa.svc",
					"service_principal_names.*", spn2a),
				resource.TestCheckTypeSetElemAttr("activedirectory_gmsa.svc",
					"service_principal_names.*", spn2b),
			),
		},
		{
			Config: base + cleared,
			Check:  resource.TestCheckResourceAttr("activedirectory_gmsa.svc", "description", ""),
		},
		{
			Config: base + moved,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("activedirectory_gmsa.svc", "name", renamed),
				resource.TestCheckResourceAttr("activedirectory_gmsa.svc", "dn",
					"CN="+renamed+",OU="+movedOU+","+e.Container),
				captureAttr("activedirectory_gmsa.svc", "id", &movedID),
				func(*terraform.State) error {
					if movedID != createID {
						return fmt.Errorf("rename+move must be in-place: objectGUID changed %q -> %q",
							createID, movedID)
					}
					return nil
				},
			),
		},
		{
			ResourceName: "activedirectory_gmsa.svc",
			ImportState:  true,
			// principals_allowed_to_retrieve_managed_password and
			// service_principal_names are Optional but not Computed. Import
			// starts from a skeleton state holding only "id"; apply()
			// (resource_gmsa.go) only refreshes either set from Active
			// Directory's actual value when the model already holds a
			// non-null value for it, so the freshly imported state always
			// reads them back null — even though service_principal_names
			// genuinely holds two SPNs in the pre-import state above.
			// Verifying them here would fail on that null-on-a-skeleton
			// behavior, not on a bug, so both are excluded.
			ImportStateVerify:       true,
			ImportStateVerifyIgnore: []string{"principals_allowed_to_retrieve_managed_password", "service_principal_names"},
		},
	}
}

// gmsaDataSourceSteps creates a managed gMSA and reads it back through the
// activedirectory_gmsa data source, keyed by objectGUID. It asserts the data
// source resolves the account and projects sam_account_name un-suffixed (the
// same controller ruling apply() enforces on the resource) plus both sets
// (kerberos_encryption_type, service_principal_names).
func gmsaDataSourceSteps(e suiteEnv) []resource.TestStep {
	ou := accNamePrefix + "ds-gmsa-ou"
	name := accNamePrefix + "ds-gmsa"
	host := name + "01." + e.upnSuffix()
	spn := "HTTP/" + host

	config := e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "src" {
  name      = %q
  container = %q
}
resource "activedirectory_gmsa" "src" {
  name                     = %q
  container                = activedirectory_ou.src.dn
  dns_hostname             = %q
  description              = "data source read-back"
  service_principal_names  = [%q]
  kerberos_encryption_type = ["AES256"]
}

data "activedirectory_gmsa" "src" {
  guid = activedirectory_gmsa.src.id
}`, ou, e.Container, name, host, spn)

	return []resource.TestStep{{
		Config: config,
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttrPair(
				"data.activedirectory_gmsa.src", "id", "activedirectory_gmsa.src", "id"),
			resource.TestCheckResourceAttrPair(
				"data.activedirectory_gmsa.src", "sid", "activedirectory_gmsa.src", "sid"),
			resource.TestCheckResourceAttr("data.activedirectory_gmsa.src", "name", name),
			// Controller ruling mirrored from the resource: the un-suffixed base,
			// not Active Directory's own trailing "$".
			resource.TestCheckResourceAttr("data.activedirectory_gmsa.src", "sam_account_name", name),
			resource.TestCheckResourceAttr("data.activedirectory_gmsa.src", "dns_hostname", host),
			resource.TestCheckResourceAttr("data.activedirectory_gmsa.src",
				"description", "data source read-back"),
			resource.TestCheckResourceAttr("data.activedirectory_gmsa.src", "container", e.dn("OU="+ou)),
			resource.TestCheckResourceAttr("data.activedirectory_gmsa.src",
				"kerberos_encryption_type.#", "1"),
			resource.TestCheckTypeSetElemAttr("data.activedirectory_gmsa.src",
				"kerberos_encryption_type.*", "AES256"),
			resource.TestCheckResourceAttr("data.activedirectory_gmsa.src",
				"service_principal_names.#", "1"),
			resource.TestCheckTypeSetElemAttr("data.activedirectory_gmsa.src",
				"service_principal_names.*", spn),
		),
	}}
}

// ---------------------------------------------------------------------------
// Computer accounts
// ---------------------------------------------------------------------------

// computerLifecycleSteps covers create with SPNs and a description, a no-diff
// replan, an attribute update that touches every mutable field including the
// two delegation forms (constrained via allowed_to_delegate_to and RBCD via
// principals_allowed_to_delegate_to_account) and clearing description, rename
// plus move in one step, a name past the 15-character NetBIOS warn threshold
// (warning, not error — Active Directory does not enforce the limit on
// computer accounts), and import by objectGUID.
//
// RBCD needs an existing objectGUID to point at: a second computer ("helper")
// is created alongside "svc" and referenced by
// principals_allowed_to_delegate_to_account from the update step onward, kept
// unchanged on every subsequent config so the set never churns — the same
// pattern gmsaLifecycleSteps uses for
// principals_allowed_to_retrieve_managed_password.
func computerLifecycleSteps(e suiteEnv) []resource.TestStep {
	ou := accNamePrefix + "cpu-ou"
	movedOU := accNamePrefix + "cpu-ou2"
	name := accNamePrefix + "cpu"
	renamed := accNamePrefix + "cpu2"
	mgr := accNamePrefix + "cpu-mgr"
	helper := accNamePrefix + "cpu-helper"
	// 16 characters total (accNamePrefix is 6): past computerNameWarnLen (15),
	// so sam_account_name — left to default from name — trips the warn-only
	// NetBIOS-length validator. Active Directory itself accepts it; the apply
	// below must succeed, not error.
	longName := accNamePrefix + "1234567890"
	host1 := name + "01." + e.upnSuffix()
	host2 := name + "02." + e.upnSuffix()
	spn1 := "HOST/" + host1
	spn2a := "HOST/" + host2
	spn2b := "WSMAN/" + host2
	atd := "HTTP/" + host2

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
resource "activedirectory_computer" "helper" {
  name      = %q
  container = activedirectory_ou.staff.dn
}
`, ou, e.Container, mgr, mgr, helper)

	create := fmt.Sprintf(`
resource "activedirectory_computer" "svc" {
  name                     = %q
  container                = activedirectory_ou.staff.dn
  description              = "initial"
  enabled                  = true
  dns_hostname             = %q
  service_principal_names  = [%q]
}`, name, host1, spn1)

	// Every mutable attribute touched in one apply: dns_hostname and the SPN
	// set changed, plus every field create left at its default — display_name,
	// location, managed_by (pointed at a group the suite creates, the same
	// reasoning groupLifecycleSteps documents for its own managed_by),
	// trusted_for_delegation, kerberos_encryption_type, account_expiration_date,
	// constrained delegation (allowed_to_delegate_to) and RBCD
	// (principals_allowed_to_delegate_to_account, pointed at the helper
	// computer's objectGUID).
	updated := fmt.Sprintf(`
resource "activedirectory_computer" "svc" {
  name                                       = %q
  container                                  = activedirectory_ou.staff.dn
  description                                = "initial"
  enabled                                    = true
  dns_hostname                               = %q
  service_principal_names                    = [%q, %q]
  display_name                               = "Service Computer"
  location                                   = "DC1/Rack1"
  managed_by                                 = activedirectory_group.mgr.dn
  trusted_for_delegation                     = true
  kerberos_encryption_type                   = ["AES128", "AES256"]
  account_expiration_date                    = "2027-06-01T12:00:00Z"
  allowed_to_delegate_to                     = [%q]
  principals_allowed_to_delegate_to_account  = [activedirectory_computer.helper.id]
}`, name, host2, spn2a, spn2b, atd)

	// description carries a load-bearing empty default, so setting it to ""
	// must clear it rather than retain the prior value. Every other field is
	// held at the value updated set, so the set never churns.
	cleared := fmt.Sprintf(`
resource "activedirectory_computer" "svc" {
  name                                       = %q
  container                                  = activedirectory_ou.staff.dn
  description                                = ""
  enabled                                    = true
  dns_hostname                               = %q
  service_principal_names                    = [%q, %q]
  display_name                               = "Service Computer"
  location                                   = "DC1/Rack1"
  managed_by                                 = activedirectory_group.mgr.dn
  trusted_for_delegation                     = true
  kerberos_encryption_type                   = ["AES128", "AES256"]
  account_expiration_date                    = "2027-06-01T12:00:00Z"
  allowed_to_delegate_to                     = [%q]
  principals_allowed_to_delegate_to_account  = [activedirectory_computer.helper.id]
}`, name, host2, spn2a, spn2b, atd)

	// Rename and move in one step, into a freshly introduced OU. The objectGUID
	// must survive: this is an attribute update, not a replace.
	moved := fmt.Sprintf(`
resource "activedirectory_ou" "moved" {
  name      = %q
  container = %q
}
resource "activedirectory_computer" "svc" {
  name                                       = %q
  container                                  = activedirectory_ou.moved.dn
  description                                = ""
  enabled                                    = true
  dns_hostname                               = %q
  service_principal_names                    = [%q, %q]
  display_name                               = "Service Computer"
  location                                   = "DC1/Rack1"
  managed_by                                 = activedirectory_group.mgr.dn
  trusted_for_delegation                     = true
  kerberos_encryption_type                   = ["AES128", "AES256"]
  account_expiration_date                    = "2027-06-01T12:00:00Z"
  allowed_to_delegate_to                     = [%q]
  principals_allowed_to_delegate_to_account  = [activedirectory_computer.helper.id]
}`, movedOU, e.Container, renamed, host2, spn2a, spn2b, atd)

	// A second, independent computer whose name is past the 15-character
	// NetBIOS warn threshold. The point of this step is that the apply
	// succeeds — computerEffectiveSamValidator only warns, it must never
	// error, because Active Directory itself accepts the name.
	long := moved + fmt.Sprintf(`
resource "activedirectory_computer" "long" {
  name      = %q
  container = activedirectory_ou.staff.dn
}`, longName)

	var createID, movedID string

	return []resource.TestStep{
		{
			Config: base + create,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("activedirectory_computer.svc", "id"),
				resource.TestCheckResourceAttrSet("activedirectory_computer.svc", "sid"),
				resource.TestCheckResourceAttr("activedirectory_computer.svc", "dn",
					"CN="+name+",OU="+ou+","+e.Container),
				resource.TestCheckResourceAttr("activedirectory_computer.svc", "sam_account_name", name),
				resource.TestCheckResourceAttr("activedirectory_computer.svc", "description", "initial"),
				resource.TestCheckResourceAttr("activedirectory_computer.svc", "enabled", "true"),
				resource.TestCheckResourceAttr("activedirectory_computer.svc", "dns_hostname", host1),
				resource.TestCheckResourceAttr("activedirectory_computer.svc", "service_principal_names.#", "1"),
				resource.TestCheckTypeSetElemAttr("activedirectory_computer.svc",
					"service_principal_names.*", spn1),
				resource.TestCheckResourceAttr("activedirectory_computer.svc", "trusted_for_delegation", "false"),
				captureAttr("activedirectory_computer.svc", "id", &createID),
			),
		},
		{Config: base + create, PlanOnly: true},
		{
			Config: base + updated,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("activedirectory_computer.svc", "dns_hostname", host2),
				resource.TestCheckResourceAttr("activedirectory_computer.svc", "service_principal_names.#", "2"),
				resource.TestCheckTypeSetElemAttr("activedirectory_computer.svc",
					"service_principal_names.*", spn2a),
				resource.TestCheckTypeSetElemAttr("activedirectory_computer.svc",
					"service_principal_names.*", spn2b),
				resource.TestCheckResourceAttr("activedirectory_computer.svc", "display_name", "Service Computer"),
				resource.TestCheckResourceAttr("activedirectory_computer.svc", "location", "DC1/Rack1"),
				resource.TestCheckResourceAttr("activedirectory_computer.svc", "managed_by",
					"CN="+mgr+",OU="+ou+","+e.Container),
				resource.TestCheckResourceAttr("activedirectory_computer.svc", "trusted_for_delegation", "true"),
				resource.TestCheckResourceAttr("activedirectory_computer.svc", "kerberos_encryption_type.#", "2"),
				resource.TestCheckTypeSetElemAttr("activedirectory_computer.svc",
					"kerberos_encryption_type.*", "AES128"),
				resource.TestCheckTypeSetElemAttr("activedirectory_computer.svc",
					"kerberos_encryption_type.*", "AES256"),
				resource.TestCheckResourceAttr("activedirectory_computer.svc",
					"account_expiration_date", "2027-06-01T12:00:00Z"),
				resource.TestCheckResourceAttr("activedirectory_computer.svc", "allowed_to_delegate_to.#", "1"),
				resource.TestCheckTypeSetElemAttr("activedirectory_computer.svc",
					"allowed_to_delegate_to.*", atd),
				resource.TestCheckResourceAttr("activedirectory_computer.svc",
					"principals_allowed_to_delegate_to_account.#", "1"),
				resource.TestCheckTypeSetElemAttrPair("activedirectory_computer.svc",
					"principals_allowed_to_delegate_to_account.*",
					"activedirectory_computer.helper", "id"),
			),
		},
		{
			Config: base + cleared,
			Check:  resource.TestCheckResourceAttr("activedirectory_computer.svc", "description", ""),
		},
		{
			Config: base + moved,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("activedirectory_computer.svc", "name", renamed),
				resource.TestCheckResourceAttr("activedirectory_computer.svc", "dn",
					"CN="+renamed+",OU="+movedOU+","+e.Container),
				captureAttr("activedirectory_computer.svc", "id", &movedID),
				func(*terraform.State) error {
					if movedID != createID {
						return fmt.Errorf("rename+move must be in-place: objectGUID changed %q -> %q",
							createID, movedID)
					}
					return nil
				},
			),
		},
		{
			Config: base + long,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("activedirectory_computer.long", "id"),
				resource.TestCheckResourceAttr("activedirectory_computer.long", "sam_account_name", longName),
			),
		},
		{
			ResourceName: "activedirectory_computer.svc",
			ImportState:  true,
			// service_principal_names, allowed_to_delegate_to and
			// principals_allowed_to_delegate_to_account are Optional but not
			// Computed. Import starts from a skeleton state holding only "id";
			// apply() (resource_computer.go) only refreshes any of the three
			// from Active Directory's actual value when the model already
			// holds a non-null value for it, so the freshly imported state
			// always reads them back null — the same behavior
			// gmsaLifecycleSteps documents for its own two such attributes.
			ImportStateVerify: true,
			ImportStateVerifyIgnore: []string{
				"service_principal_names", "allowed_to_delegate_to", "principals_allowed_to_delegate_to_account",
			},
		},
	}
}

// computerDataSourceSteps creates a computer with an SPN and a delegation
// attribute set, then reads it back through the activedirectory_computer data
// source, keyed by sam_account_name. It asserts the data source resolves the
// account and projects both an OS field (operating_system, read-only and
// empty against the fake and a freshly created account — the joined machine
// owns it, nothing joins in this suite) and a delegation field
// (trusted_for_delegation) alongside the identity and core metadata fields.
func computerDataSourceSteps(e suiteEnv) []resource.TestStep {
	ou := accNamePrefix + "ds-cpu-ou"
	name := accNamePrefix + "ds-cpu"
	host := name + "01." + e.upnSuffix()
	spn := "HOST/" + host

	config := e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "src" {
  name      = %q
  container = %q
}
resource "activedirectory_computer" "src" {
  name                     = %q
  container                = activedirectory_ou.src.dn
  dns_hostname             = %q
  description              = "data source read-back"
  service_principal_names  = [%q]
  trusted_for_delegation   = true
}

data "activedirectory_computer" "src" {
  sam_account_name = activedirectory_computer.src.sam_account_name
}`, ou, e.Container, name, host, spn)

	return []resource.TestStep{{
		Config: config,
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttrPair(
				"data.activedirectory_computer.src", "id", "activedirectory_computer.src", "id"),
			resource.TestCheckResourceAttrPair(
				"data.activedirectory_computer.src", "sid", "activedirectory_computer.src", "sid"),
			resource.TestCheckResourceAttr("data.activedirectory_computer.src", "name", name),
			resource.TestCheckResourceAttr("data.activedirectory_computer.src", "sam_account_name", name),
			resource.TestCheckResourceAttr("data.activedirectory_computer.src", "dns_hostname", host),
			resource.TestCheckResourceAttr("data.activedirectory_computer.src",
				"description", "data source read-back"),
			resource.TestCheckResourceAttr("data.activedirectory_computer.src", "container", e.dn("OU="+ou)),
			resource.TestCheckResourceAttr("data.activedirectory_computer.src", "trusted_for_delegation", "true"),
			resource.TestCheckResourceAttr("data.activedirectory_computer.src",
				"service_principal_names.#", "1"),
			resource.TestCheckTypeSetElemAttr("data.activedirectory_computer.src",
				"service_principal_names.*", spn),
			// operating_system is read-only and machine-owned; a freshly created,
			// never-joined account reports "" against both backends. The check
			// still proves the field is present (not null) in the projection.
			resource.TestCheckResourceAttr("data.activedirectory_computer.src", "operating_system", ""),
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

// computersDataSourceSteps creates two computers under a dedicated OU sharing
// a description, then searches for them through activedirectory_computers
// with a one_level scope and a filter_by equality term. depends_on defers the
// search until both computers exist. Scoping under a freshly created OU keeps
// the result set to exactly the suite's own objects on a shared domain, so the
// count assertion is stable against both backends.
func computersDataSourceSteps(e suiteEnv) []resource.TestStep {
	ou := accNamePrefix + "dss-cpu-ou"
	a := accNamePrefix + "dss-cpu-a"
	b := accNamePrefix + "dss-cpu-b"
	hostA := a + "01." + e.upnSuffix()
	hostB := b + "01." + e.upnSuffix()
	config := e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "src" {
  name      = %q
  container = %q
}
resource "activedirectory_computer" "a" {
  name         = %q
  container    = activedirectory_ou.src.dn
  dns_hostname = %q
  description  = "tfacc-search-target"
}
resource "activedirectory_computer" "b" {
  name         = %q
  container    = activedirectory_ou.src.dn
  dns_hostname = %q
  description  = "tfacc-search-target"
}

data "activedirectory_computers" "src" {
  container  = activedirectory_ou.src.dn
  scope      = "one_level"
  filter_by  = { description = "tfacc-search-target" }
  depends_on = [activedirectory_computer.a, activedirectory_computer.b]
}`, ou, e.Container, a, hostA, b, hostB)

	return []resource.TestStep{{
		Config: config,
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("data.activedirectory_computers.src", "computers.#", "2"),
			resource.TestCheckTypeSetElemNestedAttrs(
				"data.activedirectory_computers.src", "computers.*",
				map[string]string{"sam_account_name": a}),
			resource.TestCheckTypeSetElemNestedAttrs(
				"data.activedirectory_computers.src", "computers.*",
				map[string]string{"sam_account_name": b, "description": "tfacc-search-target"}),
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

// groupMembersRecursiveDataSourceSteps proves the data source's recursive flag:
// with user ∈ child ∈ parent, a direct read of parent returns the child group,
// and a recursive read returns the leaf user (the group flattened away).
func groupMembersRecursiveDataSourceSteps(e suiteEnv) []resource.TestStep {
	usr := accNamePrefix + "rec-usr"
	base := nestedBase(e, accNamePrefix+"rec-ou", accNamePrefix+"rec-parent",
		accNamePrefix+"rec-child", usr, usr+"@"+e.upnSuffix())

	edges := `
resource "activedirectory_group_membership" "child" {
  group_id = activedirectory_group.child.id
  members  = [activedirectory_user.u.id]
}
resource "activedirectory_group_membership" "parent" {
  group_id = activedirectory_group.parent.id
  members  = [activedirectory_group.child.id]
}

data "activedirectory_group_members" "direct" {
  guid       = activedirectory_group.parent.id
  depends_on = [activedirectory_group_membership.parent]
}
data "activedirectory_group_members" "effective" {
  guid       = activedirectory_group.parent.id
  recursive  = true
  depends_on = [activedirectory_group_membership.parent, activedirectory_group_membership.child]
}`

	return []resource.TestStep{{
		Config: base + edges,
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("data.activedirectory_group_members.direct", "members.#", "1"),
			resource.TestCheckTypeSetElemNestedAttrs(
				"data.activedirectory_group_members.direct", "members.*",
				map[string]string{"class": "group"}),
			resource.TestCheckResourceAttr("data.activedirectory_group_members.effective", "members.#", "1"),
			resource.TestCheckTypeSetElemNestedAttrs(
				"data.activedirectory_group_members.effective", "members.*",
				map[string]string{"class": "user"}),
		),
	}}
}

// accessRuleSteps proves depth beyond a single ACE: three independent rules
// coexist on the same target (single-right + object_type, multi-right, and an
// independent Deny), a no-diff replan, a replace-as-revoke+grant when an
// existing rule's Type flips Allow->Deny (access_rule is replace-only — every
// attribute forces a replace, so Update is unreachable), and round-trip
// through import. The ACE's friendly names (object_type "Reset Password",
// applies_to.object_class "user") resolve to GUIDs and the trustee resolves
// to a SID, none of which are echoed back verbatim on import (hence the
// ImportStateVerifyIgnore below).
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

	// reset: the original single ExtendedRight ACE.
	reset := `
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

	// write: a second, independent ACE on the same target with two rights and no
	// object_type (all properties). Proves multiple ACEs coexist and multi-right
	// mapping.
	write := `
resource "activedirectory_access_rule" "write" {
  target  = activedirectory_ou.t.dn
  trustee = activedirectory_group.helpdesk.id
  rights  = ["ReadProperty", "WriteProperty"]
  applies_to = {
    scope        = "descendants"
    object_class = "user"
  }
  type = "Allow"
}`

	// writeDeny: the same rule as write but Type flipped Allow->Deny. Because the
	// resource is replace-only, applying this destroys the Allow ACE and creates
	// the Deny ACE; the DACL must end with exactly the Deny form.
	writeDeny := `
resource "activedirectory_access_rule" "write" {
  target  = activedirectory_ou.t.dn
  trustee = activedirectory_group.helpdesk.id
  rights  = ["ReadProperty", "WriteProperty"]
  applies_to = {
    scope        = "descendants"
    object_class = "user"
  }
  type = "Deny"
}`

	// No standalone second Deny ACE on the same (trustee, type, scope) is used
	// here on purpose. Real Active Directory coalesces same-scope ACEs by
	// union-ing their access masks (Grant maps to .NET AddAccessRule), so two
	// overlapping Deny rules that differ only in rights merge into one ACE — the
	// subset rule then cannot be read back or revoked as an independent resource.
	// Deny coverage comes from the writeDeny replacement below instead, which
	// coexists with the Allow reset ACE (a different type/objectType, so no merge).

	return []resource.TestStep{
		{
			Config: base + reset + write,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("activedirectory_access_rule.reset", "trustee_sid"),
				resource.TestCheckResourceAttr("activedirectory_access_rule.reset", "type", "Allow"),
				resource.TestCheckResourceAttr("activedirectory_access_rule.write", "rights.#", "2"),
				resource.TestCheckResourceAttr("activedirectory_access_rule.write", "type", "Allow"),
			),
		},
		{Config: base + reset + write, PlanOnly: true},
		{
			// Replace proof: flip write from Allow to Deny. Because the resource
			// is replace-only, this destroys the Allow ACE and creates the Deny
			// one; the DACL must end with exactly the Deny form and replan clean.
			Config: base + reset + writeDeny,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("activedirectory_access_rule.write", "type", "Deny"),
				resource.TestCheckResourceAttr("activedirectory_access_rule.write", "rights.#", "2"),
			),
		},
		{Config: base + reset + writeDeny, PlanOnly: true},
		{
			ResourceName:            "activedirectory_access_rule.reset",
			ImportState:             true,
			ImportStateVerify:       true,
			ImportStateVerifyIgnore: []string{"target", "trustee", "object_type", "applies_to"},
			Config:                  base + reset + writeDeny,
		},
	}
}
