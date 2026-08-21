package provider

import (
	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	adpwsh "github.com/nemethhh/go-adpwsh"
)

// identitySelectorSchema returns the optional identity attributes a singular
// data source accepts. security adds sid + sam_account_name, which OUs lack.
func identitySelectorSchema(security bool) map[string]dschema.Attribute {
	attrs := map[string]dschema.Attribute{
		"guid": dschema.StringAttribute{Optional: true,
			MarkdownDescription: "Look the object up by objectGUID."},
		"dn": dschema.StringAttribute{Optional: true,
			MarkdownDescription: "Look the object up by distinguished name."},
	}
	if security {
		attrs["sid"] = dschema.StringAttribute{Optional: true,
			MarkdownDescription: "Look the object up by security identifier."}
		attrs["sam_account_name"] = dschema.StringAttribute{Optional: true,
			MarkdownDescription: "Look the object up by sAMAccountName."}
	}
	return attrs
}

// identitySelectorValidators requires exactly one identity attribute to be set.
func identitySelectorValidators(security bool) []datasource.ConfigValidator {
	paths := []path.Expression{path.MatchRoot("guid"), path.MatchRoot("dn")}
	if security {
		paths = append(paths, path.MatchRoot("sid"), path.MatchRoot("sam_account_name"))
	}
	return []datasource.ConfigValidator{datasourcevalidator.ExactlyOneOf(paths...)}
}

// identityFrom builds an adpwsh.Identity from whichever attribute is set.
// ExactlyOneOf guarantees exactly one is non-null by the time Read runs.
func identityFrom(guid, dn, sid, sam types.String) adpwsh.Identity {
	switch {
	case !guid.IsNull() && guid.ValueString() != "":
		return adpwsh.ByGUID(guid.ValueString())
	case !dn.IsNull() && dn.ValueString() != "":
		return adpwsh.ByDN(dn.ValueString())
	case !sid.IsNull() && sid.ValueString() != "":
		return adpwsh.BySID(sid.ValueString())
	case !sam.IsNull() && sam.ValueString() != "":
		return adpwsh.BySAM(sam.ValueString())
	default:
		return nil
	}
}
