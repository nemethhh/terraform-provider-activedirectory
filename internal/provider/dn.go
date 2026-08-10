package provider

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// splitDN splits a distinguished name at its unescaped commas. An RDN value
// may contain an escaped comma — `OU=Sales\, EMEA` is one component, not two —
// so splitting on every comma is the parser bug this avoids.
func splitDN(dn string) []string {
	var out []string
	var cur strings.Builder
	for i := 0; i < len(dn); i++ {
		switch dn[i] {
		case '\\':
			cur.WriteByte(dn[i])
			if i+1 < len(dn) {
				i++
				cur.WriteByte(dn[i])
			}
		case ',':
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(dn[i])
		}
	}
	return append(out, cur.String())
}

// dnEqual reports whether two distinguished names name the same object.
// Active Directory matches attribute types and values case-insensitively and
// echoes a DN back in whatever case it stored, so a case-only difference is
// not a change. Whitespace around the RDN separator is insignificant too.
func dnEqual(a, b string) bool {
	ra, rb := splitDN(a), splitDN(b)
	if len(ra) != len(rb) {
		return false
	}
	for i := range ra {
		if !strings.EqualFold(strings.TrimSpace(ra[i]), strings.TrimSpace(rb[i])) {
			return false
		}
	}
	return true
}

// keepEquivalentDN keeps the prior state's spelling of a distinguished name
// when the configuration differs from it only by case or separator spacing.
// Without it, writing a container as `dc=CORP,dc=local` against a directory
// that stored `DC=corp,DC=local` plans a move of an object that is already
// where it belongs, and the plan never converges.
//
// Terraform permits this precisely because the value returned is the prior
// one: a provider may declare a configured value and a prior value
// functionally equivalent, but it may not invent a third.
type keepEquivalentDN struct{}

func (keepEquivalentDN) Description(_ context.Context) string {
	return "Treats distinguished names that differ only by case or separator spacing as unchanged."
}

func (m keepEquivalentDN) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (keepEquivalentDN) PlanModifyString(_ context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	// A null prior or a null config is a create or a removal, where there is
	// no equivalence to assert — and Terraform rejects the prior value in
	// either case.
	if req.StateValue.IsNull() || req.ConfigValue.IsNull() || req.PlanValue.IsUnknown() {
		return
	}
	if dnEqual(req.StateValue.ValueString(), req.PlanValue.ValueString()) {
		resp.PlanValue = req.StateValue
	}
}

var _ planmodifier.String = keepEquivalentDN{}

// dnFollowsNameAndContainer keeps the prior distinguished name in the plan
// unless the rename or move that would change it is actually planned.
//
// The framework marks a computed attribute unknown before attribute plan
// modifiers run, so `dn` arrives here as "known after apply" whenever any
// other attribute changes. Left alone it would report a change to the
// distinguished name on every description edit, and no plan would ever be
// empty. UseStateForUnknown is not the answer: `dn` really does change when
// the object is renamed or moved, and pinning it to the prior value would
// make apply return a value the plan did not predict.
type dnFollowsNameAndContainer struct{}

func (dnFollowsNameAndContainer) Description(_ context.Context) string {
	return "Holds the prior distinguished name unless a rename or move is planned."
}

func (m dnFollowsNameAndContainer) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (dnFollowsNameAndContainer) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	// Nothing to hold on create or destroy, and a value the framework already
	// knows is not this modifier's business.
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() || !resp.PlanValue.IsUnknown() {
		return
	}

	var planName, stateName, planContainer, stateContainer, stateDN types.String
	for _, get := range []struct {
		p    path.Path
		from interface {
			GetAttribute(context.Context, path.Path, any) diag.Diagnostics
		}
		target any
	}{
		{path.Root("name"), req.Plan, &planName},
		{path.Root("name"), req.State, &stateName},
		{path.Root("container"), req.Plan, &planContainer},
		{path.Root("container"), req.State, &stateContainer},
		{path.Root("dn"), req.State, &stateDN},
	} {
		resp.Diagnostics.Append(get.from.GetAttribute(ctx, get.p, get.target)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	// An unknown name or container is a rename or move whose target is not
	// settled yet, so the distinguished name is not knowable either.
	if planName.IsUnknown() || planContainer.IsUnknown() {
		return
	}
	if planName.Equal(stateName) && dnEqual(planContainer.ValueString(), stateContainer.ValueString()) {
		resp.PlanValue = stateDN
	}
}

var _ planmodifier.String = dnFollowsNameAndContainer{}
