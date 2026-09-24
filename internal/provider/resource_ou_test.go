package provider_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/nemethhh/go-adcore/adcorefake"
	"github.com/nemethhh/go-adpwsh/transport/fake"
)

// The lifecycle assertions live in the builder and are shared. Running the
// same builder against both backends is what proves a user switching between
// them sees no difference — change the builder, never one entry point.
func TestOULifecycleAgainstTheFake(t *testing.T) {
	t.Run("pwsh", func(t *testing.T) {
		dir := fake.NewDirectory()
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: factoriesWith(dir),
			Steps:                    ouLifecycleSteps(fakeSuiteEnv()),
		})
	})
	t.Run("directory", func(t *testing.T) {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: factoriesWithDirectory(adcorefake.New(fakeSuiteEnv().Container)),
			Steps:                    ouLifecycleSteps(fakeSuiteEnv()),
		})
	})
}

func TestOUImportByDNAgainstTheFake(t *testing.T) {
	e := fakeSuiteEnv()
	dir := fake.NewDirectory()
	guid := dir.Seed("organizationalUnit", accNamePrefix+"ou-adopted", e.Container, map[string]any{
		"description": "adopted", "protected": true,
	})
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps:                    ouImportByDNSteps(e, guid),
	})
}

// Creating over an existing object must hand back a ready-to-paste import block
// rather than only naming the conflict.
func TestOUAlreadyExistsSuggestsImportAgainstTheFake(t *testing.T) {
	t.Run("pwsh", func(t *testing.T) {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: factoriesWith(fake.NewDirectory()),
			Steps:                    ouAlreadyExistsSteps(fakeSuiteEnv()),
		})
	})
	t.Run("directory", func(t *testing.T) {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: factoriesWithDirectory(adcorefake.New(fakeSuiteEnv().Container)),
			Steps:                    ouAlreadyExistsSteps(fakeSuiteEnv()),
		})
	})
}

func TestAccOUAlreadyExistsSuggestsImport(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 accPreCheck(t),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             accCheckDestroy(t),
		Steps:                    ouAlreadyExistsSteps(accSuiteEnv()),
	})
}

// The same steps the fake runs, against a real domain. What only real AD proves
// here: that the cmdlets accept what the fake accepted.
func TestAccOULifecycle(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 accPreCheck(t),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             accCheckDestroy(t),
		Steps:                    ouLifecycleSteps(accSuiteEnv()),
	})
}
