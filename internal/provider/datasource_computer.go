package provider

import (
	"context"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	adpwsh "github.com/nemethhh/go-adpwsh"
)

const computerDataSourceType = "activedirectory_computer"

type computerDataSource struct{ client *adpwsh.Client }

func newComputerDataSource() datasource.DataSource { return &computerDataSource{} }

type computerDataSourceModel struct {
	ID                                   types.String `tfsdk:"id"`
	GUID                                 types.String `tfsdk:"guid"`
	DN                                   types.String `tfsdk:"dn"`
	SID                                  types.String `tfsdk:"sid"`
	SamAccountName                       types.String `tfsdk:"sam_account_name"`
	Name                                 types.String `tfsdk:"name"`
	Container                            types.String `tfsdk:"container"`
	DNSHostName                          types.String `tfsdk:"dns_hostname"`
	Description                          types.String `tfsdk:"description"`
	DisplayName                          types.String `tfsdk:"display_name"`
	Location                             types.String `tfsdk:"location"`
	ManagedBy                            types.String `tfsdk:"managed_by"`
	Enabled                              types.Bool   `tfsdk:"enabled"`
	TrustedForDelegation                 types.Bool   `tfsdk:"trusted_for_delegation"`
	SPNs                                 types.Set    `tfsdk:"service_principal_names"`
	AllowedToDelegateTo                  types.Set    `tfsdk:"allowed_to_delegate_to"`
	PrincipalsAllowedToDelegateToAccount types.Set    `tfsdk:"principals_allowed_to_delegate_to_account"`
	KerberosEncryption                   types.Set    `tfsdk:"kerberos_encryption_type"`
	AccountExpirationDate                types.String `tfsdk:"account_expiration_date"`
	OperatingSystem                      types.String `tfsdk:"operating_system"`
	OperatingSystemVersion               types.String `tfsdk:"operating_system_version"`
	OperatingSystemServicePack           types.String `tfsdk:"operating_system_service_pack"`
}

func (d *computerDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = computerDataSourceType
}

// computerResultAttributes is the full set of computed computer attributes,
// matching every tfsdk tag on computerDataSourceModel and every attribute the
// activedirectory_computer resource exposes (resource_computer.go), minus the
// resource's write-side concerns (defaults, plan modifiers, timeouts). It is a
// standalone function so the plural activedirectory_computers list data
// source (task 2.5) can reuse it verbatim, the same pattern
// gmsaResultAttributes establishes. The singular source overlays the identity
// selector on top of it.
func computerResultAttributes() map[string]dschema.Attribute {
	attrs := map[string]dschema.Attribute{}
	for name, desc := range map[string]string{
		"id": "The objectGUID.", "guid": "The objectGUID.",
		"dn": "The distinguished name.", "sid": "The security identifier.",
		"sam_account_name": "The pre-Windows-2000 logon name (sAMAccountName), without the " +
			"trailing \"$\" (matching the resource's own representation).",
		"name":                          "The CN.",
		"container":                     "Distinguished name of the parent.",
		"dns_hostname":                  "The DNS host name (dNSHostName).",
		"description":                   "Free-text description.",
		"display_name":                  "The name shown in address lists.",
		"location":                      "Free-form location (location).",
		"managed_by":                    "Distinguished name of the managing user or group (managedBy).",
		"account_expiration_date":       "Account expiry (RFC 3339), or \"\" if it never expires.",
		"operating_system":              "The operating system (operatingSystem). Read-only: the joined machine owns it.",
		"operating_system_version":      "The operating-system version (operatingSystemVersion). Read-only.",
		"operating_system_service_pack": "The operating-system service pack (operatingSystemServicePack). Read-only.",
	} {
		attrs[name] = dschema.StringAttribute{Computed: true, MarkdownDescription: desc}
	}
	for name, desc := range map[string]string{
		"enabled":                "Whether the account is enabled.",
		"trusted_for_delegation": "Whether the account is trusted for unconstrained Kerberos delegation.",
	} {
		attrs[name] = dschema.BoolAttribute{Computed: true, MarkdownDescription: desc}
	}
	for name, desc := range map[string]string{
		"service_principal_names": "The account's service principal names.",
		"allowed_to_delegate_to": "Constrained-delegation target service principal names " +
			"(msDS-AllowedToDelegateTo).",
		"principals_allowed_to_delegate_to_account": "Resource-based constrained delegation (RBCD): the " +
			"objectGUIDs of the principals allowed to delegate to this account.",
		"kerberos_encryption_type": "The Kerberos encryption types the account supports.",
	} {
		attrs[name] = dschema.SetAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: desc}
	}
	return attrs
}

