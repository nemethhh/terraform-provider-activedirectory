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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	adpwsh "github.com/nemethhh/go-adpwsh"
)

const computerResourceType = "activedirectory_computer"

// computerNameWarnLen is the 15-character NetBIOS computer-name limit past
// which the provider warns (never errors — Active Directory does not enforce
// it for computer accounts, so the provider must not out-strict the
// directory). It matches the ceiling warnLongSam applies on the attribute.
const computerNameWarnLen = 15

type computerResource struct {
	client *adpwsh.Client
}

func newComputerResource() resource.Resource { return &computerResource{} }

type computerModel struct {
	ID                                   types.String   `tfsdk:"id"`
	DN                                   types.String   `tfsdk:"dn"`
	SID                                  types.String   `tfsdk:"sid"`
	Name                                 types.String   `tfsdk:"name"`
	SamAccountName                       types.String   `tfsdk:"sam_account_name"`
	Container                            types.String   `tfsdk:"container"`
	DNSHostName                          types.String   `tfsdk:"dns_hostname"`
	Description                          types.String   `tfsdk:"description"`
	DisplayName                          types.String   `tfsdk:"display_name"`
	Location                             types.String   `tfsdk:"location"`
	ManagedBy                            types.String   `tfsdk:"managed_by"`
	Enabled                              types.Bool     `tfsdk:"enabled"`
	TrustedForDelegation                 types.Bool     `tfsdk:"trusted_for_delegation"`
	SPNs                                 types.Set      `tfsdk:"service_principal_names"`
	AllowedToDelegateTo                  types.Set      `tfsdk:"allowed_to_delegate_to"`
	PrincipalsAllowedToDelegateToAccount types.Set      `tfsdk:"principals_allowed_to_delegate_to_account"`
	KerberosEncryption                   types.Set      `tfsdk:"kerberos_encryption_type"`
	AccountExpirationDate                types.String   `tfsdk:"account_expiration_date"`
	OperatingSystem                      types.String   `tfsdk:"operating_system"`
	OperatingSystemVersion               types.String   `tfsdk:"operating_system_version"`
	OperatingSystemServicePack           types.String   `tfsdk:"operating_system_service_pack"`
	Timeouts                             timeouts.Value `tfsdk:"timeouts"`
}

func (r *computerResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = computerResourceType
}

