package provider_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/nemethhh/go-adpwsh/transport/fake"
)

func TestOUDataSourceAgainstTheFake(t *testing.T) {
	dir := fake.NewDirectory()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps:                    ouDataSourceSteps(fakeSuiteEnv()),
	})
}

// The same read-back, against a real domain. What only real AD proves here: the
// data source resolves the OU the managed resource created and its projection
// matches Get-ADOrganizationalUnit's.
func TestAccOUDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 accPreCheck(t),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             accCheckDestroy(t),
		Steps:                    ouDataSourceSteps(accSuiteEnv()),
	})
}
