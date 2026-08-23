package provider

import (
	"context"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	adpwsh "github.com/nemethhh/go-adpwsh"
)

const userResourceType = "activedirectory_user"

type userResource struct {
	client *adpwsh.Client
}

func newUserResource() resource.Resource { return &userResource{} }

type userModel struct {
	ID                    types.String   `tfsdk:"id"`
	DN                    types.String   `tfsdk:"dn"`
	SID                   types.String   `tfsdk:"sid"`
	SamAccountName        types.String   `tfsdk:"sam_account_name"`
	Container             types.String   `tfsdk:"container"`
	Name                  types.String   `tfsdk:"name"`
	UserPrincipalName     types.String   `tfsdk:"user_principal_name"`
	DisplayName           types.String   `tfsdk:"display_name"`
	GivenName             types.String   `tfsdk:"given_name"`
	Surname               types.String   `tfsdk:"surname"`
	Description           types.String   `tfsdk:"description"`
	Enabled               types.Bool     `tfsdk:"enabled"`
	Password              types.String   `tfsdk:"password"`
	PasswordVersion       types.Int64    `tfsdk:"password_version"`
	ChangePasswordAtLogon types.Bool     `tfsdk:"change_password_at_logon"`
	CanChangePassword     types.Bool     `tfsdk:"can_change_password"`
	PasswordExpires       types.Bool     `tfsdk:"password_expires"`
	AccountExpirationDate types.String   `tfsdk:"account_expiration_date"`
	Timeouts              timeouts.Value `tfsdk:"timeouts"`
}

func (r *userResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = userResourceType
}

func (r *userResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A user account.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				MarkdownDescription: "The objectGUID.",
			},
			"dn": schema.StringAttribute{Computed: true,
				PlanModifiers:       []planmodifier.String{dnFollowsNameAndContainer{}},
				MarkdownDescription: "The distinguished name."},
			"sid": schema.StringAttribute{Computed: true,
				// A SID is minted once and outlives every rename and move.
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				MarkdownDescription: "The security identifier."},
			"sam_account_name": schema.StringAttribute{Required: true,
				Validators: samAccountNameValidators(),
				MarkdownDescription: "The logon name. Changing it updates the account in place." +
					" At most 20 characters; it may not contain \" [ ] : ; | = + * ? < > / \\ , or end with a period or space."},
			"container": schema.StringAttribute{Required: true,
				PlanModifiers:       []planmodifier.String{keepEquivalentDN{}},
				MarkdownDescription: "Distinguished name of the parent. Changing it moves the account in place."},
			"name": schema.StringAttribute{Optional: true, Computed: true,
				Validators: cnLengthValidators(),
				MarkdownDescription: "The CN. Defaults to `sam_account_name`. Changing it renames the account in place." +
					" At most 64 characters."},
			// Every optional string carries an empty default. Optional+Computed
			// alone retains the prior state value when the line is removed
			// from configuration, which would make removal a silent no-op
			// instead of a clear. With the default, removal plans "" and the
			// library emits -Clear against the attribute's LDAP name — sn for
			// surname, which is the row that catches a wrong mapping.
			"user_principal_name": schema.StringAttribute{Optional: true, Computed: true,
				Default: stringdefault.StaticString(""),
				MarkdownDescription: "The UPN the account signs in with, such as " +
					"`jdoe@corp.local`. `\"\"` or removal clears the attribute."},
			"display_name": schema.StringAttribute{Optional: true, Computed: true,
				Default: stringdefault.StaticString(""),
				MarkdownDescription: "The name shown in address lists. Often written by a sync " +
					"engine as well, in which case put it in `ignore_changes`. `\"\"` or removal " +
					"clears the attribute."},
			"given_name": schema.StringAttribute{Optional: true, Computed: true,
				Default:             stringdefault.StaticString(""),
				MarkdownDescription: "The first name. `\"\"` or removal clears the attribute."},
			"surname": schema.StringAttribute{Optional: true, Computed: true,
				Default: stringdefault.StaticString(""),
				MarkdownDescription: "The last name, stored in Active Directory as `sn`. " +
					"`\"\"` or removal clears the attribute."},
			"description": schema.StringAttribute{Optional: true, Computed: true,
				Default:             stringdefault.StaticString(""),
				MarkdownDescription: "Free-text description. `\"\"` or removal clears the attribute."},
			"enabled": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(true),
				MarkdownDescription: "Whether the account is enabled. Active Directory refuses to " +
					"enable an account without a password satisfying domain policy, so supply " +
					"`password` alongside `enabled = true`, or set `enabled = false` and let " +
					"another system establish the password. The provider does not paper over " +
					"this by silently creating a disabled account.",
			},
			"password": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				WriteOnly: true,
				MarkdownDescription: "The account's password. **Write-only**: it is sent on create " +
					"and on rotation and is never written to state or to a plan file, so " +
					"`terraform import` cannot reset an existing user's password. Requires " +
					"Terraform 1.11 or later. Rotate by changing `password_version`.",
			},
			"password_version": schema.Int64Attribute{
				Optional: true,
				Validators: []validator.Int64{
					int64validator.AlsoRequires(path.MatchRoot("password")),
				},
				MarkdownDescription: "Increment to rotate the password. A write-only value cannot " +
					"itself be diffed, so this ordinary integer is what makes a rotation visible " +
					"in a plan. If `password` is set and this never changes, the password is " +
					"applied at create only.",
			},
			"change_password_at_logon": schema.BoolAttribute{Optional: true, Computed: true,
				MarkdownDescription: "Require a password change at the next logon."},
			"can_change_password": schema.BoolAttribute{Optional: true, Computed: true,
				MarkdownDescription: "Whether the user may change their own password. Stated " +
					"positively; Active Directory's own parameter is the negative form."},
			"password_expires": schema.BoolAttribute{Optional: true, Computed: true,
				MarkdownDescription: "Whether the password expires. Stated positively."},
			"account_expiration_date": schema.StringAttribute{Optional: true, Computed: true,
				// Empty, not null, is the canonical "never expires". Terraform
				// cannot distinguish an explicit null from omission on an
				// Optional+Computed attribute, so a null representation would
				// leave no way to clear an expiry once one was set.
				Default: stringdefault.StaticString(""),
				MarkdownDescription: "An RFC 3339 timestamp, or `\"\"` for an account that never " +
					"expires. Removing the line clears any expiry. The underlying FILETIME " +
					"integer is never part of this surface."},
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{Create: true, Read: true, Update: true, Delete: true}),
		},
	}
}

