package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	adpwsh "github.com/nemethhh/go-adpwsh"
)

const groupMembershipResourceType = "activedirectory_group_membership"

type groupMembershipResource struct {
	client *adpwsh.Client
}

func newGroupMembershipResource() resource.Resource { return &groupMembershipResource{} }

type groupMembershipModel struct {
	ID       types.String   `tfsdk:"id"`
	GroupID  types.String   `tfsdk:"group_id"`
	Members  types.Set      `tfsdk:"members"`
	Timeouts timeouts.Value `tfsdk:"timeouts"`
}

func (r *groupMembershipResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = groupMembershipResourceType
}

func (r *groupMembershipResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	guidValidator := stringvalidator.RegexMatches(guidPattern, "must be an objectGUID")
	resp.Schema = schema.Schema{
		MarkdownDescription: "A group's entire membership — an authoritative member " +
			"set. This resource owns every member of the group: a member present in " +
			"Active Directory but absent from `members` is removed on the next apply, " +
			"**including on the first apply**, so any membership added out of band is " +
			"reconciled away. Use at most one per group, and do not also manage the " +
			"group with `activedirectory_group_member`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				MarkdownDescription: "The group's objectGUID (one membership resource per group).",
			},
			"group_id": schema.StringAttribute{
				Required:      true,
				Validators:    []validator.String{guidValidator},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				MarkdownDescription: "The group's objectGUID. Changing it points the resource at a " +
					"different group, which replaces it.",
			},
			"members": schema.SetAttribute{
				Required:    true,
				ElementType: types.StringType,
				Validators: []validator.Set{
					setvalidator.ValueStringsAre(guidValidator),
				},
				MarkdownDescription: "The objectGUIDs of every member the group should have. " +
					"May be empty, which removes all members.",
			},
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{Create: true, Read: true, Update: true, Delete: true}),
		},
	}
}

func (r *groupMembershipResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

// reconcile drives the group's membership to exactly want, against the actual
// current membership read from the directory. It is authoritative: members
// present in AD but absent from want are removed.
func (r *groupMembershipResource) reconcile(ctx context.Context, group string, want []string) error {
	current, err := r.client.Group.Members(ctx, adpwsh.ByGUID(group))
	if err != nil {
		return err
	}
	have := make(map[string]bool, len(current))
	for _, m := range current {
		have[m.GUID] = true
	}
	wantSet := make(map[string]bool, len(want))
	for _, g := range want {
		wantSet[g] = true
	}
	var toAdd, toRemove []adpwsh.Identity
	for g := range wantSet {
		if !have[g] {
			toAdd = append(toAdd, adpwsh.ByGUID(g))
		}
	}
	for g := range have {
		if !wantSet[g] {
			toRemove = append(toRemove, adpwsh.ByGUID(g))
		}
	}
	if err := r.client.Group.AddMembers(ctx, adpwsh.ByGUID(group), toAdd); err != nil {
		return err
	}
	return r.client.Group.RemoveMembers(ctx, adpwsh.ByGUID(group), toRemove)
}

func (r *groupMembershipResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan groupMembershipModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel, diags := withTimeout(ctx, plan.Timeouts.Create)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	defer cancel()

	var want []string
	resp.Diagnostics.Append(plan.Members.ElementsAs(ctx, &want, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.reconcile(ctx, plan.GroupID.ValueString(), want); err != nil {
		resp.Diagnostics.Append(errorDiagnostics("Group.Members", groupMembershipResourceType, err)...)
		return
	}
	plan.ID = plan.GroupID
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *groupMembershipResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state groupMembershipModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel, diags := withTimeout(ctx, state.Timeouts.Read)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	defer cancel()

	current, err := r.client.Group.Members(ctx, adpwsh.ByGUID(state.GroupID.ValueString()))
	if isNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.Append(errorDiagnostics("Group.Members", groupMembershipResourceType, err)...)
		return
	}
	guids := make([]string, len(current))
	for i, m := range current {
		guids[i] = m.GUID
	}
	set, d := types.SetValueFrom(ctx, types.StringType, guids)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	state.Members = set
	state.ID = state.GroupID
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *groupMembershipResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state groupMembershipModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID = state.ID
	ctx, cancel, diags := withTimeout(ctx, plan.Timeouts.Update)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	defer cancel()

	var want []string
	resp.Diagnostics.Append(plan.Members.ElementsAs(ctx, &want, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.reconcile(ctx, plan.GroupID.ValueString(), want); err != nil {
		resp.Diagnostics.Append(errorDiagnostics("Group.Members", groupMembershipResourceType, err)...)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *groupMembershipResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state groupMembershipModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel, diags := withTimeout(ctx, state.Timeouts.Delete)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	defer cancel()

	var have []string
	resp.Diagnostics.Append(state.Members.ElementsAs(ctx, &have, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ids := make([]adpwsh.Identity, len(have))
	for i, g := range have {
		ids[i] = adpwsh.ByGUID(g)
	}
	if err := r.client.Group.RemoveMembers(ctx, adpwsh.ByGUID(state.GroupID.ValueString()), ids); err != nil {
		resp.Diagnostics.Append(errorDiagnostics("Group.RemoveMembers", groupMembershipResourceType, err)...)
	}
}

func (r *groupMembershipResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("group_id"), req.ID)...)
}

var (
	_ resource.Resource                = (*groupMembershipResource)(nil)
	_ resource.ResourceWithConfigure   = (*groupMembershipResource)(nil)
	_ resource.ResourceWithImportState = (*groupMembershipResource)(nil)
)
