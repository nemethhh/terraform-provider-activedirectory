package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	adpwsh "github.com/nemethhh/go-adpwsh"
)

const computersDataSourceType = "activedirectory_computers"

type computersDataSource struct{ client *adpwsh.Client }

func newComputersDataSource() datasource.DataSource { return &computersDataSource{} }

type computersDataSourceModel struct {
	Container  types.String              `tfsdk:"container"`
	Scope      types.String              `tfsdk:"scope"`
	FilterBy   types.Map                 `tfsdk:"filter_by"`
	LDAPFilter types.String              `tfsdk:"ldap_filter"`
	MaxResults types.Int64               `tfsdk:"max_results"`
	Computers  []computerDataSourceModel `tfsdk:"computers"`
}

func (d *computersDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = computersDataSourceType
}

func (d *computersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := searchInputAttributes()
	attrs["computers"] = dschema.ListNestedAttribute{Computed: true,
		MarkdownDescription: "Matching computers.",
		NestedObject:        dschema.NestedAttributeObject{Attributes: computerResultAttributes()}}
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Search for computers under a container by scope and filter.",
		Attributes:          attrs,
	}
}

func (d *computersDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *computersDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg computersDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	filterBy := map[string]string{}
	resp.Diagnostics.Append(cfg.FilterBy.ElementsAs(ctx, &filterBy, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	computers, err := d.client.Computer.Search(ctx, adpwsh.Query{
		Filter:     compileFilter(filterBy, cfg.LDAPFilter.ValueString()),
		SearchBase: cfg.Container.ValueString(),
		Scope:      scopeFromString(cfg.Scope.ValueString()),
		SizeLimit:  int(cfg.MaxResults.ValueInt64()),
	})
	if err != nil {
		resp.Diagnostics.Append(errorDiagnostics("Computer.Search", computersDataSourceType, err)...)
		return
	}
	cfg.Computers = make([]computerDataSourceModel, 0, len(computers))
	for i := range computers {
		var m computerDataSourceModel
		applyComputer(ctx, &m, &computers[i], &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
		cfg.Computers = append(cfg.Computers, m)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

var (
	_ datasource.DataSource              = (*computersDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*computersDataSource)(nil)
)