func (d *computerDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := identitySelectorSchema(true)
	for name, attr := range computerResultAttributes() {
		// guid/dn/sid/sam_account_name are the Optional selector on the singular
		// source; the read fills them with canonical values afterwards.
		if _, isSelector := attrs[name]; isSelector {
			continue
		}
		attrs[name] = attr
	}
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Look up an Active Directory computer account by GUID, DN, SID, or " +
			"sAMAccountName. Errors if it does not exist.",
		Attributes: attrs,
	}
}

// applyComputer projects a library Computer onto the model, filling every
// field unconditionally: a data source always reports Active Directory's
// actual value, unlike the resource's apply() (resource_computer.go), which
// conditionally refreshes service_principal_names, allowed_to_delegate_to and
// principals_allowed_to_delegate_to_account to satisfy Terraform's plan
// consistency check. sam_account_name strips Active Directory's trailing "$",
// the same controller ruling resource_computer.go's apply() enforces, so the
// resource and this data source agree on the attribute's representation.
func applyComputer(ctx context.Context, m *computerDataSourceModel, c *adpwsh.Computer, diags *diag.Diagnostics) {
	m.ID = types.StringValue(c.GUID)
	m.GUID = types.StringValue(c.GUID)
	m.DN = types.StringValue(c.DN)
	m.SID = types.StringValue(c.SID)
	m.SamAccountName = types.StringValue(strings.TrimSuffix(c.SamAccountName, "$"))
	m.Name = types.StringValue(c.Name)
	m.Container = types.StringValue(c.Container)
	m.DNSHostName = types.StringValue(c.DNSHostName)
	m.Description = types.StringValue(c.Description)
	m.DisplayName = types.StringValue(c.DisplayName)
	m.Location = types.StringValue(c.Location)
	m.ManagedBy = types.StringValue(c.ManagedBy)
	m.Enabled = types.BoolValue(c.Enabled)
	m.TrustedForDelegation = types.BoolValue(c.TrustedForDelegation)

	spns, d := types.SetValueFrom(ctx, types.StringType, c.ServicePrincipalNames)
	diags.Append(d...)
	m.SPNs = spns

	atd, d := types.SetValueFrom(ctx, types.StringType, c.AllowedToDelegateTo)
	diags.Append(d...)
	m.AllowedToDelegateTo = atd

	principals, d := types.SetValueFrom(ctx, types.StringType, c.PrincipalsAllowed)
	diags.Append(d...)
	m.PrincipalsAllowedToDelegateToAccount = principals

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

func (d *computerDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return identitySelectorValidators(true)
}

func (d *computerDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

// computerIdentityFrom mirrors identityFrom, except for the sam case: a
// computer's sAMAccountName always carries Active Directory's own trailing
// "$" (the account's actual stored value), while sam_account_name in this
// model holds the un-suffixed base — the same convention resource_computer.go's
// apply() establishes. Re-appending "$" here (unless the caller already
// supplied it) hands the lookup Active Directory's exact stored value instead
// of relying on identity resolution to guess the "$" back on, matching what
// resource_computer.go's own SamAccountName-diff comparison already does in
// reverse.
func computerIdentityFrom(guid, dn, sid, sam types.String) adpwsh.Identity {
	switch {
	case !guid.IsNull() && guid.ValueString() != "":
		return adpwsh.ByGUID(guid.ValueString())
	case !dn.IsNull() && dn.ValueString() != "":
		return adpwsh.ByDN(dn.ValueString())
	case !sid.IsNull() && sid.ValueString() != "":
		return adpwsh.BySID(sid.ValueString())
	case !sam.IsNull() && sam.ValueString() != "":
		v := sam.ValueString()
		if !strings.HasSuffix(v, "$") {
			v += "$"
		}
		return adpwsh.BySAM(v)
	default:
		return nil
	}
}

func (d *computerDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg computerDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := d.client.Computer.Get(ctx, computerIdentityFrom(cfg.GUID, cfg.DN, cfg.SID, cfg.SamAccountName))
	if err != nil {
		resp.Diagnostics.Append(errorDiagnostics("Computer.Get", computerDataSourceType, err)...)
		return
	}
	applyComputer(ctx, &cfg, c, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

var (
	_ datasource.DataSource                     = (*computerDataSource)(nil)
	_ datasource.DataSourceWithConfigure        = (*computerDataSource)(nil)
	_ datasource.DataSourceWithConfigValidators = (*computerDataSource)(nil)
)
