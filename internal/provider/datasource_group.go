package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	adpwsh "github.com/nemethhh/go-adpwsh"
)

const groupDataSourceType = "activedirectory_group"

type groupDataSource struct{ client *adpwsh.Client }

func newGroupDataSource() datasource.DataSource { return &groupDataSource{} }

type groupDataSourceModel struct {
	ID             types.String `tfsdk:"id"`
	GUID           types.String `tfsdk:"guid"`
	DN             types.String `tfsdk:"dn"`
	SID            types.String `tfsdk:"sid"`
	SamAccountName types.String `tfsdk:"sam_account_name"`
	Name           types.String `tfsdk:"name"`
	Container      types.String `tfsdk:"container"`
	Scope          types.String `tfsdk:"scope"`
	Category       types.String `tfsdk:"category"`
	Description    types.String `tfsdk:"description"`
	ManagedBy      types.String `tfsdk:"managed_by"`
}

func (d *groupDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = groupDataSourceType
}

func (d *groupDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := identitySelectorSchema(true)
	for name, desc := range map[string]string{
		"id": "The objectGUID.", "name": "The group's name (RDN).",
		"container": "Distinguished name of the parent.", "scope": "global, domainlocal, or universal.",
		"category": "security or distribution.", "description": "Free-text description.",
		"managed_by": "DN of the managing principal.",
	} {
		attrs[name] = dschema.StringAttribute{Computed: true, MarkdownDescription: desc}
	}
	// sam_account_name and sid are in the selector (Optional); the read fills
	// them with canonical values.
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Look up a group by GUID, DN, SID, or sAMAccountName. Errors if it does not exist.",
		Attributes:          attrs,
	}
}

func (d *groupDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return identitySelectorValidators(true)
}

func (d *groupDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *groupDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg groupDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	g, err := d.client.Group.Get(ctx, identityFrom(cfg.GUID, cfg.DN, cfg.SID, cfg.SamAccountName))
	if err != nil {
		resp.Diagnostics.Append(errorDiagnostics("Group.Get", groupDataSourceType, err)...)
		return
	}
	cfg.ID = types.StringValue(g.GUID)
	cfg.GUID = types.StringValue(g.GUID)
	cfg.DN = types.StringValue(g.DN)
	cfg.SID = types.StringValue(g.SID)
	cfg.SamAccountName = types.StringValue(g.SamAccountName)
	cfg.Name = types.StringValue(g.Name)
	cfg.Container = types.StringValue(g.Container)
	cfg.Scope = types.StringValue(string(g.Scope))
	cfg.Category = types.StringValue(string(g.Category))
	cfg.Description = types.StringValue(g.Description)
	cfg.ManagedBy = types.StringValue(g.ManagedBy)
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

var (
	_ datasource.DataSource                     = (*groupDataSource)(nil)
	_ datasource.DataSourceWithConfigure        = (*groupDataSource)(nil)
	_ datasource.DataSourceWithConfigValidators = (*groupDataSource)(nil)
)
