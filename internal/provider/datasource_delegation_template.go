package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	adpwsh "github.com/nemethhh/go-adpwsh"
)

const delegationTemplateDataSourceType = "activedirectory_delegation_template"

type delegationTemplateDataSource struct{}

func newDelegationTemplateDataSource() datasource.DataSource { return &delegationTemplateDataSource{} }

type appliesToModel struct {
	Scope       types.String `tfsdk:"scope"`
	ObjectClass types.String `tfsdk:"object_class"`
}

type templateRuleModel struct {
	Rights     types.List     `tfsdk:"rights"`
	ObjectType types.String   `tfsdk:"object_type"`
	AppliesTo  appliesToModel `tfsdk:"applies_to"`
	Type       types.String   `tfsdk:"type"`
}

type delegationTemplateModel struct {
	Task  types.String        `tfsdk:"task"`
	Rules []templateRuleModel `tfsdk:"rules"`
}

func (d *delegationTemplateDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = delegationTemplateDataSourceType
}

func (d *delegationTemplateDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Expands a curated delegation task into the concrete access-control " +
			"rules that implement it. Feed `rules` into `activedirectory_access_rule` with `for_each`. " +
			"Pure computation — no directory is read.",
		Attributes: map[string]dschema.Attribute{
			"task": dschema.StringAttribute{
				Required:            true,
				Validators:          []validator.String{stringvalidator.OneOf("reset_user_passwords", "manage_users", "modify_group_membership", "manage_groups")},
				MarkdownDescription: "One of `reset_user_passwords`, `manage_users`, `modify_group_membership`, `manage_groups`.",
			},
			"rules": dschema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The access-control rules this task expands into.",
				NestedObject: dschema.NestedAttributeObject{Attributes: map[string]dschema.Attribute{
					"rights":      dschema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "AD rights names."},
					"object_type": dschema.StringAttribute{Computed: true, MarkdownDescription: "Object type (friendly name); `\"\"` = all."},
					"type":        dschema.StringAttribute{Computed: true, MarkdownDescription: "`Allow` or `Deny`."},
					"applies_to": dschema.SingleNestedAttribute{Computed: true, Attributes: map[string]dschema.Attribute{
						"scope":        dschema.StringAttribute{Computed: true, MarkdownDescription: "`this`, `descendants`, or `children`."},
						"object_class": dschema.StringAttribute{Computed: true, MarkdownDescription: "Inherited object class (friendly name); `\"\"` = all."},
					}},
				}},
			},
		},
	}
}

func rightStrings(rs []adpwsh.Right) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = string(r)
	}
	return out
}

func (d *delegationTemplateDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg delegationTemplateModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var deleg adpwsh.DelegationClient
	specs, err := deleg.Template(adpwsh.DelegationTask(cfg.Task.ValueString()))
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("task"), "Unknown delegation task", err.Error())
		return
	}
	cfg.Rules = make([]templateRuleModel, 0, len(specs))
	for _, s := range specs {
		rlist, dl := types.ListValueFrom(ctx, types.StringType, rightStrings(s.Rights))
		resp.Diagnostics.Append(dl...)
		cfg.Rules = append(cfg.Rules, templateRuleModel{
			Rights:     rlist,
			ObjectType: types.StringValue(s.ObjectType),
			Type:       types.StringValue(string(s.Type)),
			AppliesTo:  appliesToModel{Scope: types.StringValue(string(s.Scope)), ObjectClass: types.StringValue(s.ObjectClass)},
		})
	}
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

var _ datasource.DataSource = (*delegationTemplateDataSource)(nil)
