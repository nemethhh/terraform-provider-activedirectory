package provider_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/nemethhh/go-adpwsh/transport/fake"
)

func TestGroupsDataSourceAgainstTheFake(t *testing.T) {
	dir := fake.NewDirectory()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps:                    groupsDataSourceSteps(fakeSuiteEnv()),
	})
}

// The same search, against a real domain: activedirectory_groups resolves the
// groups the managed resources created, matching the raw ldap_filter wildcard
// through Get-ADGroup.
func TestAccGroupsDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 accPreCheck(t),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             accCheckDestroy(t),
		Steps:                    groupsDataSourceSteps(accSuiteEnv()),
	})
}
