package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	adpwsh "github.com/nemethhh/go-adpwsh"
)

const groupsDataSourceType = "activedirectory_groups"

type groupsDataSource struct{ client *adpwsh.Client }

func newGroupsDataSource() datasource.DataSource { return &groupsDataSource{} }

type groupsDataSourceModel struct {
	Container  types.String           `tfsdk:"container"`
	Scope      types.String           `tfsdk:"scope"`
	FilterBy   types.Map              `tfsdk:"filter_by"`
	LDAPFilter types.String           `tfsdk:"ldap_filter"`
	MaxResults types.Int64            `tfsdk:"max_results"`
	Groups     []groupDataSourceModel `tfsdk:"groups"`
}

func (d *groupsDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = groupsDataSourceType
}

func (d *groupsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := searchInputAttributes()
	attrs["groups"] = dschema.ListNestedAttribute{Computed: true,
		MarkdownDescription: "Matching groups.",
		NestedObject:        dschema.NestedAttributeObject{Attributes: groupResultAttributes()}}
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Search for groups under a container by scope and filter.",
		Attributes:          attrs,
	}
}

func (d *groupsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *groupsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg groupsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	filterBy := map[string]string{}
	resp.Diagnostics.Append(cfg.FilterBy.ElementsAs(ctx, &filterBy, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	groups, err := d.client.Group.Search(ctx, adpwsh.Query{
		Filter:     compileFilter(filterBy, cfg.LDAPFilter.ValueString()),
		SearchBase: cfg.Container.ValueString(),
		Scope:      scopeFromString(cfg.Scope.ValueString()),
		SizeLimit:  int(cfg.MaxResults.ValueInt64()),
	})
	if err != nil {
		resp.Diagnostics.Append(errorDiagnostics("Group.Search", groupsDataSourceType, err)...)
		return
	}
	cfg.Groups = make([]groupDataSourceModel, 0, len(groups))
	for i := range groups {
		var m groupDataSourceModel
		applyGroup(&m, &groups[i])
		cfg.Groups = append(cfg.Groups, m)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

var (
	_ datasource.DataSource              = (*groupsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*groupsDataSource)(nil)
)
