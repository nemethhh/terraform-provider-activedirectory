package provider_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/nemethhh/go-adpwsh/transport/fake"
)

// TestGMSALifecycleAgainstTheFake runs the gMSA lifecycle builder against the
// fake directory: create with a non-default Kerberos encryption set and
// rotation interval, an attribute update (description set then cleared,
// dns_hostname changed, kerberos_encryption_type grown, SPNs replaced),
// rename plus move in one step, and import by objectGUID.
func TestGMSALifecycleAgainstTheFake(t *testing.T) {
	dir := fake.NewDirectory()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps:                    gmsaLifecycleSteps(fakeSuiteEnv()),
	})
}

// The same steps the fake runs, against a real domain. What only real AD
// proves here: that New-/Set-/Get-ADServiceAccount accept what the fake
// accepted, including the rename+move folded into one Update call — the
// KDS-backed managed password itself is not modeled by the fake at all, so
// this is also the first suite that exercises a real gMSA end to end.
func TestAccGMSALifecycle(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 accPreCheck(t),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             accCheckDestroy(t),
		Steps:                    gmsaLifecycleSteps(accSuiteEnv()),
	})
}
