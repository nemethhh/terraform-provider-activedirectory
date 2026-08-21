package provider_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/nemethhh/go-adpwsh/transport/fake"
)

func TestUsersDataSourceAgainstTheFake(t *testing.T) {
	dir := fake.NewDirectory()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps:                    usersDataSourceSteps(fakeSuiteEnv()),
	})
}

// The same search, against a real domain: activedirectory_users resolves the
// users the managed resources created under the search base.
func TestAccUsersDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 accPreCheck(t),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             accCheckDestroy(t),
		Steps:                    usersDataSourceSteps(accSuiteEnv()),
	})
}

// TestUsersDataSourceOverMaxResultsAgainstTheFake proves the search errors
// rather than truncating when the match count exceeds max_results: the library
// requests one row over the limit and finding it is the signal. The message is
// the KindTooManyResults rendering, not a partial result set.
func TestUsersDataSourceOverMaxResultsAgainstTheFake(t *testing.T) {
	dir := fake.NewDirectory()
	for _, n := range []string{"u1", "u2", "u3"} {
		dir.Seed("user", accNamePrefix+n, "DC=corp,DC=local", map[string]any{
			"samAccountName": accNamePrefix + n, "enabled": true, "sid": "S-1-5-21-" + n,
			"canChangePassword": true, "passwordExpires": true,
		})
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps: []resource.TestStep{{
			Config: providerConfig + `
data "activedirectory_users" "all" {
  max_results = 2
}`,
			ExpectError: regexp.MustCompile(`(?i)too many results`),
		}},
	})
}
