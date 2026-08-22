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
	Recursive      types.Bool    `tfsdk:"recursive"`
	Members        []memberModel `tfsdk:"members"`
}

func (d *groupMembersDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = groupMembersDataSourceType
}

func (d *groupMembersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := identitySelectorSchema(true)
	attrs["recursive"] = dschema.BoolAttribute{
		Optional: true,
		MarkdownDescription: "When `true`, return the group's effective membership: " +
			"every leaf account (user/computer) reachable through nested groups, with " +
			"intermediate group objects flattened away, matching " +
			"`Get-ADGroupMember -Recursive`. When `false` (the default), return only the " +
			"group's direct members, including any nested group objects. Recursive " +
			"results do not include primary-group-only membership (for example a user " +
			"whose primary group is the target, such as Domain Users).",
	}
	attrs["members"] = dschema.ListNestedAttribute{
		Computed: true,
		MarkdownDescription: "The group's members: direct members when `recursive` is " +
			"false, effective leaf accounts when `recursive` is true.",
		NestedObject: dschema.NestedAttributeObject{Attributes: map[string]dschema.Attribute{
			"dn":    dschema.StringAttribute{Computed: true, MarkdownDescription: "Member DN."},
			"guid":  dschema.StringAttribute{Computed: true, MarkdownDescription: "Member objectGUID."},
			"class": dschema.StringAttribute{Computed: true, MarkdownDescription: "Member object class."},
			"sid":   dschema.StringAttribute{Computed: true, MarkdownDescription: "Member SID."},
		}},
	}
	resp.Schema = dschema.Schema{
		MarkdownDescription: "List a group's members. Identify the group by GUID, DN, " +
			"SID, or sAMAccountName. By default the group's direct members are returned; " +
			"set `recursive = true` for the effective membership resolved through nested " +
			"groups.",
		Attributes: attrs,
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
	id := identityFrom(cfg.GUID, cfg.DN, cfg.SID, cfg.SamAccountName)
	var members []adpwsh.Member
	var err error
	if cfg.Recursive.ValueBool() {
		members, err = d.client.Group.MembersRecursive(ctx, id)
	} else {
		members, err = d.client.Group.Members(ctx, id)
	}
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