func (r *computerResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An Active Directory computer account. Like `activedirectory_gmsa`, the " +
			"machine self-manages its password, so this resource has no password attribute; it manages " +
			"the account's identity, metadata, and delegation. Pre-stage an account for domain join, or " +
			"manage an existing machine account's metadata and delegation as code.",
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
				Validators:    computerSamAccountNameValidators(),
				MarkdownDescription: "The pre-Windows-2000 logon name (sAMAccountName), without the " +
					"trailing `$`. Defaults to `name`. Active Directory stores computers with a trailing " +
					"`$`; the provider strips it. Names longer than 15 characters are allowed but warned " +
					"about (NetBIOS limit).",
			},
			"container": schema.StringAttribute{Required: true,
				PlanModifiers:       []planmodifier.String{keepEquivalentDN{}},
				MarkdownDescription: "Distinguished name of the parent. Changing it moves the account in place."},
			"dns_hostname": schema.StringAttribute{Optional: true, Computed: true,
				Default:             stringdefault.StaticString(""),
				MarkdownDescription: "The DNS host name (dNSHostName). `\"\"` or removal clears it."},
			"description": schema.StringAttribute{Optional: true, Computed: true,
				Default:             stringdefault.StaticString(""),
				MarkdownDescription: "Free-text description. `\"\"` or removal clears the attribute."},
			"display_name": schema.StringAttribute{Optional: true, Computed: true,
				Default: stringdefault.StaticString(""),
				MarkdownDescription: "The name shown in address lists. `\"\"` or removal clears the " +
					"attribute."},
			"location": schema.StringAttribute{Optional: true, Computed: true,
				Default:             stringdefault.StaticString(""),
				MarkdownDescription: "Free-form location (location). `\"\"` or removal clears it."},
			"managed_by": schema.StringAttribute{Optional: true, Computed: true,
				Default:       stringdefault.StaticString(""),
				PlanModifiers: []planmodifier.String{keepEquivalentDN{}},
				MarkdownDescription: "Distinguished name of the managing user or group (managedBy). " +
					"`\"\"` or removal clears it. Active Directory reads it back as a DN."},
			"enabled": schema.BoolAttribute{
				Optional: true, Computed: true, Default: booldefault.StaticBool(true),
				MarkdownDescription: "Whether the account is enabled. Computer accounts default to enabled.",
			},
			"trusted_for_delegation": schema.BoolAttribute{Optional: true, Computed: true,
				MarkdownDescription: "**Unconstrained Kerberos delegation.** Security-sensitive: a host " +
					"trusted for unconstrained delegation, if compromised, can impersonate any user that " +
					"authenticates to it. Prefer `principals_allowed_to_delegate_to_account` (RBCD) or " +
					"`allowed_to_delegate_to` (constrained delegation). Setting this requires the account " +
					"running Terraform to hold the *Enable computer and user accounts to be trusted for " +
					"delegation* right (`SeEnableDelegationPrivilege`); without it Active Directory refuses " +
					"the write with \"A required privilege is not held by the client\"."},
			"service_principal_names": schema.SetAttribute{
				Optional:    true,
				ElementType: types.StringType,
				MarkdownDescription: "The account's service principal names (servicePrincipalName). " +
					"Full-replace: omitting the attribute leaves whatever Active Directory already holds " +
					"untouched, while setting it — including to `[]` — replaces the entire set.",
			},
			"allowed_to_delegate_to": schema.SetAttribute{
				Optional:    true,
				ElementType: types.StringType,
				MarkdownDescription: "Constrained-delegation target service principal names " +
					"(msDS-AllowedToDelegateTo). Full-replace, the same as `service_principal_names`: " +
					"omitting the attribute leaves Active Directory's existing value untouched, and " +
					"setting it — including to `[]` — replaces the entire set. Setting this requires " +
					"the account running Terraform to hold the *Enable computer and user accounts to " +
					"be trusted for delegation* right (`SeEnableDelegationPrivilege`); without it " +
					"Active Directory refuses the write with \"A required privilege is not held by " +
					"the client\".",
			},
			"principals_allowed_to_delegate_to_account": schema.SetAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Validators: []validator.Set{
					setvalidator.ValueStringsAre(stringvalidator.RegexMatches(guidPattern, "must be an objectGUID")),
				},
				MarkdownDescription: "Resource-based constrained delegation (RBCD): the objectGUIDs of " +
					"the principals allowed to delegate to this account " +
					"(PrincipalsAllowedToDelegateToAccount). Active Directory stores these as DNs; the " +
					"provider resolves them back to objectGUIDs on read. Full-replace, the same as " +
					"`service_principal_names`.",
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
					"assigns a newly created computer.",
			},
			"account_expiration_date": schema.StringAttribute{Optional: true, Computed: true,
				// Empty, not null, is the canonical "never expires" — see
				// resource_user.go's identical attribute for why.
				Default: stringdefault.StaticString(""),
				MarkdownDescription: "An RFC 3339 timestamp, or `\"\"` for an account that never " +
					"expires. Removing the line clears any expiry. The underlying FILETIME " +
					"integer is never part of this surface."},
			"operating_system": schema.StringAttribute{Computed: true,
				MarkdownDescription: "The operating system (operatingSystem). Read-only: the joined " +
					"machine owns it."},
			"operating_system_version": schema.StringAttribute{Computed: true,
				MarkdownDescription: "The operating-system version (operatingSystemVersion). Read-only."},
			"operating_system_service_pack": schema.StringAttribute{Computed: true,
				MarkdownDescription: "The operating-system service pack (operatingSystemServicePack). " +
					"Read-only."},
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{Create: true, Read: true, Update: true, Delete: true}),
		},
	}
}

