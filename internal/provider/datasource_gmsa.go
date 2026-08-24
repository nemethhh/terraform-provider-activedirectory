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

const gmsaDataSourceType = "activedirectory_gmsa"

type gmsaDataSource struct{ client *adpwsh.Client }

func newGMSADataSource() datasource.DataSource { return &gmsaDataSource{} }

type gmsaDataSourceModel struct {
	ID                    types.String `tfsdk:"id"`
	GUID                  types.String `tfsdk:"guid"`
	DN                    types.String `tfsdk:"dn"`
	SID                   types.String `tfsdk:"sid"`
	SamAccountName        types.String `tfsdk:"sam_account_name"`
	Name                  types.String `tfsdk:"name"`
	Container             types.String `tfsdk:"container"`
	DNSHostName           types.String `tfsdk:"dns_hostname"`
	Description           types.String `tfsdk:"description"`
	DisplayName           types.String `tfsdk:"display_name"`
	Enabled               types.Bool   `tfsdk:"enabled"`
	TrustedForDelegation  types.Bool   `tfsdk:"trusted_for_delegation"`
	Principals            types.Set    `tfsdk:"principals_allowed_to_retrieve_managed_password"`
	SPNs                  types.Set    `tfsdk:"service_principal_names"`
	KerberosEncryption    types.Set    `tfsdk:"kerberos_encryption_type"`
	AccountExpirationDate types.String `tfsdk:"account_expiration_date"`
	Interval              types.Int64  `tfsdk:"managed_password_interval_in_days"`
}

func (d *gmsaDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = gmsaDataSourceType
}

// gmsaResultAttributes is the full set of computed gMSA attributes, matching
// every tfsdk tag on gmsaDataSourceModel and every attribute the
// activedirectory_gmsa resource exposes (resource_gmsa.go), minus the
// resource's write-side concerns (defaults, plan modifiers, timeouts). The
// singular source overlays the identity selector on top of it, the same
// pattern userResultAttributes/groupResultAttributes/ouResultAttributes
// already establish.
func gmsaResultAttributes() map[string]dschema.Attribute {
	attrs := map[string]dschema.Attribute{}
	for name, desc := range map[string]string{
		"id": "The objectGUID.", "guid": "The objectGUID.",
		"dn": "The distinguished name.", "sid": "The security identifier.",
		"sam_account_name": "The logon name (Active Directory's trailing \"$\" stripped, " +
			"matching the resource's own representation).",
		"name":                    "The CN.",
		"container":               "Distinguished name of the parent.",
		"dns_hostname":            "The FQDN associated with this account's default service principal names.",
		"description":             "Free-text description.",
		"display_name":            "The name shown in address lists.",
		"account_expiration_date": "Account expiry (RFC 3339), or \"\" if it never expires.",
	} {
		attrs[name] = dschema.StringAttribute{Computed: true, MarkdownDescription: desc}
	}
	for name, desc := range map[string]string{
		"enabled":                "Whether the account is enabled.",
		"trusted_for_delegation": "Whether the account is trusted for Kerberos delegation.",
	} {
		attrs[name] = dschema.BoolAttribute{Computed: true, MarkdownDescription: desc}
	}
	for name, desc := range map[string]string{
		"principals_allowed_to_retrieve_managed_password": "The objectGUIDs of the computers " +
			"or groups allowed to retrieve this account's managed password.",
		"service_principal_names":  "The account's service principal names.",
		"kerberos_encryption_type": "The Kerberos encryption types the account supports.",
	} {
		attrs[name] = dschema.SetAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: desc}
	}
	attrs["managed_password_interval_in_days"] = dschema.Int64Attribute{Computed: true,
		MarkdownDescription: "How often Active Directory rotates the managed password, in days."}
	return attrs
}

func (d *gmsaDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := identitySelectorSchema(true)
	for name, attr := range gmsaResultAttributes() {
		// guid/dn/sid/sam_account_name are the Optional selector on the singular
		// source; the read fills them with canonical values afterwards.
		if _, isSelector := attrs[name]; isSelector {
			continue
		}
		attrs[name] = attr
	}
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Look up a group Managed Service Account (gMSA) by GUID, DN, SID, " +
			"or sAMAccountName. Errors if it does not exist.",
		Attributes: attrs,
	}
}

// applyGMSA projects a library GMSA onto the model, filling every field.
// sam_account_name strips Active Directory's trailing "$", the same
// controller ruling resource_gmsa.go's apply() enforces, so the resource and
// this data source agree on the attribute's representation.
func applyGMSA(ctx context.Context, m *gmsaDataSourceModel, g *adpwsh.GMSA, diags *diag.Diagnostics) {
	m.ID = types.StringValue(g.GUID)
	m.GUID = types.StringValue(g.GUID)
	m.DN = types.StringValue(g.DN)
	m.SID = types.StringValue(g.SID)
	m.SamAccountName = types.StringValue(strings.TrimSuffix(g.SamAccountName, "$"))
	m.Name = types.StringValue(g.Name)
	m.Container = types.StringValue(g.Container)
	m.DNSHostName = types.StringValue(g.DNSHostName)
	m.Description = types.StringValue(g.Description)
	m.DisplayName = types.StringValue(g.DisplayName)
	m.Enabled = types.BoolValue(g.Enabled)
	m.TrustedForDelegation = types.BoolValue(g.TrustedForDelegation)

	principals, d := types.SetValueFrom(ctx, types.StringType, g.PrincipalsAllowed)
	diags.Append(d...)
	m.Principals = principals

	spns, d := types.SetValueFrom(ctx, types.StringType, g.ServicePrincipalNames)
	diags.Append(d...)
	m.SPNs = spns

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

func (d *gmsaDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return identitySelectorValidators(true)
}

func (d *gmsaDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *gmsaDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg gmsaDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	g, err := d.client.ServiceAccount.Get(ctx, identityFrom(cfg.GUID, cfg.DN, cfg.SID, cfg.SamAccountName))
	if err != nil {
		resp.Diagnostics.Append(errorDiagnostics("GMSA.Get", gmsaDataSourceType, err)...)
		return
	}
	applyGMSA(ctx, &cfg, g, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

var (
	_ datasource.DataSource                     = (*gmsaDataSource)(nil)
	_ datasource.DataSourceWithConfigure        = (*gmsaDataSource)(nil)
	_ datasource.DataSourceWithConfigValidators = (*gmsaDataSource)(nil)
)
