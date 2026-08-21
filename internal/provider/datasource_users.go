package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	adpwsh "github.com/nemethhh/go-adpwsh"
)

const usersDataSourceType = "activedirectory_users"

type usersDataSource struct{ client *adpwsh.Client }

func newUsersDataSource() datasource.DataSource { return &usersDataSource{} }

// scopeFromString maps the schema enum to the library scope. Shared by the
// three plural sources.
func scopeFromString(s string) adpwsh.SearchScope {
	switch s {
	case "base":
		return adpwsh.SearchScopeBase
	case "one_level":
		return adpwsh.SearchScopeOneLevel
	default:
		return adpwsh.SearchScopeSubtree
	}
}

// searchInputAttributes are the filter inputs common to every plural source.
func searchInputAttributes() map[string]dschema.Attribute {
	return map[string]dschema.Attribute{
		"container": dschema.StringAttribute{Optional: true,
			MarkdownDescription: "SearchBase DN. Defaults to the domain root."},
		"scope": dschema.StringAttribute{Optional: true,
			MarkdownDescription: "base, one_level, or subtree (default).",
			Validators:          []validator.String{stringvalidator.OneOf("base", "one_level", "subtree")}},
		"filter_by": dschema.MapAttribute{Optional: true, ElementType: types.StringType,
			MarkdownDescription: "Attribute equality terms, ANDed. Values are escaped for you."},
		"ldap_filter": dschema.StringAttribute{Optional: true,
			MarkdownDescription: "Raw LDAP filter, ANDed with filter_by. You own its correctness."},
		"max_results": dschema.Int64Attribute{Optional: true,
			MarkdownDescription: "Cap on results (default 1000). Exceeding it is an error, not truncation."},
	}
}

type usersDataSourceModel struct {
	Container  types.String          `tfsdk:"container"`
	Scope      types.String          `tfsdk:"scope"`
	FilterBy   types.Map             `tfsdk:"filter_by"`
	LDAPFilter types.String          `tfsdk:"ldap_filter"`
	MaxResults types.Int64           `tfsdk:"max_results"`
	Users      []userDataSourceModel `tfsdk:"users"`
}

func (d *usersDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = usersDataSourceType
}

func (d *usersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := searchInputAttributes()
	attrs["users"] = dschema.ListNestedAttribute{
		Computed:            true,
		MarkdownDescription: "Matching users.",
		NestedObject:        dschema.NestedAttributeObject{Attributes: userResultAttributes()},
	}
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Search for users under a container by scope and filter.",
		Attributes:          attrs,
	}
}

func (d *usersDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *usersDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg usersDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	filterBy := map[string]string{}
	resp.Diagnostics.Append(cfg.FilterBy.ElementsAs(ctx, &filterBy, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	q := adpwsh.Query{
		Filter:     compileFilter(filterBy, cfg.LDAPFilter.ValueString()),
		SearchBase: cfg.Container.ValueString(),
		Scope:      scopeFromString(cfg.Scope.ValueString()),
		SizeLimit:  int(cfg.MaxResults.ValueInt64()),
	}
	users, err := d.client.User.Search(ctx, q)
	if err != nil {
		resp.Diagnostics.Append(errorDiagnostics("User.Search", usersDataSourceType, err)...)
		return
	}
	cfg.Users = make([]userDataSourceModel, 0, len(users))
	for i := range users {
		var m userDataSourceModel
		applyUser(&m, &users[i])
		cfg.Users = append(cfg.Users, m)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

var (
	_ datasource.DataSource              = (*usersDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*usersDataSource)(nil)
)
