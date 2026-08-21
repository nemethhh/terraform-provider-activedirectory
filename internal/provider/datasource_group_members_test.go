package provider_test

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/nemethhh/go-adpwsh/transport/fake"
	adlocal "github.com/nemethhh/go-adpwsh/transport/local"
)

func TestGroupMembersDataSourceAgainstTheFake(t *testing.T) {
	dir := fake.NewDirectory()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factoriesWith(dir),
		Steps:                    groupMembersDataSourceSteps(fakeSuiteEnv()),
	})
}

// The same read-back, against a real domain: the data source enumerates a
// group's direct members through Group.Members.
func TestAccGroupMembersDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 accPreCheck(t),
		ProtoV6ProviderFactories: accFactories(),
		CheckDestroy:             accCheckDestroy(t),
		Steps:                    groupMembersDataSourceSteps(accSuiteEnv()),
	})
}

// TestAccGroupMembersDataSourceLargeSet proves the data source does not truncate
// a group whose membership exceeds the 1500-entry ranged-retrieval page. It
// reuses the large-group fixture (provisioned in one PowerShell pass, since
// building thousands of users through the provider would spawn one pwsh each),
// then reads the group back through activedirectory_group_members by objectGUID
// and asserts every member is returned. Opt-in via AD_ACC_LARGE_COUNT, the same
// gate as TestAccGroupMembershipLargeSet, so an ordinary acceptance run does not
// pay for thousands of objects.
func TestAccGroupMembersDataSourceLargeSet(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("acceptance test; set TF_ACC=1")
	}
	countStr := os.Getenv(envLargeCount)
	if countStr == "" {
		t.Skipf("%s is not set; skipping the large-group data source suite", envLargeCount)
	}
	count, err := strconv.Atoi(countStr)
	if err != nil || count <= 0 {
		t.Fatalf("%s=%q must be a positive integer", envLargeCount, countStr)
	}
	container := os.Getenv(envContainer)
	if container == "" {
		t.Fatalf("%s must be set", envContainer)
	}

	ctx := context.Background()
	tr, err := adlocal.New(adlocal.Config{PwshPath: os.Getenv(envPwshPath), Timeout: 15 * time.Minute})
	if err != nil {
		t.Fatalf("start PowerShell: %v", err)
	}
	defer func() { _ = tr.Close() }()

	tag := accNamePrefix + "large-ds"
	prov, err := runLargeGroup(ctx, tr, map[string]any{
		"action": "provision", "base": container, "tag": tag, "count": count,
	})
	if err != nil {
		t.Fatalf("provision %d members: %v", count, err)
	}
	t.Cleanup(func() {
		if _, err := runLargeGroup(context.Background(), tr, map[string]any{
			"action": "teardown", "ou": prov.OU,
		}); err != nil {
			t.Errorf("teardown %s: %v", prov.OU, err)
		}
	})

	config := accProviderConfig() + fmt.Sprintf(`
data "activedirectory_group_members" "large" {
  guid = %q
}`, prov.GroupGUID)

	resource.Test(t, resource.TestCase{
		PreCheck:                 accPreCheck(t),
		ProtoV6ProviderFactories: accFactories(),
		Steps: []resource.TestStep{{
			Config: config,
			Check: resource.TestCheckResourceAttr(
				"data.activedirectory_group_members.large", "members.#", strconv.Itoa(count)),
		}},
	})
}
