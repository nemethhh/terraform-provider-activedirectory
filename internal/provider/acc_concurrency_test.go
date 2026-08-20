package provider_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// accConcurrentCount is above Terraform's default parallelism of 10, so the
// graph really does run more operations than the transport permits processes.
const accConcurrentCount = 12

// What only real AD proves here: bounded concurrency under real latency. The
// fake has no processes and no latency, so an unbounded transport looks
// identical against it. Against a real domain, max_concurrency = 4 with
// Terraform running 10 operations at once is where an unbounded transport would
// put ten pwsh processes on the host, each paying its own
// Import-Module ActiveDirectory.
func TestAccConcurrentCreates(t *testing.T) {
	e := accSuiteEnv()

	config := e.ProviderConfig + fmt.Sprintf(`
resource "activedirectory_ou" "fan" {
  count     = %d
  name      = "%sfan-${count.index}"
  container = %q
}`, accConcurrentCount, accNamePrefix, e.Container)

	checks := make([]resource.TestCheckFunc, 0, accConcurrentCount*2)
	for i := 0; i < accConcurrentCount; i++ {
		// A counted resource is addressed with a dotted index in the state the
		// test framework shims, not the bracketed form configuration uses:
		// `fan.0`, never `fan[0]`. The bracketed form finds nothing and every
		// check fails with "Not found" while the apply itself succeeded.
		address := fmt.Sprintf("activedirectory_ou.fan.%d", i)
		checks = append(checks,
			resource.TestCheckResourceAttrSet(address, "id"),
			resource.TestCheckResourceAttr(address, "dn",
				fmt.Sprintf("OU=%sfan-%d,%s", accNamePrefix, i, e.Container)))
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 accPreCheck(t),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             accCheckDestroy(t),
		Steps: []resource.TestStep{
			{
				Config: config,
				Check:  resource.ComposeAggregateTestCheckFunc(checks...),
			},
			// A second plan with nothing changed must be empty. A bounded
			// transport that dropped, duplicated or crossed an operation shows
			// up here rather than as a mystery three applies later.
			{
				Config:   config,
				PlanOnly: true,
			},
		},
	})
}

// The same fan-out with the bound set to one. It is slower and it is the
// configuration an operator picks on a small jump host, so it should be known to
// work rather than assumed to.
func TestAccConcurrentCreatesWithASingleProcess(t *testing.T) {
	e := accSuiteEnv()
	// accProviderConfig writes max_concurrency = 4. A configuration has exactly
	// one provider block, so a different bound means rebuilding the whole block
	// rather than appending a second one.
	config := accProviderConfigWithConcurrency(1) + fmt.Sprintf(`
resource "activedirectory_ou" "serial" {
  count     = 4
  name      = "%sserial-${count.index}"
  container = %q
}`, accNamePrefix, e.Container)

	resource.Test(t, resource.TestCase{
		PreCheck:                 accPreCheck(t),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             accCheckDestroy(t),
		Steps: []resource.TestStep{
			{Config: config},
			{Config: config, PlanOnly: true},
		},
	})
}