func (r *computerResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

// computerEffectiveSamValidator warns, at plan time, when sam_account_name —
// left to default from name — exceeds the 15-character NetBIOS limit. The
// attribute-level warnLongSam validator on sam_account_name only fires when it
// is actually set in configuration; when it is left to default from name (the
// common case), an over-length name would otherwise go unremarked. Warning,
// not error: Active Directory accepts a computer name past 15 characters
// (lab-confirmed), so the provider must not out-strict the directory.
type computerEffectiveSamValidator struct{}

func (computerEffectiveSamValidator) Description(_ context.Context) string {
	return "warns when sam_account_name, defaulting from name, exceeds the 15-character NetBIOS computer-name limit."
}

func (v computerEffectiveSamValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (computerEffectiveSamValidator) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config computerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// An unknown value on either side cannot be checked until apply time.
	if config.SamAccountName.IsUnknown() || config.Name.IsUnknown() {
		return
	}
	// An explicitly set sam_account_name is already covered by the
	// attribute-level warnLongSam validator; this only covers the
	// derive-from-name case.
	if !config.SamAccountName.IsNull() && config.SamAccountName.ValueString() != "" {
		return
	}
	name := strings.TrimSuffix(config.Name.ValueString(), "$")
	if len(name) <= computerNameWarnLen {
		return
	}
	resp.Diagnostics.AddAttributeWarning(path.Root("name"),
		"Computer name longer than NetBIOS limit",
		fmt.Sprintf("sam_account_name is not set, so it defaults to name (%q), which is %d characters. "+
			"Active Directory allows this, but computers with names longer than %d characters can hit "+
			"NetBIOS/domain-join problems. Shorten name, or set sam_account_name explicitly.",
			name, len(name), computerNameWarnLen))
}

// computerDelegationConflictValidator warns when both trusted_for_delegation
// and allowed_to_delegate_to are set. Unconstrained delegation
// (trusted_for_delegation) and constrained delegation (allowed_to_delegate_to)
// are conceptually exclusive delegation models; configuring both is unusual.
// Warning, not error, to honour "don't out-strict AD" — the directory permits
// the combination.
type computerDelegationConflictValidator struct{}

func (computerDelegationConflictValidator) Description(_ context.Context) string {
	return "warns when trusted_for_delegation and allowed_to_delegate_to are both set (conceptually exclusive delegation modes)."
}

func (v computerDelegationConflictValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (computerDelegationConflictValidator) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config computerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if config.TrustedForDelegation.IsUnknown() || config.AllowedToDelegateTo.IsUnknown() {
		return
	}
	if !config.TrustedForDelegation.ValueBool() {
		return
	}
	if config.AllowedToDelegateTo.IsNull() || len(config.AllowedToDelegateTo.Elements()) == 0 {
		return
	}
	resp.Diagnostics.AddAttributeWarning(path.Root("trusted_for_delegation"),
		"Conflicting delegation modes",
		"trusted_for_delegation enables unconstrained Kerberos delegation, while allowed_to_delegate_to "+
			"configures constrained delegation; these are conceptually exclusive. Setting both is "+
			"unusual — prefer one delegation model.")
}

func (r *computerResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		computerEffectiveSamValidator{},
		computerDelegationConflictValidator{},
	}
}

