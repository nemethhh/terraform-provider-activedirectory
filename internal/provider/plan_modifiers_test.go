package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// runInt64PlanModifier builds a minimal Int64Request/Response pair from a
// prior state value and a proposed plan value and runs the modifier against
// it directly. There is no gMSA resource yet (Task 8) to drive this through a
// real plan, so the request is built by hand rather than through
// resource.UnitTest.
func runInt64PlanModifier(t *testing.T, m planmodifier.Int64, state, plan types.Int64) *planmodifier.Int64Response {
	t.Helper()
	req := planmodifier.Int64Request{
		Path:       path.Root("managed_password_interval_in_days"),
		StateValue: state,
		PlanValue:  plan,
	}
	resp := &planmodifier.Int64Response{PlanValue: plan}
	m.PlanModifyInt64(context.Background(), req, resp)
	return resp
}

func TestImmutableAfterCreate(t *testing.T) {
	m := immutableAfterCreate{attr: "managed_password_interval_in_days"}

	t.Run("state known, plan differs -> error", func(t *testing.T) {
		resp := runInt64PlanModifier(t, m, types.Int64Value(30), types.Int64Value(60))
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected error changing a create-only attribute")
		}
	})

	t.Run("state null (create), plan set -> no error", func(t *testing.T) {
		resp := runInt64PlanModifier(t, m, types.Int64Null(), types.Int64Value(60))
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected error on create: %v", resp.Diagnostics)
		}
	})

	t.Run("state known, plan same -> no error", func(t *testing.T) {
		resp := runInt64PlanModifier(t, m, types.Int64Value(30), types.Int64Value(30))
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected error on unchanged value: %v", resp.Diagnostics)
		}
	})
}
