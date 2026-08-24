package provider_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/nemethhh/go-adpwsh/transport/fake"
)

func TestGMSADataSourceAgainstTheFake(t *testing.T) {
	dir := fake.NewDirectory()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps:                    gmsaDataSourceSteps(fakeSuiteEnv()),
	})
}

// The same read-back, against a real domain: the data source resolves the
// gMSA by objectGUID and its projection matches Get-ADServiceAccount's.
func TestAccGMSADataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 accPreCheck(t),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             accCheckDestroy(t),
		Steps:                    gmsaDataSourceSteps(accSuiteEnv()),
	})
}
