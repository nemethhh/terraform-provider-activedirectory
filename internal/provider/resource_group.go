package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	adpwsh "github.com/nemethhh/go-adpwsh"
)

const groupResourceType = "activedirectory_group"

type groupResource struct {
	client *adpwsh.Client
}

func newGroupResource() resource.Resource { return &groupResource{} }

type groupModel struct {
	ID             types.String   `tfsdk:"id"`
	DN             types.String   `tfsdk:"dn"`
	SID            types.String   `tfsdk:"sid"`
	Name           types.String   `tfsdk:"name"`
	SamAccountName types.String   `tfsdk:"sam_account_name"`
	Container      types.String   `tfsdk:"container"`
	Scope          types.String   `tfsdk:"scope"`
	Category       types.String   `tfsdk:"category"`
	Description    types.String   `tfsdk:"description"`
	ManagedBy      types.String   `tfsdk:"managed_by"`
	Timeouts       timeouts.Value `tfsdk:"timeouts"`
}

func (r *groupResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = groupResourceType
}

func (r *groupResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A security or distribution group.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				MarkdownDescription: "The objectGUID. This is the only identity space membership " +
					"resources use, so configuration and state cannot disagree about a member.",
			},
			"dn": schema.StringAttribute{Computed: true,
				PlanModifiers:       []planmodifier.String{dnFollowsNameAndContainer{}},
				MarkdownDescription: "The distinguished name."},
			"sid": schema.StringAttribute{Computed: true,
				// A SID is minted once and outlives every rename and move.
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				MarkdownDescription: "The security identifier."},
			"name": schema.StringAttribute{Required: true,
				MarkdownDescription: "The group's CN. Changing it renames the group in place."},
			"sam_account_name": schema.StringAttribute{Required: true,
				MarkdownDescription: "The pre-Windows 2000 name. Changing it updates the group in place."},
			"container": schema.StringAttribute{Required: true,
				PlanModifiers:       []planmodifier.String{keepEquivalentDN{}},
				MarkdownDescription: "Distinguished name of the parent. Changing it moves the group in place."},
			"scope": schema.StringAttribute{
				Optional: true, Computed: true,
				Default:    stringdefault.StaticString("global"),
				Validators: []validator.String{stringvalidator.OneOf("global", "domainlocal", "universal")},
				MarkdownDescription: "`global`, `domainlocal` or `universal`. Active Directory " +
					"refuses some conversions; where it does, its own error is surfaced and the " +
					"provider never replaces the group to force the change.",
			},
			"category": schema.StringAttribute{
				Optional: true, Computed: true,
				Default:             stringdefault.StaticString("security"),
				Validators:          []validator.String{stringvalidator.OneOf("security", "distribution")},
				MarkdownDescription: "`security` or `distribution`.",
			},
			// The empty default is load-bearing: without it, Optional+Computed
			// retains the prior state value when the line is removed from
			// configuration, so removal would silently not clear the attribute.
			"description": schema.StringAttribute{Optional: true, Computed: true,
				Default:             stringdefault.StaticString(""),
				MarkdownDescription: "Free-text description. `\"\"` or removal clears the attribute."},
			"managed_by": schema.StringAttribute{Optional: true, Computed: true,
				Default:             stringdefault.StaticString(""),
				MarkdownDescription: "Distinguished name of the managing principal. `\"\"` or removal clears it."},
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{Create: true, Read: true, Update: true, Delete: true}),
		},
	}
}

func (r *groupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *groupResource) specFrom(m groupModel) adpwsh.GroupSpec {
	return adpwsh.GroupSpec{
		Name:           m.Name.ValueString(),
		SamAccountName: m.SamAccountName.ValueString(),
		Container:      m.Container.ValueString(),
		Scope:          adpwsh.GroupScope(m.Scope.ValueString()),
		Category:       adpwsh.GroupCategory(m.Category.ValueString()),
		Description:    adpwsh.String(m.Description.ValueString()),
		ManagedBy:      adpwsh.String(m.ManagedBy.ValueString()),
	}
}

func (r *groupResource) apply(g *adpwsh.Group, m *groupModel) {
	m.ID = types.StringValue(g.GUID)
	m.DN = types.StringValue(g.DN)
	m.SID = types.StringValue(g.SID)
	m.Name = types.StringValue(g.Name)
	m.SamAccountName = types.StringValue(g.SamAccountName)
	m.Container = types.StringValue(g.Container)
	m.Scope = types.StringValue(string(g.Scope))
	m.Category = types.StringValue(string(g.Category))
	m.Description = types.StringValue(g.Description)
	m.ManagedBy = types.StringValue(g.ManagedBy)
}

func (r *groupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan groupModel
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

	g, err := r.client.Group.Create(ctx, r.specFrom(plan))
	if g != nil {
		r.apply(g, &plan)
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
	if err != nil {
		resp.Diagnostics.Append(errorDiagnostics("Group.Create", groupResourceType, err)...)
	}
}

func (r *groupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state groupModel
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

	g, err := r.client.Group.Get(ctx, adpwsh.ByGUID(state.ID.ValueString()))
	if isNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.Append(errorDiagnostics("Group.Get", groupResourceType, err)...)
		return
	}
	r.apply(g, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *groupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state groupModel
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

	g, err := r.client.Group.Update(ctx, adpwsh.ByGUID(state.ID.ValueString()), r.specFrom(plan))
	if g != nil {
		r.apply(g, &plan)
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
	if err != nil {
		// A refused scope conversion is attributable to a specific line.
		if plan.Scope != state.Scope {
			resp.Diagnostics.Append(attributeErrorDiagnostics("Group.Update", groupResourceType, err,
				path.Root("scope"))...)
			return
		}
		resp.Diagnostics.Append(errorDiagnostics("Group.Update", groupResourceType, err)...)
	}
}

func (r *groupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state groupModel
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

	if err := r.client.Group.Delete(ctx, adpwsh.ByGUID(state.ID.ValueString())); err != nil && !isNotFound(err) {
		resp.Diagnostics.Append(errorDiagnostics("Group.Delete", groupResourceType, err)...)
	}
}

func (r *groupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	g, err := r.client.Group.Get(ctx, identityFromImportID(req.ID))
	if err != nil {
		resp.Diagnostics.Append(errorDiagnostics("Group.Get", groupResourceType, err)...)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), g.GUID)...)
}

var (
	_ resource.Resource                = (*groupResource)(nil)
	_ resource.ResourceWithConfigure   = (*groupResource)(nil)
	_ resource.ResourceWithImportState = (*groupResource)(nil)
)
