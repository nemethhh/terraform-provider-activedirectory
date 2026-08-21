package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
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

const groupMemberResourceType = "activedirectory_group_member"

type groupMemberResource struct {
	client *adpwsh.Client
}

func newGroupMemberResource() resource.Resource { return &groupMemberResource{} }

type groupMemberModel struct {
	ID       types.String   `tfsdk:"id"`
	GroupID  types.String   `tfsdk:"group_id"`
	MemberID types.String   `tfsdk:"member_id"`
	Timeouts timeouts.Value `tfsdk:"timeouts"`
}

func (r *groupMemberResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = groupMemberResourceType
}

func (r *groupMemberResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	guidValidator := stringvalidator.RegexMatches(guidPattern, "must be an objectGUID")
	resp.Schema = schema.Schema{
		MarkdownDescription: "A single group membership edge — a non-authoritative " +
			"membership. Each resource manages exactly one member of one group and " +
			"leaves every other member untouched, so several may target the same " +
			"group and out-of-band members are never removed. To own a group's whole " +
			"member set instead, use `activedirectory_group_membership`; do not manage " +
			"the same group with both.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				MarkdownDescription: "`<group objectGUID>/<member objectGUID>`.",
			},
			"group_id": schema.StringAttribute{
				Required:      true,
				Validators:    []validator.String{guidValidator},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				MarkdownDescription: "The group's objectGUID. Changing it replaces the edge " +
					"(the old membership is removed and the new one added); an edge holds no SID " +
					"or ACL, so unlike the object resources this is a replace rather than a move.",
			},
			"member_id": schema.StringAttribute{
				Required:      true,
				Validators:    []validator.String{guidValidator},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				MarkdownDescription: "The member's objectGUID (a user, group, computer or other " +
					"security principal). Changing it replaces the edge.",
			},
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{Create: true, Read: true, Delete: true}),
		},
	}
}

func (r *groupMemberResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *groupMemberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan groupMemberModel
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

	group := plan.GroupID.ValueString()
	member := plan.MemberID.ValueString()
	if err := r.client.Group.AddMembers(ctx, adpwsh.ByGUID(group),
		[]adpwsh.Identity{adpwsh.ByGUID(member)}); err != nil {
		resp.Diagnostics.Append(errorDiagnostics("Group.AddMembers", groupMemberResourceType, err)...)
		return
	}
	plan.ID = types.StringValue(group + "/" + member)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *groupMemberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state groupMemberModel
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

	ok, err := r.client.Group.IsMember(ctx,
		adpwsh.ByGUID(state.GroupID.ValueString()), adpwsh.ByGUID(state.MemberID.ValueString()))
	if err != nil {
		resp.Diagnostics.Append(errorDiagnostics("Group.IsMember", groupMemberResourceType, err)...)
		return
	}
	if !ok {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update is unreachable: every non-computed attribute forces a replace. It
// exists only to satisfy the resource.Resource interface.
func (r *groupMemberResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan groupMemberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *groupMemberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state groupMemberModel
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

	if err := r.client.Group.RemoveMembers(ctx,
		adpwsh.ByGUID(state.GroupID.ValueString()),
		[]adpwsh.Identity{adpwsh.ByGUID(state.MemberID.ValueString())}); err != nil {
		resp.Diagnostics.Append(errorDiagnostics("Group.RemoveMembers", groupMemberResourceType, err)...)
	}
}

func (r *groupMemberResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	group, member, ok := strings.Cut(req.ID, "/")
	if !ok || group == "" || member == "" {
		resp.Diagnostics.AddError("Invalid import ID",
			fmt.Sprintf("Expected \"<group objectGUID>/<member objectGUID>\", got %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("group_id"), group)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("member_id"), member)...)
}

var (
	_ resource.Resource                = (*groupMemberResource)(nil)
	_ resource.ResourceWithConfigure   = (*groupMemberResource)(nil)
	_ resource.ResourceWithImportState = (*groupMemberResource)(nil)
)
