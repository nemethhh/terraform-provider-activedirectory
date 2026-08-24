package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"

	adpwsh "github.com/nemethhh/go-adpwsh"
)

// TestGMSAApplyStripsSamAccountNameSuffix exercises apply() directly. Active
// Directory appends "$" to a gMSA's sAMAccountName on every read
// (Get/Create/Update all return it that way), but sam_account_name must hold
// the un-suffixed base: that is what specFrom sends, what the library's own
// Update path compares against (stripping "$" off its own "current" read
// before diffing), and what the attribute's MarkdownDescription documents
// ("the effective sAMAccountName is one character longer than this value").
// Storing the suffixed form verbatim fails Terraform's plan-consistency
// check whenever sam_account_name is set explicitly, and turns every Update
// into a spurious rewrite. The full round trip through the fake is Task 9's
// lifecycle suite; this is the narrow, fast check on apply() itself, the way
// dn_test.go and validators_internal_test.go check their own pure-function
// edges directly.
func TestGMSAApplyStripsSamAccountNameSuffix(t *testing.T) {
	tests := []struct {
		name string
		sam  string
		want string
	}{
		{"suffixed, as every real AD read returns it", "svc-web$", "svc-web"},
		{"already unsuffixed left unchanged (defensive)", "svc-web", "svc-web"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &gmsaResource{}
			m := &gmsaModel{}
			var diags diag.Diagnostics
			r.apply(context.Background(), &adpwsh.GMSA{
				GUID:           "11111111-1111-1111-1111-111111111111",
				DN:             "CN=svc-web,OU=x,DC=corp,DC=local",
				Name:           "svc-web",
				SamAccountName: tt.sam,
				Container:      "OU=x,DC=corp,DC=local",
			}, m, &diags)
			if diags.HasError() {
				t.Fatalf("unexpected error: %v", diags)
			}
			if got := m.SamAccountName.ValueString(); got != tt.want {
				t.Errorf("SamAccountName = %q, want %q", got, tt.want)
			}
		})
	}
}