// specFrom maps the plan onto the library's spec. Every optional string is
// passed as a pointer to its value — including the empty string, which the
// library turns into -Clear, because Active Directory has no empty attribute
// value. Unlike a gMSA, a computer has no create-only fields, so forCreate is
// carried for signature parity with the other resources but gates nothing.
func (r *computerResource) specFrom(ctx context.Context, m computerModel, forCreate bool, diags *diag.Diagnostics) adpwsh.ComputerSpec {
	sam := m.SamAccountName.ValueString()
	if sam == "" {
		// sam_account_name is Optional+Computed with no schema-level default,
		// so an omitted value arrives here as "": derive it from name, the
		// same way Active Directory's own default behaves (the library appends
		// the trailing "$").
		sam = m.Name.ValueString()
	}
	spec := adpwsh.ComputerSpec{
		Name:                 m.Name.ValueString(),
		SamAccountName:       sam,
		Container:            m.Container.ValueString(),
		DNSHostName:          adpwsh.String(m.DNSHostName.ValueString()),
		Description:          adpwsh.String(m.Description.ValueString()),
		DisplayName:          adpwsh.String(m.DisplayName.ValueString()),
		Location:             adpwsh.String(m.Location.ValueString()),
		ManagedBy:            adpwsh.String(m.ManagedBy.ValueString()),
		Enabled:              optBool(m.Enabled),
		TrustedForDelegation: optBool(m.TrustedForDelegation),
	}

	if !m.SPNs.IsNull() && !m.SPNs.IsUnknown() {
		var spns []string
		diags.Append(m.SPNs.ElementsAs(ctx, &spns, false)...)
		spec.ServicePrincipalNames = &spns
	}
	if !m.AllowedToDelegateTo.IsNull() && !m.AllowedToDelegateTo.IsUnknown() {
		var atd []string
		diags.Append(m.AllowedToDelegateTo.ElementsAs(ctx, &atd, false)...)
		spec.AllowedToDelegateTo = &atd
	}
	if !m.PrincipalsAllowedToDelegateToAccount.IsNull() && !m.PrincipalsAllowedToDelegateToAccount.IsUnknown() {
		var guids []string
		diags.Append(m.PrincipalsAllowedToDelegateToAccount.ElementsAs(ctx, &guids, false)...)
		ids := make([]adpwsh.Identity, len(guids))
		for i, g := range guids {
			ids[i] = adpwsh.ByGUID(g)
		}
		spec.PrincipalsAllowed = ids
	}
	if !m.KerberosEncryption.IsNull() && !m.KerberosEncryption.IsUnknown() {
		var kerb []string
		diags.Append(m.KerberosEncryption.ElementsAs(ctx, &kerb, false)...)
		spec.KerberosEncryptionType = &kerb
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
// service_principal_names, allowed_to_delegate_to and
// principals_allowed_to_delegate_to_account are Optional but not Computed, so
// Terraform requires the state after apply to equal exactly what was planned;
// the plan for an omitted value is always null, never "whatever Active
// Directory happens to hold" (which, left over from an earlier apply or an
// out-of-band change, need not be empty). Overwriting a null plan value with
// Active Directory's actual value would fail that consistency check, so all
// three are left untouched here when the model does not already hold a value —
// mirroring the library's own "nil leaves it alone" contract for them.
// kerberos_encryption_type has no such restriction: it is Computed, so the
// provider is always free to report Active Directory's actual value, as are
// the operating_system* fields (Computed, machine-owned).
func (r *computerResource) apply(ctx context.Context, c *adpwsh.Computer, m *computerModel, diags *diag.Diagnostics) {
	m.ID = types.StringValue(c.GUID)
	m.DN = types.StringValue(c.DN)
	m.SID = types.StringValue(c.SID)
	m.Name = types.StringValue(c.Name)
	// Active Directory appends "$" to a computer's sAMAccountName on read; the
	// sam_account_name attribute holds the un-suffixed base the user
	// configured (or that was derived from name), matching what specFrom sends
	// and what its MarkdownDescription documents. Storing the suffixed form
	// verbatim would both fail Terraform's plan-consistency check whenever
	// sam_account_name is set explicitly (planned "WEB01" vs applied "WEB01$")
	// and make every Update a spurious rewrite.
	m.SamAccountName = types.StringValue(strings.TrimSuffix(c.SamAccountName, "$"))
	m.Container = types.StringValue(c.Container)
	m.DNSHostName = types.StringValue(c.DNSHostName)
	m.Description = types.StringValue(c.Description)
	m.DisplayName = types.StringValue(c.DisplayName)
	m.Location = types.StringValue(c.Location)
	m.ManagedBy = types.StringValue(c.ManagedBy)
	m.Enabled = types.BoolValue(c.Enabled)
	m.TrustedForDelegation = types.BoolValue(c.TrustedForDelegation)

	if !m.SPNs.IsNull() {
		v, d := types.SetValueFrom(ctx, types.StringType, c.ServicePrincipalNames)
		diags.Append(d...)
		m.SPNs = v
	}
	if !m.AllowedToDelegateTo.IsNull() {
		v, d := types.SetValueFrom(ctx, types.StringType, c.AllowedToDelegateTo)
		diags.Append(d...)
		m.AllowedToDelegateTo = v
	}
	if !m.PrincipalsAllowedToDelegateToAccount.IsNull() {
		v, d := types.SetValueFrom(ctx, types.StringType, c.PrincipalsAllowed)
		diags.Append(d...)
		m.PrincipalsAllowedToDelegateToAccount = v
	}
	kerb, d := types.SetValueFrom(ctx, types.StringType, c.KerberosEncryptionType)
	diags.Append(d...)
	m.KerberosEncryption = kerb

	m.OperatingSystem = types.StringValue(c.OperatingSystem)
	m.OperatingSystemVersion = types.StringValue(c.OperatingSystemVersion)
	m.OperatingSystemServicePack = types.StringValue(c.OperatingSystemServicePack)

	if c.AccountExpiration == nil {
		m.AccountExpirationDate = types.StringValue("")
		return
	}
	m.AccountExpirationDate = types.StringValue(c.AccountExpiration.UTC().Format(time.RFC3339))
}

func (r *computerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan computerModel
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

	c, err := r.client.Computer.Create(ctx, spec)
	if c != nil {
		r.apply(ctx, c, &plan, &resp.Diagnostics)
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
	if err != nil {
		resp.Diagnostics.Append(errorDiagnostics("Computer.Create", computerResourceType, err)...)
	}
}

func (r *computerResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state computerModel
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

	c, err := r.client.Computer.Get(ctx, adpwsh.ByGUID(state.ID.ValueString()))
	if isNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.Append(errorDiagnostics("Computer.Get", computerResourceType, err)...)
		return
	}
	r.apply(ctx, c, &state, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *computerResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state computerModel
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

	c, err := r.client.Computer.Update(ctx, id, spec)
	if c != nil {
		r.apply(ctx, c, &plan, &resp.Diagnostics)
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
	if err != nil {
		resp.Diagnostics.Append(errorDiagnostics("Computer.Update", computerResourceType, err)...)
	}
}

func (r *computerResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state computerModel
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

	if err := r.client.Computer.Delete(ctx, adpwsh.ByGUID(state.ID.ValueString())); err != nil && !isNotFound(err) {
		resp.Diagnostics.Append(errorDiagnostics("Computer.Delete", computerResourceType, err)...)
	}
}

func (r *computerResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	c, err := r.client.Computer.Get(ctx, identityFromImportID(req.ID))
	if err != nil {
		resp.Diagnostics.Append(errorDiagnostics("Computer.Get", computerResourceType, err)...)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), c.GUID)...)
}

var (
	_ resource.Resource                     = (*computerResource)(nil)
	_ resource.ResourceWithConfigure        = (*computerResource)(nil)
	_ resource.ResourceWithConfigValidators = (*computerResource)(nil)
	_ resource.ResourceWithImportState      = (*computerResource)(nil)
)
