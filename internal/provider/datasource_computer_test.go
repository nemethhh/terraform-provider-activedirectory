package provider_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/nemethhh/go-adpwsh/transport/fake"
)

func TestComputerDataSourceAgainstTheFake(t *testing.T) {
	dir := fake.NewDirectory()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps:                    computerDataSourceSteps(fakeSuiteEnv()),
	})
}

// The same read-back, against a real domain: the data source resolves the
// computer by sAMAccountName and its projection matches Get-ADComputer's.
func TestAccComputerDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 accPreCheck(t),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             accCheckDestroy(t),
		Steps:                    computerDataSourceSteps(accSuiteEnv()),
	})
}
