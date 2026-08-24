package provider

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	adpwsh "github.com/nemethhh/go-adpwsh"
)

const gmsaResourceType = "activedirectory_gmsa"

type gmsaResource struct {
	client *adpwsh.Client
}

func newGMSAResource() resource.Resource { return &gmsaResource{} }

type gmsaModel struct {
	ID                    types.String   `tfsdk:"id"`
	DN                    types.String   `tfsdk:"dn"`
	SID                   types.String   `tfsdk:"sid"`
	Name                  types.String   `tfsdk:"name"`
	SamAccountName        types.String   `tfsdk:"sam_account_name"`
	Container             types.String   `tfsdk:"container"`
	DNSHostName           types.String   `tfsdk:"dns_hostname"`
	Description           types.String   `tfsdk:"description"`
	DisplayName           types.String   `tfsdk:"display_name"`
	Enabled               types.Bool     `tfsdk:"enabled"`
	TrustedForDelegation  types.Bool     `tfsdk:"trusted_for_delegation"`
	Principals            types.Set      `tfsdk:"principals_allowed_to_retrieve_managed_password"`
	SPNs                  types.Set      `tfsdk:"service_principal_names"`
	KerberosEncryption    types.Set      `tfsdk:"kerberos_encryption_type"`
	AccountExpirationDate types.String   `tfsdk:"account_expiration_date"`
	Interval              types.Int64    `tfsdk:"managed_password_interval_in_days"`
	Timeouts              timeouts.Value `tfsdk:"timeouts"`
}

func (r *gmsaResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = gmsaResourceType
}

