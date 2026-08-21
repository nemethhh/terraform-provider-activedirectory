package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	adpwsh "github.com/nemethhh/go-adpwsh"
)

const groupMembersDataSourceType = "activedirectory_group_members"

type groupMembersDataSource struct{ client *adpwsh.Client }

func newGroupMembersDataSource() datasource.DataSource { return &groupMembersDataSource{} }

type memberModel struct {
	DN    types.String `tfsdk:"dn"`
	GUID  types.String `tfsdk:"guid"`
	Class types.String `tfsdk:"class"`
	SID   types.String `tfsdk:"sid"`
}

type groupMembersDataSourceModel struct {
	GUID           types.String  `tfsdk:"guid"`
	DN             types.String  `tfsdk:"dn"`
	SID            types.String  `tfsdk:"sid"`
	SamAccountName types.String  `tfsdk:"sam_account_name"`
	Members        []memberModel `tfsdk:"members"`
}

func (d *groupMembersDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = groupMembersDataSourceType
}

func (d *groupMembersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := identitySelectorSchema(true)
	attrs["members"] = dschema.ListNestedAttribute{
		Computed:            true,
		MarkdownDescription: "The group's direct members.",
		NestedObject: dschema.NestedAttributeObject{Attributes: map[string]dschema.Attribute{
			"dn":    dschema.StringAttribute{Computed: true, MarkdownDescription: "Member DN."},
			"guid":  dschema.StringAttribute{Computed: true, MarkdownDescription: "Member objectGUID."},
			"class": dschema.StringAttribute{Computed: true, MarkdownDescription: "Member object class."},
			"sid":   dschema.StringAttribute{Computed: true, MarkdownDescription: "Member SID."},
		}},
	}
	resp.Schema = dschema.Schema{
		MarkdownDescription: "List a group's direct members. Identify the group by GUID, DN, SID, or sAMAccountName.",
		Attributes:          attrs,
	}
}

func (d *groupMembersDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return identitySelectorValidators(true)
}

func (d *groupMembersDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *groupMembersDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg groupMembersDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	members, err := d.client.Group.Members(ctx, identityFrom(cfg.GUID, cfg.DN, cfg.SID, cfg.SamAccountName))
	if err != nil {
		resp.Diagnostics.Append(errorDiagnostics("Group.Members", groupMembersDataSourceType, err)...)
		return
	}
	cfg.Members = make([]memberModel, 0, len(members))
	for _, m := range members {
		cfg.Members = append(cfg.Members, memberModel{
			DN: types.StringValue(m.DN), GUID: types.StringValue(m.GUID),
			Class: types.StringValue(m.Class), SID: types.StringValue(m.SID),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

var (
	_ datasource.DataSource                     = (*groupMembersDataSource)(nil)
	_ datasource.DataSourceWithConfigure        = (*groupMembersDataSource)(nil)
	_ datasource.DataSourceWithConfigValidators = (*groupMembersDataSource)(nil)
)
