package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	adpwsh "github.com/nemethhh/go-adpwsh"
)

const ouResourceType = "activedirectory_ou"

type ouResource struct {
	client *adpwsh.Client
}

func newOUResource() resource.Resource { return &ouResource{} }

type ouModel struct {
	ID          types.String   `tfsdk:"id"`
	DN          types.String   `tfsdk:"dn"`
	Name        types.String   `tfsdk:"name"`
	Container   types.String   `tfsdk:"container"`
	Description types.String   `tfsdk:"description"`
	Protected   types.Bool     `tfsdk:"protected_from_accidental_deletion"`
	Timeouts    timeouts.Value `tfsdk:"timeouts"`
}

func (r *ouResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = ouResourceType
}

func (r *ouResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An organizational unit. This is what makes `container` a real " +
			"dependency edge rather than a hardcoded string.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The objectGUID. Stable across rename and move.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"dn": schema.StringAttribute{
				Computed:            true,
				PlanModifiers:       []planmodifier.String{dnFollowsNameAndContainer{}},
				MarkdownDescription: "The distinguished name. Reference it from another resource's `container`.",
			},
			"name": schema.StringAttribute{
				Required: true,
				// No RequiresReplace: a rename is Rename-ADObject. Replacing
				// would destroy the object and everything referencing it.
				Validators: cnLengthValidators(),
				MarkdownDescription: "The OU's name (its RDN). Changing it renames the OU in place." +
					" At most 64 characters.",
			},
			"container": schema.StringAttribute{
				Required: true,
				// A DN is matched case-insensitively by the directory, so a
				// re-spelling of the same container is not a move.
				PlanModifiers:       []planmodifier.String{keepEquivalentDN{}},
				MarkdownDescription: "Distinguished name of the parent. Changing it moves the OU in place.",
			},
			"description": schema.StringAttribute{
				Optional: true,
				Computed: true,
				// The empty default is what makes correctness rule 2 work.
				// Optional+Computed alone would retain the prior state value
				// when the attribute is removed from configuration, so
				// deleting the line would silently not clear the attribute.
				// With the default, removal plans "" and the library turns
				// that into -Clear.
				Default:             stringdefault.StaticString(""),
				MarkdownDescription: "Free-text description. Setting it to `\"\"` or removing it clears the attribute.",
			},
			"protected_from_accidental_deletion": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(true),
				MarkdownDescription: "Mirrors Active Directory's own default of `true`. Destroying " +
					"the resource lifts the protection and then deletes, in one operation.",
			},
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{Create: true, Read: true, Update: true, Delete: true}),
		},
	}
}

func (r *ouResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *ouResource) specFrom(m ouModel) adpwsh.OUSpec {
	return adpwsh.OUSpec{
		Name:      m.Name.ValueString(),
		Container: m.Container.ValueString(),
		// A null the provider manages means clear, which is the same as "".
		Description: adpwsh.String(m.Description.ValueString()),
		Protected:   adpwsh.Bool(m.Protected.ValueBool()),
	}
}

func (r *ouResource) apply(ou *adpwsh.OU, m *ouModel) {
	m.ID = types.StringValue(ou.GUID)
	m.DN = types.StringValue(ou.DN)
	m.Name = types.StringValue(ou.Name)
	m.Container = types.StringValue(ou.Container)
	m.Description = types.StringValue(ou.Description)
	m.Protected = types.BoolValue(ou.Protected)
}

func (r *ouResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ouModel
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

	ou, err := r.client.OU.Create(ctx, r.specFrom(plan))
	// A non-nil model with a non-nil error is the replication-timeout
	// contract: the object exists, so state must be saved before the error is
	// surfaced. Erroring without saving orphans the object.
	if ou != nil {
		r.apply(ou, &plan)
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
	if err != nil {
		resp.Diagnostics.Append(errorDiagnostics("OU.Create", ouResourceType, err)...)
	}
}

func (r *ouResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ouModel
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

	ou, err := r.client.OU.Get(ctx, adpwsh.ByGUID(state.ID.ValueString()))
	if isNotFound(err) {
		// Gone from the directory is drift, not a failure.
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.Append(errorDiagnostics("OU.Get", ouResourceType, err)...)
		return
	}
	r.apply(ou, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ouResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state ouModel
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

	ou, err := r.client.OU.Update(ctx, adpwsh.ByGUID(state.ID.ValueString()), r.specFrom(plan))
	if ou != nil {
		r.apply(ou, &plan)
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
	if err != nil {
		resp.Diagnostics.Append(errorDiagnostics("OU.Update", ouResourceType, err)...)
	}
}

func (r *ouResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state ouModel
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

	// Unprotect is always true here: the provider's own default sets the
	// protection, so destroy must lift it or it fails on a flag it chose.
	err := r.client.OU.Delete(ctx, adpwsh.ByGUID(state.ID.ValueString()), adpwsh.DeleteOptions{Unprotect: true})
	if err != nil && !isNotFound(err) {
		resp.Diagnostics.Append(errorDiagnostics("OU.Delete", ouResourceType, err)...)
	}
}

func (r *ouResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	ou, err := r.client.OU.Get(ctx, identityFromImportID(req.ID))
	if err != nil {
		resp.Diagnostics.Append(errorDiagnostics("OU.Get", ouResourceType, err)...)
		return
	}
	// State only ever holds one identity space, so the import ID is resolved
	// to the GUID here rather than carried in whatever form it arrived.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), ou.GUID)...)
}

var (
	_ resource.Resource                = (*ouResource)(nil)
	_ resource.ResourceWithConfigure   = (*ouResource)(nil)
	_ resource.ResourceWithImportState = (*ouResource)(nil)
)
