package provider_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/nemethhh/go-adpwsh/transport/fake"
)

func TestSamAccountNameValidatorRejectsBadValues(t *testing.T) {
	e := fakeSuiteEnv()
	cases := []struct {
		name    string
		sam     string
		wantErr *regexp.Regexp
	}{
		{"illegal_char", "bad,name", regexp.MustCompile(`(?i)must not contain`)},
		{"trailing_dot", "name.", regexp.MustCompile(`(?i)period or space`)},
		{"trailing_space", "name ", regexp.MustCompile(`(?i)period or space|must not contain`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_group" "g" {
  name             = "validator-probe"
  sam_account_name = %q
  container        = %q
}`, tc.sam, e.Container)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factoriesWith(fake.NewDirectory()),
				Steps:                    []resource.TestStep{{Config: cfg, ExpectError: tc.wantErr}},
			})
		})
	}
}

// TestUserSamAccountNameValidatorRejectsOverLong pins the USER sam ceiling at
// 20: the lab proved Active Directory rejects a down-level logon name past 20
// characters, so the user validator stays at 20 even though the group
// validator does not.
func TestUserSamAccountNameValidatorRejectsOverLong(t *testing.T) {
	e := fakeSuiteEnv()
	over := strings.Repeat("a", 21) // one past the 20-char user ceiling
	cfg := e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_user" "u" {
  sam_account_name = %q
  container        = %q
}`, over, e.Container)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factoriesWith(fake.NewDirectory()),
		Steps:                    []resource.TestStep{{Config: cfg, ExpectError: regexp.MustCompile(`(?i)length|20`)}},
	})
}

// TestGroupSamAccountNameValidatorRejectsOverLong pins the GROUP sam ceiling
// at 256 (the sAMAccountName schema ceiling): the lab proved Active Directory
// accepts and stores a 25-character group sam, so the 20-char user limit does
// not apply to groups.
func TestGroupSamAccountNameValidatorRejectsOverLong(t *testing.T) {
	e := fakeSuiteEnv()
	over := strings.Repeat("a", 257) // one past the 256-char group ceiling
	cfg := e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_group" "g" {
  name             = "validator-probe"
  sam_account_name = %q
  container        = %q
}`, over, e.Container)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factoriesWith(fake.NewDirectory()),
		Steps:                    []resource.TestStep{{Config: cfg, ExpectError: regexp.MustCompile(`(?i)length|256`)}},
	})
}

// TestGroupSamAccountNameValidatorAllowsUpTo256 pins the raised GROUP
// ceiling itself: a value at exactly 256 characters — well past the 20-char
// user limit — must plan cleanly. PlanOnly is enough: the length validator
// runs during plan (ValidateResourceConfig), so an accepted value yields a
// clean plan and a rejected one would fail there.
func TestGroupSamAccountNameValidatorAllowsUpTo256(t *testing.T) {
	e := fakeSuiteEnv()
	sam := strings.Repeat("a", 256) // exactly the new group ceiling
	cfg := e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_group" "g" {
  name             = "validator-probe"
  sam_account_name = %q
  container        = %q
}`, sam, e.Container)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factoriesWith(fake.NewDirectory()),
		Steps:                    []resource.TestStep{{Config: cfg, PlanOnly: true, ExpectNonEmptyPlan: true}},
	})
}

func TestCNLengthValidatorRejectsOverLong(t *testing.T) {
	e := fakeSuiteEnv()
	over := strings.Repeat("a", 65) // one past the 64-char cn ceiling
	cfg := e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "o" {
  name      = %q
  container = %q
}`, over, e.Container)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factoriesWith(fake.NewDirectory()),
		Steps:                    []resource.TestStep{{Config: cfg, ExpectError: regexp.MustCompile(`(?i)length|64`)}},
	})
}
