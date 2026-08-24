package provider_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/nemethhh/go-adpwsh/transport/fake"
)

// TestComputerLifecycleAgainstTheFake runs the computer lifecycle builder
// against the fake directory: create with SPNs and a description, a no-diff
// replan, an attribute update touching every mutable field (including both
// delegation forms), clearing description, rename plus move in one step, a
// name past the 15-character NetBIOS warn threshold, and import by
// objectGUID.
func TestComputerLifecycleAgainstTheFake(t *testing.T) {
	dir := fake.NewDirectory()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps:                    computerLifecycleSteps(fakeSuiteEnv()),
	})
}

// The same steps the fake runs, against a real domain. What only real AD
// proves here: that New-/Set-ADComputer accept what the fake accepted,
// including the rename+move folded into one Update call, the constrained and
// resource-based constrained delegation attributes, and that Active Directory
// itself does not reject a computer name past the NetBIOS limit.
func TestAccComputerLifecycle(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 accPreCheck(t),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             accCheckDestroy(t),
		Steps:                    computerLifecycleSteps(accSuiteEnv()),
	})
}