func (r *gmsaResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A group Managed Service Account (gMSA). Active Directory generates and " +
			"rotates its password automatically, so unlike `activedirectory_user` this resource has " +
			"no password attribute at all.",
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
			"name": schema.StringAttribute{Required: true,
				Validators: cnLengthValidators(),
				MarkdownDescription: "The CN. Changing it renames the account in place. At most 64 " +
					"characters."},
			"sam_account_name": schema.StringAttribute{Optional: true, Computed: true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Validators:    gmsaSamAccountNameValidators(),
				MarkdownDescription: "The logon name. Defaults to `name` (Active Directory stores it " +
					"with a trailing `$`, so the effective sAMAccountName is one character longer " +
					"than this value). Changing it updates the account in place. At most 15 " +
					"characters — a gMSA is a computer-like account, bound by the NetBIOS " +
					"computer-name limit rather than the user's 20-character down-level logon name " +
					"limit — and it may not contain \" [ ] : ; | = + * ? < > / \\ , or end with a " +
					"period or space.",
			},
			"container": schema.StringAttribute{Required: true,
				PlanModifiers:       []planmodifier.String{keepEquivalentDN{}},
				MarkdownDescription: "Distinguished name of the parent. Changing it moves the account in place."},
			"dns_hostname": schema.StringAttribute{Required: true,
				MarkdownDescription: "The FQDN Active Directory associates with this account's " +
					"default service principal names, e.g. `gmsa01.corp.local`. Required."},
			"description": schema.StringAttribute{Optional: true, Computed: true,
				Default:             stringdefault.StaticString(""),
				MarkdownDescription: "Free-text description. `\"\"` or removal clears the attribute."},
			"display_name": schema.StringAttribute{Optional: true, Computed: true,
				Default: stringdefault.StaticString(""),
				MarkdownDescription: "The name shown in address lists. `\"\"` or removal clears the " +
					"attribute."},
			"enabled": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(true),
				MarkdownDescription: "Whether the account is enabled.",
			},
			"trusted_for_delegation": schema.BoolAttribute{Optional: true, Computed: true,
				MarkdownDescription: "Whether the account is trusted for Kerberos delegation."},
			"principals_allowed_to_retrieve_managed_password": schema.SetAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Validators: []validator.Set{
					setvalidator.ValueStringsAre(stringvalidator.RegexMatches(guidPattern, "must be an objectGUID")),
				},
				MarkdownDescription: "The objectGUIDs of the computers or groups allowed to retrieve " +
					"this account's managed password. Full-replace: omitting the attribute leaves " +
					"whatever Active Directory already holds untouched, while setting it — including " +
					"to `[]` — replaces the entire set.",
			},
			"service_principal_names": schema.SetAttribute{
				Optional:    true,
				ElementType: types.StringType,
				MarkdownDescription: "The account's service principal names. Full-replace, the same " +
					"as `principals_allowed_to_retrieve_managed_password`: omitting the attribute " +
					"leaves Active Directory's existing SPNs untouched, and setting it — including to " +
					"`[]` — replaces the entire set.",
			},
			"kerberos_encryption_type": schema.SetAttribute{
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
				Validators: []validator.Set{
					setvalidator.ValueStringsAre(stringvalidator.OneOf(kerberosEncryptionTypeValues()...)),
				},
				MarkdownDescription: "The Kerberos encryption types the account supports: any of " +
					"`None`, `DES`, `RC4`, `AES128`, `AES256`. Defaults to whatever Active Directory " +
					"assigns a newly created gMSA.",
			},
			"account_expiration_date": schema.StringAttribute{Optional: true, Computed: true,
				// Empty, not null, is the canonical "never expires" — see
				// resource_user.go's identical attribute for why.
				Default: stringdefault.StaticString(""),
				MarkdownDescription: "An RFC 3339 timestamp, or `\"\"` for an account that never " +
					"expires. Removing the line clears any expiry. The underlying FILETIME " +
					"integer is never part of this surface."},
			"managed_password_interval_in_days": schema.Int64Attribute{
				Optional: true, Computed: true, Default: int64default.StaticInt64(30),
				PlanModifiers: []planmodifier.Int64{
					immutableAfterCreate{attr: "managed_password_interval_in_days"},
				},
				MarkdownDescription: "How often Active Directory rotates the managed password, in " +
					"days. Set only at creation: Set-ADServiceAccount exposes no update for it, so " +
					"changing this attribute in place is refused rather than silently discarded or " +
					"forcing a replace (which would mint a new SID).",
			},
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{Create: true, Read: true, Update: true, Delete: true}),
		},
	}
}

