package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	adpwsh "github.com/nemethhh/go-adpwsh"
)

const ousDataSourceType = "activedirectory_ous"

type ousDataSource struct{ client *adpwsh.Client }

func newOUsDataSource() datasource.DataSource { return &ousDataSource{} }

type ousDataSourceModel struct {
	Container  types.String        `tfsdk:"container"`
	Scope      types.String        `tfsdk:"scope"`
	FilterBy   types.Map           `tfsdk:"filter_by"`
	LDAPFilter types.String        `tfsdk:"ldap_filter"`
	MaxResults types.Int64         `tfsdk:"max_results"`
	OUs        []ouDataSourceModel `tfsdk:"ous"`
}

func (d *ousDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = ousDataSourceType
}

func (d *ousDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := searchInputAttributes()
	attrs["ous"] = dschema.ListNestedAttribute{Computed: true,
		MarkdownDescription: "Matching organizational units.",
		NestedObject:        dschema.NestedAttributeObject{Attributes: ouResultAttributes()}}
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Search for organizational units under a container by scope and filter.",
		Attributes:          attrs,
	}
}

func (d *ousDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *ousDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg ousDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	filterBy := map[string]string{}
	resp.Diagnostics.Append(cfg.FilterBy.ElementsAs(ctx, &filterBy, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ous, err := d.client.OU.Search(ctx, adpwsh.Query{
		Filter:     compileFilter(filterBy, cfg.LDAPFilter.ValueString()),
		SearchBase: cfg.Container.ValueString(),
		Scope:      scopeFromString(cfg.Scope.ValueString()),
		SizeLimit:  int(cfg.MaxResults.ValueInt64()),
	})
	if err != nil {
		resp.Diagnostics.Append(errorDiagnostics("OU.Search", ousDataSourceType, err)...)
		return
	}
	cfg.OUs = make([]ouDataSourceModel, 0, len(ous))
	for i := range ous {
		var m ouDataSourceModel
		applyOU(&m, &ous[i])
		cfg.OUs = append(cfg.OUs, m)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

var (
	_ datasource.DataSource              = (*ousDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*ousDataSource)(nil)
)
