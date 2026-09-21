package provider_test

import (
	"fmt"
	"regexp"
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
// rather than only naming the conflict. The fake is the right backend for this:
// it can seed the collision without a directory of its own.
func TestOUAlreadyExistsSuggestsImportAgainstTheFake(t *testing.T) {
	e := fakeSuiteEnv()
	name := accNamePrefix + "ou"
	dir := fake.NewDirectory()
	dir.Seed("organizationalUnit", name, e.Container, map[string]any{
		"description": "", "protected": true,
	})
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps: []resource.TestStep{{
			Config: e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "staff" {
  name      = %q
  container = %q
}`, name, e.Container),
			ExpectError: regexp.MustCompile(`import \{`),
		}},
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