func (r *gmsaResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

// gmsaEffectiveSamValidator catches, at plan time, a name too long to derive
// a valid sam_account_name from. The attribute-level validator on
// sam_account_name only fires when it is actually set in configuration; when
// it is left to default from name (the common case), an over-length name
// would otherwise surface only as an opaque KindConstraint error from the
// library on apply.
type gmsaEffectiveSamValidator struct{}

func (gmsaEffectiveSamValidator) Description(_ context.Context) string {
	return "sam_account_name, when it defaults from name, must not exceed the 15-character gMSA limit."
}

func (v gmsaEffectiveSamValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (gmsaEffectiveSamValidator) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config gmsaModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// An explicitly set sam_account_name is already bounded by
	// gmsaSamAccountNameValidators(); this check only covers the
	// derive-from-name case. An unknown value on either side cannot be
	// checked until apply time.
	if config.SamAccountName.IsUnknown() || config.Name.IsUnknown() {
		return
	}
	if !config.SamAccountName.IsNull() && config.SamAccountName.ValueString() != "" {
		return
	}

	name := config.Name.ValueString()
	if len(name) <= gmsaSamAccountNameMaxLen {
		return
	}
	resp.Diagnostics.AddAttributeError(path.Root("name"), "name too long to derive sam_account_name",
		fmt.Sprintf("sam_account_name is not set, so it defaults to name plus Active Directory's "+
			"trailing \"$\" (%q), which is %d characters; a gMSA sAMAccountName must be at most %d. "+
			"Shorten name, or set sam_account_name explicitly to at most %d characters.",
			name+"$", len(name)+1, gmsaSamAccountNameMaxLen, gmsaSamAccountNameMaxLen))
}

func (r *gmsaResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{gmsaEffectiveSamValidator{}}
}

// specFrom maps the plan onto the library's spec. Every optional string is
// passed as a pointer to its value — including the empty string, which the
// library turns into -Clear, because Active Directory has no empty attribute
// value. forCreate gates ManagedPasswordIntervalInDays, which the library
// only reads on Create: Set-ADServiceAccount has no such parameter, and the
// managed_password_interval_in_days plan modifier already refuses any
// attempt to change it after creation, so Update never needs to send it.
func (r *gmsaResource) specFrom(ctx context.Context, m gmsaModel, forCreate bool, diags *diag.Diagnostics) adpwsh.GMSASpec {
	sam := m.SamAccountName.ValueString()
	if sam == "" {
		// sam_account_name is Optional+Computed with no schema-level default,
		// so an omitted value arrives here as "": derive it from name, the
		// same way Active Directory's own default behaves.
		sam = m.Name.ValueString()
	}
	spec := adpwsh.GMSASpec{
		Name:                 m.Name.ValueString(),
		SamAccountName:       sam,
		Container:            m.Container.ValueString(),
		DNSHostName:          adpwsh.String(m.DNSHostName.ValueString()),
		Description:          adpwsh.String(m.Description.ValueString()),
		DisplayName:          adpwsh.String(m.DisplayName.ValueString()),
		Enabled:              optBool(m.Enabled),
		TrustedForDelegation: optBool(m.TrustedForDelegation),
	}

	if !m.Principals.IsNull() && !m.Principals.IsUnknown() {
		var guids []string
		diags.Append(m.Principals.ElementsAs(ctx, &guids, false)...)
		ids := make([]adpwsh.Identity, len(guids))
		for i, g := range guids {
			ids[i] = adpwsh.ByGUID(g)
		}
		spec.PrincipalsAllowed = ids
	}
	if !m.SPNs.IsNull() && !m.SPNs.IsUnknown() {
		var spns []string
		diags.Append(m.SPNs.ElementsAs(ctx, &spns, false)...)
		spec.ServicePrincipalNames = &spns
	}
	if !m.KerberosEncryption.IsNull() && !m.KerberosEncryption.IsUnknown() {
		var kerb []string
		diags.Append(m.KerberosEncryption.ElementsAs(ctx, &kerb, false)...)
		spec.KerberosEncryptionType = &kerb
	}
	if forCreate {
		spec.ManagedPasswordIntervalInDays = adpwsh.Int(int(m.Interval.ValueInt64()))
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

// apply copies the library's read model onto the Terraform model.
//
// principals_allowed_to_retrieve_managed_password and
// service_principal_names are Optional but not Computed, so Terraform
// requires the state after apply to equal exactly what was planned; the plan
// for an omitted value is always null, never "whatever Active Directory
// happens to hold" (which, left over from an earlier apply or an out-of-band
// change, need not be empty). Overwriting a null plan value with Active
// Directory's actual value would fail that consistency check, so both are
// left untouched here when the model does not already hold a value — mirror
// the library's own "nil leaves it alone" contract for these two attributes.
// kerberos_encryption_type has no such restriction: it is Computed, so the
// provider is always free to report Active Directory's actual value.
func (r *gmsaResource) apply(ctx context.Context, g *adpwsh.GMSA, m *gmsaModel, diags *diag.Diagnostics) {
	m.ID = types.StringValue(g.GUID)
	m.DN = types.StringValue(g.DN)
	m.SID = types.StringValue(g.SID)
	m.Name = types.StringValue(g.Name)
	// Active Directory appends "$" to a gMSA's sAMAccountName on read; the
	// sam_account_name attribute holds the un-suffixed base the user
	// configured (or that was derived from name), matching what specFrom
	// sends and what its own MarkdownDescription documents. Storing the
	// suffixed form verbatim would both fail Terraform's plan-consistency
	// check whenever sam_account_name is set explicitly (planned "svc01" vs
	// applied "svc01$") and make every Update a spurious rewrite, since the
	// library's own update path compares against the un-suffixed value too.
	m.SamAccountName = types.StringValue(strings.TrimSuffix(g.SamAccountName, "$"))
	m.Container = types.StringValue(g.Container)
	m.DNSHostName = types.StringValue(g.DNSHostName)
	m.Description = types.StringValue(g.Description)
	m.DisplayName = types.StringValue(g.DisplayName)
	m.Enabled = types.BoolValue(g.Enabled)
	m.TrustedForDelegation = types.BoolValue(g.TrustedForDelegation)

	if !m.Principals.IsNull() {
		v, d := types.SetValueFrom(ctx, types.StringType, g.PrincipalsAllowed)
		diags.Append(d...)
		m.Principals = v
	}
	if !m.SPNs.IsNull() {
		v, d := types.SetValueFrom(ctx, types.StringType, g.ServicePrincipalNames)
		diags.Append(d...)
		m.SPNs = v
	}
	kerb, d := types.SetValueFrom(ctx, types.StringType, g.KerberosEncryptionType)
	diags.Append(d...)
	m.KerberosEncryption = kerb

	m.Interval = types.Int64Value(int64(g.ManagedPasswordIntervalInDays))

	if g.AccountExpiration == nil {
		m.AccountExpirationDate = types.StringValue("")
		return
	}
	m.AccountExpirationDate = types.StringValue(g.AccountExpiration.UTC().Format(time.RFC3339))
}

func (r *gmsaResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan gmsaModel
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

	spec := r.specFrom(ctx, plan, true, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	g, err := r.client.ServiceAccount.Create(ctx, spec)
	if g != nil {
		r.apply(ctx, g, &plan, &resp.Diagnostics)
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
	if err != nil {
		resp.Diagnostics.Append(errorDiagnostics("GMSA.Create", gmsaResourceType, err)...)
	}
}

func (r *gmsaResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state gmsaModel
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

	g, err := r.client.ServiceAccount.Get(ctx, adpwsh.ByGUID(state.ID.ValueString()))
	if isNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.Append(errorDiagnostics("GMSA.Get", gmsaResourceType, err)...)
		return
	}
	r.apply(ctx, g, &state, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *gmsaResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state gmsaModel
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

	spec := r.specFrom(ctx, plan, false, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	id := adpwsh.ByGUID(state.ID.ValueString())

	g, err := r.client.ServiceAccount.Update(ctx, id, spec)
	if g != nil {
		r.apply(ctx, g, &plan, &resp.Diagnostics)
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
	if err != nil {
		resp.Diagnostics.Append(errorDiagnostics("GMSA.Update", gmsaResourceType, err)...)
	}
}

func (r *gmsaResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state gmsaModel
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

	if err := r.client.ServiceAccount.Delete(ctx, adpwsh.ByGUID(state.ID.ValueString())); err != nil && !isNotFound(err) {
		resp.Diagnostics.Append(errorDiagnostics("GMSA.Delete", gmsaResourceType, err)...)
	}
}

func (r *gmsaResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	g, err := r.client.ServiceAccount.Get(ctx, identityFromImportID(req.ID))
	if err != nil {
		resp.Diagnostics.Append(errorDiagnostics("GMSA.Get", gmsaResourceType, err)...)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), g.GUID)...)
}

var (
	_ resource.Resource                     = (*gmsaResource)(nil)
	_ resource.ResourceWithConfigure        = (*gmsaResource)(nil)
	_ resource.ResourceWithConfigValidators = (*gmsaResource)(nil)
	_ resource.ResourceWithImportState      = (*gmsaResource)(nil)
)
