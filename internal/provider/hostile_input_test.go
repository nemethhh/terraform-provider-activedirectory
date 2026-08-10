package provider_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/nemethhh/go-adpwsh/transport/fake"
)

func TestHostileInputRoundTripsAgainstTheFake(t *testing.T) {
	e := fakeSuiteEnv()
	for _, tt := range hostileValues {
		t.Run(tt.Name, func(t *testing.T) {
			dir := fake.NewDirectory()
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factoriesWith(dir),
				Steps:                    hostileDescriptionSteps(e, tt.Value),
			})
		})
	}
}

func TestHostileInputEscapedCommaInADNAgainstTheFake(t *testing.T) {
	dir := fake.NewDirectory()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps:                    hostileEscapedCommaSteps(fakeSuiteEnv()),
	})
}
