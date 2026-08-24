package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"

	adpwsh "github.com/nemethhh/go-adpwsh"
)

// TestComputerApplyStripsSamDollar exercises apply() directly. Active
// Directory appends "$" to a computer's sAMAccountName on every read
// (Get/Create/Update all return it that way), but sam_account_name must hold
// the un-suffixed base: that is what specFrom sends, what the library's own
// Update path compares against, and what the attribute's MarkdownDescription
// documents. Storing the suffixed form verbatim fails Terraform's
// plan-consistency check whenever sam_account_name is set explicitly and turns
// every Update into a spurious rewrite. The full round trip through the fake
// is the lifecycle suite's job; this is the narrow, fast check on apply()
// itself, the way dn_test.go and validators_internal_test.go check their own
// pure-function edges directly.
func TestComputerApplyStripsSamDollar(t *testing.T) {
	r := &computerResource{}
	m := &computerModel{}
	var diags diag.Diagnostics
	r.apply(context.Background(), &adpwsh.Computer{
		GUID: "g", DN: "CN=WEB01,OU=x,DC=corp,DC=local", Name: "WEB01",
		SamAccountName: "WEB01$", SID: "S-1-5-21-1", Container: "OU=x,DC=corp,DC=local",
	}, m, &diags)
	if diags.HasError() {
		t.Fatalf("unexpected error: %v", diags)
	}
	if m.SamAccountName.ValueString() != "WEB01" {
		t.Fatalf("want stripped sam WEB01, got %q", m.SamAccountName.ValueString())
	}
}
