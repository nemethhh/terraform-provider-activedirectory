package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	adpwsh "github.com/nemethhh/go-adpwsh"
)

const ouDataSourceType = "activedirectory_ou"

type ouDataSource struct{ client *adpwsh.Client }

func newOUDataSource() datasource.DataSource { return &ouDataSource{} }

type ouDataSourceModel struct {
	ID          types.String `tfsdk:"id"`
	GUID        types.String `tfsdk:"guid"`
	DN          types.String `tfsdk:"dn"`
	Name        types.String `tfsdk:"name"`
	Container   types.String `tfsdk:"container"`
	Description types.String `tfsdk:"description"`
	Protected   types.Bool   `tfsdk:"protected_from_accidental_deletion"`
}

func (d *ouDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = ouDataSourceType
}

func (d *ouDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := identitySelectorSchema(false)
	attrs["id"] = dschema.StringAttribute{Computed: true, MarkdownDescription: "The objectGUID."}
	attrs["name"] = dschema.StringAttribute{Computed: true, MarkdownDescription: "The OU's name (RDN)."}
	attrs["container"] = dschema.StringAttribute{Computed: true, MarkdownDescription: "Distinguished name of the parent."}
	attrs["description"] = dschema.StringAttribute{Computed: true, MarkdownDescription: "Free-text description."}
	attrs["protected_from_accidental_deletion"] = dschema.BoolAttribute{Computed: true,
		MarkdownDescription: "Whether the OU is protected from accidental deletion."}
	// `dn` is in the selector as Optional; the read overwrites it with the
	// canonical value, so it doubles as a computed output.
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Look up an organizational unit by GUID or DN. Errors if it does not exist.",
		Attributes:          attrs,
	}
}

func (d *ouDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return identitySelectorValidators(false)
}

func (d *ouDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *ouDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg ouDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := identityFrom(cfg.GUID, cfg.DN, types.StringNull(), types.StringNull())
	ou, err := d.client.OU.Get(ctx, id)
	if err != nil {
		resp.Diagnostics.Append(errorDiagnostics("OU.Get", ouDataSourceType, err)...)
		return
	}
	cfg.ID = types.StringValue(ou.GUID)
	cfg.GUID = types.StringValue(ou.GUID)
	cfg.DN = types.StringValue(ou.DN)
	cfg.Name = types.StringValue(ou.Name)
	cfg.Container = types.StringValue(ou.Container)
	cfg.Description = types.StringValue(ou.Description)
	cfg.Protected = types.BoolValue(ou.Protected)
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

var (
	_ datasource.DataSource                     = (*ouDataSource)(nil)
	_ datasource.DataSourceWithConfigure        = (*ouDataSource)(nil)
	_ datasource.DataSourceWithConfigValidators = (*ouDataSource)(nil)
)
