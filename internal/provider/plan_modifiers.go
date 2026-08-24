package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
)

// immutableAfterCreate rejects any attempt to change attr once the resource
// exists. Active Directory exposes no update path for the attributes this
// guards (a gMSA's managed_password_interval_in_days, set only at creation),
// so silently accepting a changed value would either be dropped on apply or
// require destroying and recreating the object — and this repo's one rule
// (see CLAUDE.md) is that nothing forces a replace, because deleting and
// recreating an AD object mints a new SID and drops every ACL naming it.
// immutableAfterCreate therefore fails the plan instead: it neither silently
// discards the change nor sets RequiresReplace, forcing the operator to make
// the destroy/recreate decision deliberately if they truly want a new value.
type immutableAfterCreate struct{ attr string }

func (m immutableAfterCreate) Description(_ context.Context) string {
	return m.attr + " is set once when the resource is created and cannot be changed in place."
}

func (m immutableAfterCreate) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (m immutableAfterCreate) PlanModifyInt64(_ context.Context, req planmodifier.Int64Request, resp *planmodifier.Int64Response) {
	// A null or unknown prior state means this is a create (or the value is
	// not yet known for another reason) — there is nothing to compare against.
	if req.StateValue.IsNull() || req.StateValue.IsUnknown() {
		return
	}
	if !req.PlanValue.Equal(req.StateValue) {
		resp.Diagnostics.AddAttributeError(req.Path, "Attribute cannot be changed",
			m.attr+" is set once when the gMSA is created and cannot be changed in place; "+
				"Active Directory exposes no update for it. Destroy and recreate the resource "+
				"deliberately if you must change it (this mints a new SID and drops ACLs naming it).")
	}
}

var _ planmodifier.Int64 = immutableAfterCreate{}