func (r *userResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

// specFrom maps the plan onto the library's spec. Every optional string is
// passed as a pointer to its value — including the empty string, which the
// library turns into -Clear, because Active Directory has no empty attribute
// value.
func (r *userResource) specFrom(m userModel, diags *diag.Diagnostics) adpwsh.UserSpec {
	spec := adpwsh.UserSpec{
		SamAccountName:    m.SamAccountName.ValueString(),
		Container:         m.Container.ValueString(),
		Name:              adpwsh.String(m.Name.ValueString()),
		UserPrincipalName: adpwsh.String(m.UserPrincipalName.ValueString()),
		DisplayName:       adpwsh.String(m.DisplayName.ValueString()),
		GivenName:         adpwsh.String(m.GivenName.ValueString()),
		Surname:           adpwsh.String(m.Surname.ValueString()),
		Description:       adpwsh.String(m.Description.ValueString()),
		Enabled:           optBool(m.Enabled),
		// These three carry no default, so an unset one must stay nil — "leave
		// it alone" — rather than becoming false. Sending false for an unset
		// can_change_password would deny password changes nobody asked to deny.
		ChangePasswordAtLogon: optBool(m.ChangePasswordAtLogon),
		CanChangePassword:     optBool(m.CanChangePassword),
		PasswordExpires:       optBool(m.PasswordExpires),
	}
	if m.Name.IsNull() || m.Name.IsUnknown() || m.Name.ValueString() == "" {
		// The CN defaults to the sAMAccountName; the library applies the same
		// default, so send nothing rather than an empty rename target.
		spec.Name = nil
	}
	switch {
	case m.AccountExpirationDate.IsUnknown():
		// Leave it alone until the value is known.
	case m.AccountExpirationDate.IsNull() || m.AccountExpirationDate.ValueString() == "":
		// Clearing accountExpires is how Active Directory spells "never".
		spec.AccountExpiration = adpwsh.ClearTime()
	default:
		t, err := time.Parse(time.RFC3339, m.AccountExpirationDate.ValueString())
		if err != nil {
			diags.AddAttributeError(path.Root("account_expiration_date"), "Invalid timestamp",
				"Expected an RFC 3339 timestamp such as \"2027-01-02T03:04:05Z\": "+err.Error())
			break
		}
		spec.AccountExpiration = adpwsh.SetTime(t)
	}
	return spec
}

func (r *userResource) apply(u *adpwsh.User, m *userModel) {
	m.ID = types.StringValue(u.GUID)
	m.DN = types.StringValue(u.DN)
	m.SID = types.StringValue(u.SID)
	m.SamAccountName = types.StringValue(u.SamAccountName)
	m.Container = types.StringValue(u.Container)
	m.Name = types.StringValue(u.Name)
	m.UserPrincipalName = types.StringValue(u.UserPrincipalName)
	m.DisplayName = types.StringValue(u.DisplayName)
	m.GivenName = types.StringValue(u.GivenName)
	m.Surname = types.StringValue(u.Surname)
	m.Description = types.StringValue(u.Description)
	m.Enabled = types.BoolValue(u.Enabled)
	m.ChangePasswordAtLogon = types.BoolValue(u.ChangePasswordAtLogon)
	m.CanChangePassword = types.BoolValue(u.CanChangePassword)
	m.PasswordExpires = types.BoolValue(u.PasswordExpires)
	if u.AccountExpiration == nil {
		m.AccountExpirationDate = types.StringValue("")
		return
	}
	m.AccountExpirationDate = types.StringValue(u.AccountExpiration.UTC().Format(time.RFC3339))
}

// optBool renders a Terraform boolean as the library's tri-state: nil means
// leave the attribute alone, which is not the same as false.
func optBool(v types.Bool) *bool {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	return adpwsh.Bool(v.ValueBool())
}

// writeOnlyPassword reads the password from the configuration. Write-only
// values never appear in the plan or the state, so the config is the only
// place to get one.
func writeOnlyPassword(ctx context.Context, cfg configGetter, diags *diag.Diagnostics) (adpwsh.Secret, bool) {
	var pw types.String
	diags.Append(cfg.GetAttribute(ctx, path.Root("password"), &pw)...)
	if diags.HasError() || pw.IsNull() || pw.IsUnknown() || pw.ValueString() == "" {
		return adpwsh.Secret{}, false
	}
	return adpwsh.NewSecret(pw.ValueString()), true
}

// configGetter is the shape Create/Update requests share.
type configGetter interface {
	GetAttribute(ctx context.Context, p path.Path, target any) diag.Diagnostics
}

func (r *userResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan userModel
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

	spec := r.specFrom(plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if pw, ok := writeOnlyPassword(ctx, req.Config, &resp.Diagnostics); ok {
		spec.Password = &pw
	}
	if resp.Diagnostics.HasError() {
		return
	}

	u, err := r.client.User.Create(ctx, spec)
	if u != nil {
		r.apply(u, &plan)
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
	if err != nil {
		resp.Diagnostics.Append(errorDiagnostics("User.Create", userResourceType, err)...)
	}
}

func (r *userResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state userModel
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

	u, err := r.client.User.Get(ctx, adpwsh.ByGUID(state.ID.ValueString()))
	if isNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.Append(errorDiagnostics("User.Get", userResourceType, err)...)
		return
	}
	r.apply(u, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *userResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state userModel
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

	spec := r.specFrom(plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	id := adpwsh.ByGUID(state.ID.ValueString())

	u, err := r.client.User.Update(ctx, id, spec)
	if u != nil {
		r.apply(u, &plan)
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
	if err != nil {
		resp.Diagnostics.Append(errorDiagnostics("User.Update", userResourceType, err)...)
		return
	}

	// Rotation is driven by password_version, because a write-only value
	// cannot be diffed. -AccountPassword does not exist on Set-ADUser, so this
	// is a separate Set-ADAccountPassword -Reset rather than part of the write
	// above.
	if plan.PasswordVersion.Equal(state.PasswordVersion) {
		return
	}
	pw, ok := writeOnlyPassword(ctx, req.Config, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if !ok {
		resp.Diagnostics.AddAttributeWarning(path.Root("password_version"),
			"Password not rotated",
			"password_version changed but no password was supplied in this configuration, so "+
				"the account's password was left as it is.")
		return
	}
	if err := r.client.User.SetPassword(ctx, id, pw); err != nil {
		resp.Diagnostics.Append(attributeErrorDiagnostics("User.SetPassword", userResourceType, err,
			path.Root("password"))...)
	}
}

func (r *userResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state userModel
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

	if err := r.client.User.Delete(ctx, adpwsh.ByGUID(state.ID.ValueString())); err != nil && !isNotFound(err) {
		resp.Diagnostics.Append(errorDiagnostics("User.Delete", userResourceType, err)...)
	}
}

func (r *userResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	u, err := r.client.User.Get(ctx, identityFromImportID(req.ID))
	if err != nil {
		resp.Diagnostics.Append(errorDiagnostics("User.Get", userResourceType, err)...)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), u.GUID)...)
}

var (
	_ resource.Resource                = (*userResource)(nil)
	_ resource.ResourceWithConfigure   = (*userResource)(nil)
	_ resource.ResourceWithImportState = (*userResource)(nil)
)
