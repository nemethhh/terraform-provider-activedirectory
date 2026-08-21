package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	adpwsh "github.com/nemethhh/go-adpwsh"
)

const userDataSourceType = "activedirectory_user"

type userDataSource struct{ client *adpwsh.Client }

func newUserDataSource() datasource.DataSource { return &userDataSource{} }

type userDataSourceModel struct {
	ID                    types.String `tfsdk:"id"`
	GUID                  types.String `tfsdk:"guid"`
	DN                    types.String `tfsdk:"dn"`
	SID                   types.String `tfsdk:"sid"`
	SamAccountName        types.String `tfsdk:"sam_account_name"`
	Name                  types.String `tfsdk:"name"`
	UserPrincipalName     types.String `tfsdk:"user_principal_name"`
	DisplayName           types.String `tfsdk:"display_name"`
	GivenName             types.String `tfsdk:"given_name"`
	Surname               types.String `tfsdk:"surname"`
	Description           types.String `tfsdk:"description"`
	Container             types.String `tfsdk:"container"`
	Enabled               types.Bool   `tfsdk:"enabled"`
	ChangePasswordAtLogon types.Bool   `tfsdk:"change_password_at_logon"`
	CanChangePassword     types.Bool   `tfsdk:"can_change_password"`
	PasswordExpires       types.Bool   `tfsdk:"password_expires"`
	AccountExpiration     types.String `tfsdk:"account_expiration_date"`
}

func (d *userDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = userDataSourceType
}

func (d *userDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := identitySelectorSchema(true)
	for name, desc := range map[string]string{
		"id": "The objectGUID.", "name": "The user's name (RDN).",
		"user_principal_name": "The UPN.", "display_name": "Display name.",
		"given_name": "First name.", "surname": "Last name.",
		"description": "Free-text description.", "container": "Distinguished name of the parent.",
		"account_expiration_date": "Account expiry (RFC 3339), or null if it never expires.",
	} {
		attrs[name] = dschema.StringAttribute{Computed: true, MarkdownDescription: desc}
	}
	for name, desc := range map[string]string{
		"enabled":                  "Whether the account is enabled.",
		"change_password_at_logon": "Whether the user must change password at next logon.",
		"can_change_password":      "Whether the user may change their password.",
		"password_expires":         "Whether the password expires.",
	} {
		attrs[name] = dschema.BoolAttribute{Computed: true, MarkdownDescription: desc}
	}
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Look up a user by GUID, DN, SID, or sAMAccountName. Errors if it does not exist.",
		Attributes:          attrs,
	}
}

func (d *userDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return identitySelectorValidators(true)
}

func (d *userDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *userDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg userDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	u, err := d.client.User.Get(ctx, identityFrom(cfg.GUID, cfg.DN, cfg.SID, cfg.SamAccountName))
	if err != nil {
		resp.Diagnostics.Append(errorDiagnostics("User.Get", userDataSourceType, err)...)
		return
	}
	cfg.ID = types.StringValue(u.GUID)
	cfg.GUID = types.StringValue(u.GUID)
	cfg.DN = types.StringValue(u.DN)
	cfg.SID = types.StringValue(u.SID)
	cfg.SamAccountName = types.StringValue(u.SamAccountName)
	cfg.Name = types.StringValue(u.Name)
	cfg.UserPrincipalName = types.StringValue(u.UserPrincipalName)
	cfg.DisplayName = types.StringValue(u.DisplayName)
	cfg.GivenName = types.StringValue(u.GivenName)
	cfg.Surname = types.StringValue(u.Surname)
	cfg.Description = types.StringValue(u.Description)
	cfg.Container = types.StringValue(u.Container)
	cfg.Enabled = types.BoolValue(u.Enabled)
	cfg.ChangePasswordAtLogon = types.BoolValue(u.ChangePasswordAtLogon)
	cfg.CanChangePassword = types.BoolValue(u.CanChangePassword)
	cfg.PasswordExpires = types.BoolValue(u.PasswordExpires)
	if u.AccountExpiration != nil {
		cfg.AccountExpiration = types.StringValue(u.AccountExpiration.Format("2006-01-02T15:04:05Z07:00"))
	} else {
		cfg.AccountExpiration = types.StringNull()
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

var (
	_ datasource.DataSource                     = (*userDataSource)(nil)
	_ datasource.DataSourceWithConfigure        = (*userDataSource)(nil)
	_ datasource.DataSourceWithConfigValidators = (*userDataSource)(nil)
)
