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

// What only real AD proves here: the cmdlet layer accepts these values, not
// just the payload path. Each value is its own test case so a failure names the
// value that caused it.
func TestAccHostileInputRoundTrips(t *testing.T) {
	e := accSuiteEnv()
	for _, tt := range hostileValues {
		t.Run(tt.Name, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				PreCheck:                 accPreCheck(t),
				ProtoV6ProviderFactories: accFactories(),
				CheckDestroy:             accCheckDestroy(t),
				Steps:                    hostileDescriptionSteps(e, tt.Value),
			})
		})
	}
}

func TestAccHostileInputEscapedCommaInADN(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 accPreCheck(t),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             accCheckDestroy(t),
		Steps:                    hostileEscapedCommaSteps(accSuiteEnv()),
	})
}
