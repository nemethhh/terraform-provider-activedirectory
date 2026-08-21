package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	adpwsh "github.com/nemethhh/go-adpwsh"
)

func TestIdentityFromPicksTheSetForm(t *testing.T) {
	null := types.StringNull()
	cases := []struct {
		guid, dn, sid, sam types.String
		want               string
	}{
		{types.StringValue("11111111-1111-4111-8111-111111111111"), null, null, null, "guid:11111111-1111-4111-8111-111111111111"},
		{null, types.StringValue("OU=x,DC=corp,DC=local"), null, null, "dn:OU=x,DC=corp,DC=local"},
		{null, null, types.StringValue("S-1-5-21-1"), null, "sid:S-1-5-21-1"},
		{null, null, null, types.StringValue("jdoe"), "sam:jdoe"},
	}
	for _, c := range cases {
		got := identityFrom(c.guid, c.dn, c.sid, c.sam)
		if got == nil || got.String() != c.want {
			t.Fatalf("identityFrom = %v, want %s", got, c.want)
		}
	}
	_ = adpwsh.ByGUID // keep the import if the switch is refactored
}
