package provider_test

import (
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// accSkipIfForcedSyncUnsupported skips a suite whose assertion is specifically
// about a FORCED sync, on a cell that cannot force one. All three suites here
// state force_sync = true in their own replication block, and the provider
// refuses that under dialect = "psopenad" by design.
//
// This is a deliberate exception to the harness rule that a missing variable is
// fatal rather than a skip. That rule exists so a half-configured run cannot
// report success having proven nothing; this is a cell the design says does not
// exist — the same status winrm + cold has in the matrix. The skip names its
// reason in the log, where a negative -run regex in lab.mk would be both
// fragile (RE2 has no negative lookahead) and invisible.
func accSkipIfForcedSyncUnsupported(t *testing.T) {
	t.Helper()
	if os.Getenv(envDialect) == "psopenad" {
		t.Skip("dialect = psopenad refuses force_sync (a rootDSE modify PSOpenAD cannot " +
			"express); this suite asserts a forced sync specifically")
	}
}

// What only real AD proves here: force_sync actually shortens the wait, and the
// timeout-saves-state contract holds. Replication is a property of domain
// topology, so there is nothing for the fake to model.
//
// The suite assumes default intra-site replication scheduling, where a write
// reaches another DC on a notification delay measured in seconds rather than
// instantly. With force_sync off and the wait set to 1ms, the object cannot have
// arrived by the first verification poll, so the wait fails on purpose — which
// is the only way to observe that the state was nonetheless saved.
func TestAccReplicationTimeoutSavesState(t *testing.T) {
	accSkipIfForcedSyncUnsupported(t)
	container := os.Getenv(envContainer)
	second := os.Getenv(envSecondDC)
	ou := accNamePrefix + "repl"

	body := fmt.Sprintf(`
resource "activedirectory_ou" "repl" {
  name      = %q
  container = %q
}`, ou, container)

	impatient := accProviderConfig(fmt.Sprintf(`  replication {
    wait          = true
    targets       = [%q]
    force_sync    = false
    timeout       = "1ms"
    poll_interval = "1ms"
  }`, second))

	patient := accProviderConfig(fmt.Sprintf(`  replication {
    wait          = true
    targets       = [%q]
    force_sync    = true
    timeout       = "120s"
    poll_interval = "2s"
  }`, second))

	resource.Test(t, resource.TestCase{
		PreCheck:                 accPreCheck(t, envSecondDC),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             accCheckDestroy(t),
		Steps: []resource.TestStep{
			{
				Config: impatient + body,
				// The object exists; only the wait did not finish. The
				// diagnostic must say so, and must say the state was saved.
				ExpectError: regexp.MustCompile(`Replication wait timed out`),
			},
			{
				// The proof that the state really was saved: this apply has
				// nothing to create. Had the state been discarded, the create
				// would come back as already-exists.
				Config: patient + body,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("activedirectory_ou.repl", "id"),
					resource.TestCheckResourceAttr("activedirectory_ou.repl", "dn",
						"OU="+ou+","+container),
				),
			},
			{
				Config:   patient + body,
				PlanOnly: true,
			},
		},
	})
}

// The wait succeeding is the ordinary path, and a forced sync is what makes it
// finish in seconds rather than the quarter-hour passive replication is entitled
// to take.
func TestAccReplicationWaitSucceedsWithAForcedSync(t *testing.T) {
	accSkipIfForcedSyncUnsupported(t)
	container := os.Getenv(envContainer)
	second := os.Getenv(envSecondDC)
	ou := accNamePrefix + "repl-sync"

	config := accProviderConfig(fmt.Sprintf(`  replication {
    wait          = true
    targets       = [%q]
    force_sync    = true
    timeout       = "120s"
    poll_interval = "2s"
  }`, second)) + fmt.Sprintf(`
resource "activedirectory_ou" "repl" {
  name        = %q
  container   = %q
  description = "waited for replication"
}`, ou, container)

	resource.Test(t, resource.TestCase{
		PreCheck:                 accPreCheck(t, envSecondDC),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             accCheckDestroy(t),
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.TestCheckResourceAttr("activedirectory_ou.repl", "description",
					"waited for replication"),
			},
			{Config: config, PlanOnly: true},
		},
	})
}

// targets = ["all"] expands through Get-ADDomainController and excludes the
// pinned source. On a two-DC domain that is the same wait as naming the second
// DC, and it exercises the expansion path an operator is most likely to write.
func TestAccReplicationWaitForAllControllers(t *testing.T) {
	accSkipIfForcedSyncUnsupported(t)
	container := os.Getenv(envContainer)
	ou := accNamePrefix + "repl-all"

	config := accProviderConfig(`  replication {
    wait          = true
    targets       = ["all"]
    force_sync    = true
    timeout       = "120s"
    poll_interval = "2s"
  }`) + fmt.Sprintf(`
resource "activedirectory_ou" "repl" {
  name      = %q
  container = %q
}`, ou, container)

	resource.Test(t, resource.TestCase{
		PreCheck:                 accPreCheck(t, envSecondDC),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             accCheckDestroy(t),
		Steps: []resource.TestStep{
			{Config: config},
			{Config: config, PlanOnly: true},
		},
	})
}
