package provider_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/nemethhh/go-adcore/adcorefake"
	"github.com/nemethhh/go-adpwsh/transport/fake"
)

func TestComputersDataSourceAgainstTheFake(t *testing.T) {
	t.Run("pwsh", func(t *testing.T) {
		dir := fake.NewDirectory()
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: factoriesWith(dir),
			Steps:                    computersDataSourceSteps(fakeSuiteEnv()),
		})
	})
	t.Run("directory", func(t *testing.T) {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: factoriesWithDirectory(adcorefake.New(fakeSuiteEnv().Container)),
			Steps:                    computersDataSourceSteps(fakeSuiteEnv()),
		})
	})
}

// The same search, against a real domain: activedirectory_computers resolves
// the computers the managed resources created under the search base.
func TestAccComputersDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 accPreCheck(t),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             accCheckDestroy(t),
		Steps:                    computersDataSourceSteps(accSuiteEnv()),
	})
}
