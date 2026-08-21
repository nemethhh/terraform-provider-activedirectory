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

// ouResultAttributes is the full set of computed OU attributes, matching every
// tfsdk tag on ouDataSourceModel. The plural activedirectory_ous source uses it
// verbatim for each result object; the singular source overlays the identity
// selector on top of it, so the two sources share one projection.
func ouResultAttributes() map[string]dschema.Attribute {
	attrs := map[string]dschema.Attribute{}
	for name, desc := range map[string]string{
		"id": "The objectGUID.", "guid": "The objectGUID.",
		"dn": "The distinguished name.", "name": "The OU's name (RDN).",
		"container": "Distinguished name of the parent.", "description": "Free-text description.",
	} {
		attrs[name] = dschema.StringAttribute{Computed: true, MarkdownDescription: desc}
	}
	attrs["protected_from_accidental_deletion"] = dschema.BoolAttribute{Computed: true,
		MarkdownDescription: "Whether the OU is protected from accidental deletion."}
	return attrs
}

func (d *ouDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := identitySelectorSchema(false)
	for name, attr := range ouResultAttributes() {
		// guid/dn are the Optional selector on the singular source; the read
		// overwrites them with the canonical values afterwards.
		if _, isSelector := attrs[name]; isSelector {
			continue
		}
		attrs[name] = attr
	}
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Look up an organizational unit by GUID or DN. Errors if it does not exist.",
		Attributes:          attrs,
	}
}

// applyOU projects a library OU onto the model, filling every field. Shared by
// the singular source's Read and the plural source's per-result mapping.
func applyOU(m *ouDataSourceModel, o *adpwsh.OU) {
	m.ID = types.StringValue(o.GUID)
	m.GUID = types.StringValue(o.GUID)
	m.DN = types.StringValue(o.DN)
	m.Name = types.StringValue(o.Name)
	m.Container = types.StringValue(o.Container)
	m.Description = types.StringValue(o.Description)
	m.Protected = types.BoolValue(o.Protected)
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
	applyOU(&cfg, ou)
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

var (
	_ datasource.DataSource                     = (*ouDataSource)(nil)
	_ datasource.DataSourceWithConfigure        = (*ouDataSource)(nil)
	_ datasource.DataSourceWithConfigValidators = (*ouDataSource)(nil)
)
