package provider

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	adpwsh "github.com/nemethhh/go-adpwsh"
)

const accessRuleResourceType = "activedirectory_access_rule"

// sidShapePattern recognises a value that already looks like a SID, so trustee
// resolution can skip the Group.Get/User.Get round trip for it. Matches
// identity.go's case-insensitive "S-1-" prefix check for SID detection, so a
// lowercase "s-1-..." trustee is recognised directly here too; this is only a
// fast pre-check, so a value that slips past it still resolves correctly via
// Group.Get/User.Get.
var sidShapePattern = regexp.MustCompile(`(?i)^S-1-`)

// appliesToAttrTypes is the object type of the applies_to nested attribute,
// shared between the schema default and ImportState.
var appliesToAttrTypes = map[string]attr.Type{
	"scope":        types.StringType,
	"object_class": types.StringType,
}

type accessRuleResource struct{ client *adpwsh.Client }

func newAccessRuleResource() resource.Resource { return &accessRuleResource{} }

// accessRuleAppliesTo is a convenience shape for building applies_to values
// with ObjectValueFrom (used today only by ImportState); the symmetric .As()
// direction is reserved for future read-side use and nothing calls it yet.
// It is never the state model's field type: see accessRuleModel.AppliesTo for
// why.
type accessRuleAppliesTo struct {
	Scope       types.String `tfsdk:"scope"`
	ObjectClass types.String `tfsdk:"object_class"`
}

// accessRuleModel.AppliesTo is a types.Object, not the accessRuleAppliesTo
// struct, because terraform-plugin-framework cannot decode an UNKNOWN object
// value into a plain Go struct — only types.Object (basetypes.ObjectValue)
// can represent "unknown" at the object level. applies_to is unknown at plan
// time whenever it is sourced from a computed value (e.g.
// each.value.applies_to fanned out from activedirectory_delegation_template
// via for_each); decoding that into accessRuleAppliesTo previously failed
// plan with "Value Conversion Error ... target type cannot handle unknown
// values". Use appliesToAttributes (below) to read scope/object_class out of
// it, guarding IsNull()/IsUnknown().
type accessRuleModel struct {
	ID         types.String   `tfsdk:"id"`
	Target     types.String   `tfsdk:"target"`
	Trustee    types.String   `tfsdk:"trustee"`
	TrusteeSID types.String   `tfsdk:"trustee_sid"`
	Rights     types.Set      `tfsdk:"rights"`
	ObjectType types.String   `tfsdk:"object_type"`
	AppliesTo  types.Object   `tfsdk:"applies_to"`
	Type       types.String   `tfsdk:"type"`
	Timeouts   timeouts.Value `tfsdk:"timeouts"`
}

// appliesToAttributes extracts the scope and object_class attributes out of
// an applies_to object value. A null or unknown object itself has no
// attributes to read, so both are returned null in that case — callers apply
// whatever default or unknown-skip behaviour is appropriate for them.
func appliesToAttributes(o types.Object) (scope, objectClass types.String) {
	if o.IsNull() || o.IsUnknown() {
		return types.StringNull(), types.StringNull()
	}
	attrs := o.Attributes()
	// These type assertions fall back to a null types.String on a missing key
	// or a mismatched type, but that fallback is unreachable in practice: o's
	// object type is always appliesToAttrTypes, which guarantees both "scope"
	// and "object_class" are present as types.String.
	scope, _ = attrs["scope"].(types.String)
	objectClass, _ = attrs["object_class"].(types.String)
	return scope, objectClass
}

// effectiveAppliesTo returns the effective scope and object_class an
// applies_to object represents, applying the schema's own "this"/""
// defaults whenever the object itself, or one of its two fields, is null (or
// unknown — the same fallback resolveACE already relied on before this was
// extracted). Both call sites — appliesToScopeValidator and resolveACE — used
// to compute this inline, identically; a caller that needs to distinguish
// "still unknown" from "defaulted" (as appliesToScopeValidator does, to skip
// validation rather than validate against a default) still checks
// IsUnknown() itself before calling this.
func effectiveAppliesTo(o types.Object) (scope, objectClass string) {
	scopeVal, objectClassVal := appliesToAttributes(o)

	scope = string(adpwsh.InheritanceThis)
	if !scopeVal.IsNull() && !scopeVal.IsUnknown() {
		scope = scopeVal.ValueString()
	}

	objectClass = ""
	if !objectClassVal.IsNull() && !objectClassVal.IsUnknown() {
		objectClass = objectClassVal.ValueString()
	}

	return scope, objectClass
}

func (r *accessRuleResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = accessRuleResourceType
}

func (r *accessRuleResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	appliesToDefault := types.ObjectValueMust(appliesToAttrTypes, map[string]attr.Value{
		"scope":        types.StringValue(string(adpwsh.InheritanceThis)),
		"object_class": types.StringValue(""),
	})

	resp.Schema = schema.Schema{
		MarkdownDescription: "A single explicit access-control entry (ACE) on an object's " +
			"discretionary ACL — one delegation grant. Each resource manages exactly one ACE " +
			"and leaves every other entry on the DACL untouched, so several may target the " +
			"same object and inherited or out-of-band entries are never removed. An ACE has " +
			"no mutable identity, so every content attribute forces a replace: changing any " +
			"of them revokes the old ACE and grants a new one.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				MarkdownDescription: "`<target>|<trustee_sid>|<type>|<sorted rights>|<object_type " +
					"guid>|<object_class guid>|<scope>`.",
			},
			"target": schema.StringAttribute{
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				MarkdownDescription: "The object the ACE is granted on, as a distinguished name or " +
					"objectGUID. Used verbatim as the ACL identity — it is **not** resolved to a " +
					"GUID. Targeting by DN means a rename or move of that object changes its DN " +
					"out from under this resource and requires re-import; target by objectGUID to " +
					"avoid that.",
			},
			"trustee": schema.StringAttribute{
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				MarkdownDescription: "The security principal granted the rule: an objectGUID, DN, " +
					"SID, or sAMAccountName of a user or group. Resolved to a SID at apply time.",
			},
			"trustee_sid": schema.StringAttribute{
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				MarkdownDescription: "The trustee's resolved SID, as stored on the DACL.",
			},
			"rights": schema.SetAttribute{
				Required:      true,
				ElementType:   types.StringType,
				PlanModifiers: []planmodifier.Set{setplanmodifier.RequiresReplace()},
				MarkdownDescription: "The `System.DirectoryServices.ActiveDirectoryRights` values " +
					"granted, e.g. `[\"ExtendedRight\"]` or `[\"ReadProperty\", \"WriteProperty\"]`.",
			},
			"object_type": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString(""),
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				MarkdownDescription: "The attribute, class, or extended right the rule is scoped " +
					"to — a friendly schema name (e.g. `\"Reset Password\"`) or a GUID; empty means " +
					"all. Which schema partition the name resolves against is inferred from " +
					"`rights`: `CreateChild`/`DeleteChild` resolve it as a class, `ExtendedRight` " +
					"resolves it as an extended right, and anything else resolves it as an " +
					"attribute.",
			},
			"type": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString(string(adpwsh.ACEAllow)),
				Validators: []validator.String{
					stringvalidator.OneOf(string(adpwsh.ACEAllow), string(adpwsh.ACEDeny)),
				},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				MarkdownDescription: "`Allow` or `Deny`. Defaults to `Allow`.",
			},
			"applies_to": schema.SingleNestedAttribute{
				Optional: true,
				Computed: true,
				Default:  objectdefault.StaticValue(appliesToDefault),
				PlanModifiers: []planmodifier.Object{
					objectplanmodifier.RequiresReplace(),
				},
				MarkdownDescription: "How the ACE propagates.",
				Attributes: map[string]schema.Attribute{
					"scope": schema.StringAttribute{
						Optional: true,
						Computed: true,
						Default:  stringdefault.StaticString(string(adpwsh.InheritanceThis)),
						Validators: []validator.String{
							stringvalidator.OneOf(
								string(adpwsh.InheritanceThis),
								string(adpwsh.InheritanceDescendants),
								string(adpwsh.InheritanceChildren),
							),
						},
						MarkdownDescription: "`this` (the object only), `descendants` (all " +
							"descendants, scoped by `object_class`), or `children` (immediate " +
							"children only, scoped by `object_class`). Defaults to `this`. " +
							"`object_class` is only meaningful together with `descendants` or " +
							"`children`; with `scope = \"this\"` the rule applies to the target " +
							"object itself and `object_class` must be empty.",
					},
					"object_class": schema.StringAttribute{
						Optional: true,
						Computed: true,
						Default:  stringdefault.StaticString(""),
						MarkdownDescription: "The child class the `descendants` or `children` " +
							"scope is limited to — a friendly class name (e.g. `\"user\"`) or a " +
							"GUID; empty means all classes. Only meaningful when `scope` is " +
							"`descendants` or `children`; must be empty when `scope` is `this`.",
					},
				},
			},
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{Create: true, Read: true, Delete: true}),
		},
	}
}

func (r *accessRuleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

// appliesToScopeValidator rejects an applies_to.object_class paired with
// applies_to.scope = "this". InheritanceThis maps to .NET's
// ActiveDirectorySecurityInheritance.None, which carries no
// inheritedObjectType, so a real DC does not persist or report one: Read would
// see object_class come back "" while state still holds the configured value,
// canonicalACEKey would stop matching, and the resource would loop through
// RemoveResource and recreate forever. The four delegation templates never
// pair "this" with a class, but a hand-written config can, so this is caught
// at plan time instead.
type appliesToScopeValidator struct{}

func (appliesToScopeValidator) Description(context.Context) string {
	return "applies_to.object_class must be empty when applies_to.scope is \"this\"."
}

func (v appliesToScopeValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (appliesToScopeValidator) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config accessRuleModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// An unknown applies_to (e.g. sourced from a computed value such as
	// each.value.applies_to fanned out from activedirectory_delegation_template)
	// cannot be checked until apply time; let it through here. Likewise for an
	// unknown individual attribute within an otherwise-known object.
	if config.AppliesTo.IsUnknown() {
		return
	}
	scope, objectClass := appliesToAttributes(config.AppliesTo)
	if scope.IsUnknown() || objectClass.IsUnknown() {
		return
	}

	// Both fields default within the nested attribute itself, so a config that
	// omits one or both (or the whole applies_to object) reads back null here,
	// not the eventual default. Apply the same defaults ("this" / "") the
	// schema would, so the omitted-scope case (object_class set, scope left to
	// default to "this") is caught too, not just an explicit scope = "this".
	effectiveScope, effectiveObjectClass := effectiveAppliesTo(config.AppliesTo)

	if effectiveScope == string(adpwsh.InheritanceThis) && effectiveObjectClass != "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("applies_to").AtName("object_class"),
			"Invalid applies_to combination",
			"applies_to.object_class is only meaningful when applies_to.scope is \"descendants\" or "+
				"\"children\"; with scope = \"this\" the rule applies to the target object itself, so "+
				"object_class must be empty.",
		)
	}
}

func (r *accessRuleResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{appliesToScopeValidator{}}
}

// inferObjectTypeRefKind decides which schema partition object_type names,
// from the rights it is paired with (ruling P2): CreateChild/DeleteChild name
// a child class, ExtendedRight names an extended right, and anything else
// (ReadProperty/WriteProperty/...) names an attribute.
func inferObjectTypeRefKind(rights []adpwsh.Right) adpwsh.SchemaRefKind {
	for _, right := range rights {
		if right == "CreateChild" || right == "DeleteChild" {
			return adpwsh.RefClass
		}
	}
	for _, right := range rights {
		if right == "ExtendedRight" {
			return adpwsh.RefExtendedRight
		}
	}
	return adpwsh.RefAttribute
}

// resolveSchemaRef resolves a single friendly name to a GUID, passing GUIDs
// and the empty string through unresolved.
func (r *accessRuleResource) resolveSchemaRef(ctx context.Context, ref adpwsh.SchemaRef, attrPath path.Path) (string, diag.Diagnostics) {
	var diags diag.Diagnostics
	if ref.Name == "" || guidPattern.MatchString(ref.Name) {
		return ref.Name, diags
	}
	resolved, err := r.client.Schema.Resolve(ctx, []adpwsh.SchemaRef{ref})
	if err != nil {
		diags.Append(attributeErrorDiagnostics("Schema.Resolve", accessRuleResourceType, err, attrPath)...)
		return "", diags
	}
	guid := resolved[ref]
	if guid == "" {
		diags.AddAttributeError(attrPath, "Schema name not found",
			fmt.Sprintf("%q did not resolve to a schema GUID.", ref.Name))
	}
	return guid, diags
}

// resolveTrusteeSID resolves trustee to a SID (ruling P3): a value already
// shaped like a SID is used verbatim; otherwise a group is tried first, then a
// user, treating any error — not only not-found — as "try the next".
func (r *accessRuleResource) resolveTrusteeSID(ctx context.Context, trustee string) (string, diag.Diagnostics) {
	var diags diag.Diagnostics
	if sidShapePattern.MatchString(trustee) {
		return trustee, diags
	}
	id := identityFromImportID(trustee)
	if g, err := r.client.Group.Get(ctx, id); err == nil {
		return g.SID, diags
	}
	u, err := r.client.User.Get(ctx, id)
	if err == nil {
		return u.SID, diags
	}
	// Both lookups failed; the group error is discarded (any-error-try-next,
	// ruling P3), but the user lookup's error — the last one tried — is
	// included below so a transient transport failure reads differently from
	// a genuine "no such group or user".
	diags.AddAttributeError(path.Root("trustee"), "Trustee not found",
		fmt.Sprintf("%q did not resolve as a group or a user. Last error (from the user lookup): %s",
			trustee, err))
	return "", diags
}

// resolveACE rebuilds the ACE the model describes, re-resolving every friendly
// name to the GUID/SID the DACL stores it as. It is called on every operation
// (ruling: there are no separate *_guid state fields) and is idempotent against
// already-resolved input: a value already shaped like a GUID or SID passes
// through, which is what makes the post-import Read match cleanly.
func (r *accessRuleResource) resolveACE(ctx context.Context, m accessRuleModel) (ace adpwsh.ACE, objectTypeGUID, objectClassGUID string, diags diag.Diagnostics) {
	var rightNames []string
	diags.Append(m.Rights.ElementsAs(ctx, &rightNames, false)...)
	if diags.HasError() {
		return adpwsh.ACE{}, "", "", diags
	}
	rights := make([]adpwsh.Right, len(rightNames))
	for i, n := range rightNames {
		rights[i] = adpwsh.Right(n)
	}

	objectTypeGUID, d := r.resolveSchemaRef(ctx,
		adpwsh.SchemaRef{Kind: inferObjectTypeRefKind(rights), Name: m.ObjectType.ValueString()},
		path.Root("object_type"))
	diags.Append(d...)

	// At Create/Read/Delete, applies_to comes from the plan or from state, so
	// it is known by the time resolveACE runs even when it was unknown at
	// plan time for an intermediate step; a null object (or null attribute
	// within it) falls back to the schema's own defaults ("this"/"").
	scope, objectClass := effectiveAppliesTo(m.AppliesTo)

	objectClassGUID, d = r.resolveSchemaRef(ctx,
		adpwsh.SchemaRef{Kind: adpwsh.RefClass, Name: objectClass},
		path.Root("applies_to").AtName("object_class"))
	diags.Append(d...)

	// The trustee's SID is cached in state at Create and by ImportState. Once
	// it is known, reuse it instead of re-resolving trustee live: a
	// DN-or-SAM-shaped trustee that is later renamed or moved must not break
	// every subsequent Read/Delete, and this is what "resolved to a SID at
	// apply time" (the trustee schema doc) means. Only Create, where
	// trustee_sid is still empty/unknown, does the live resolution.
	sid := m.TrusteeSID.ValueString()
	if sid == "" {
		resolved, d := r.resolveTrusteeSID(ctx, m.Trustee.ValueString())
		diags.Append(d...)
		sid = resolved
	}

	if diags.HasError() {
		return adpwsh.ACE{}, "", "", diags
	}

	ace = adpwsh.ACE{
		Trustee:             sid,
		Type:                adpwsh.ACEType(m.Type.ValueString()),
		Rights:              rights,
		ObjectType:          objectTypeGUID,
		InheritedObjectType: objectClassGUID,
		Inheritance:         adpwsh.Inheritance(scope),
	}
	return ace, objectTypeGUID, objectClassGUID, diags
}

// idFor is the resource ID: target|trustee_sid|type|sorted-rights|object_type
// guid|object_class guid|scope — reversible for import and stable across a
// target rename/move only insofar as target itself is (ruling: target is used
// verbatim, so a DN target is not stable across rename/move).
//
// This assumes none of target (a DN or GUID), trustee (a SID), or the two
// schema GUIDs ever contains a literal "|" — true for all four — the same
// kind of deliberate separator invariant dn.go relies on for commas in a DN.
func idFor(target string, ace adpwsh.ACE, objectTypeGUID, objectClassGUID string) string {
	rights := make([]string, len(ace.Rights))
	for i, right := range ace.Rights {
		rights[i] = string(right)
	}
	sort.Strings(rights)
	return strings.Join([]string{
		target,
		ace.Trustee,
		string(ace.Type),
		strings.Join(rights, ","),
		objectTypeGUID,
		objectClassGUID,
		string(ace.Inheritance),
	}, "|")
}

// canonicalACEKey is the semantic identity of an ACE for matching purposes
// (used only for Read drift-matching, never persisted — unlike idFor's ID,
// changing this needs no state migration). It mirrors the library's
// unexported canonicalACEKey (acl.go), which the provider cannot import:
// case-insensitive, and order-insensitive over Rights. Fields join on
// "\x1f" (US) and rights join on "\x1e" (RS) — control characters that can
// never appear in a SID, GUID, AD rights name, or enum value — so no field's
// content can ever be mistaken for a separator.
func canonicalACEKey(a adpwsh.ACE) string {
	rights := make([]string, len(a.Rights))
	for i, right := range a.Rights {
		rights[i] = strings.ToLower(string(right))
	}
	sort.Strings(rights)
	parts := []string{
		strings.ToLower(a.Trustee),
		strings.ToLower(string(a.Type)),
		strings.Join(rights, "\x1e"),
		strings.ToLower(a.ObjectType),
		strings.ToLower(a.InheritedObjectType),
		strings.ToLower(string(a.Inheritance)),
	}
	return strings.Join(parts, "\x1f")
}

func (r *accessRuleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan accessRuleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel, diags := withTimeout(ctx, plan.Timeouts.Create)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	defer cancel()

	ace, objectTypeGUID, objectClassGUID, diags := r.resolveACE(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	target := plan.Target.ValueString()
	if err := r.client.ACL.Grant(ctx, identityFromImportID(target), []adpwsh.ACE{ace}); err != nil {
		resp.Diagnostics.Append(errorDiagnostics("ACL.Grant", accessRuleResourceType, err)...)
		return
	}

	plan.TrusteeSID = types.StringValue(ace.Trustee)
	plan.ID = types.StringValue(idFor(target, ace, objectTypeGUID, objectClassGUID))
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *accessRuleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state accessRuleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel, diags := withTimeout(ctx, state.Timeouts.Read)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	defer cancel()

	ace, _, _, diags := r.resolveACE(ctx, state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	aces, err := r.client.ACL.Get(ctx, identityFromImportID(state.Target.ValueString()))
	if isNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.Append(errorDiagnostics("ACL.Get", accessRuleResourceType, err)...)
		return
	}

	want := canonicalACEKey(ace)
	found := false
	for _, a := range aces {
		if a.Inherited {
			continue
		}
		if canonicalACEKey(a) == want {
			found = true
			break
		}
	}
	if !found {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update is unreachable: every non-computed attribute forces a replace. It
// exists only to satisfy the resource.Resource interface.
func (r *accessRuleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan accessRuleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *accessRuleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state accessRuleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel, diags := withTimeout(ctx, state.Timeouts.Delete)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	defer cancel()

	ace, _, _, diags := r.resolveACE(ctx, state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.ACL.Revoke(ctx, identityFromImportID(state.Target.ValueString()), []adpwsh.ACE{ace})
	if err != nil && !isNotFound(err) {
		resp.Diagnostics.Append(errorDiagnostics("ACL.Revoke", accessRuleResourceType, err)...)
	}
}

func (r *accessRuleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	fields := strings.Split(req.ID, "|")
	if len(fields) != 7 {
		resp.Diagnostics.AddError("Invalid import ID",
			fmt.Sprintf("Expected 7 \"|\"-separated fields "+
				"(target|trustee_sid|type|rights|object_type|object_class|scope), got %d in %q.",
				len(fields), req.ID))
		return
	}
	target, trusteeSID, aceType := fields[0], fields[1], fields[2]
	rightsCSV, objectType, objectClass, scope := fields[3], fields[4], fields[5], fields[6]

	var rightNames []string
	if rightsCSV != "" {
		rightNames = strings.Split(rightsCSV, ",")
	}
	rights, diags := types.SetValueFrom(ctx, types.StringType, rightNames)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	appliesTo, diags := types.ObjectValueFrom(ctx, appliesToAttrTypes, accessRuleAppliesTo{
		Scope:       types.StringValue(scope),
		ObjectClass: types.StringValue(objectClass),
	})
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("target"), target)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("trustee"), trusteeSID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("trustee_sid"), trusteeSID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("type"), aceType)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("rights"), rights)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("object_type"), objectType)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("applies_to"), appliesTo)...)
}

var (
	_ resource.Resource                     = (*accessRuleResource)(nil)
	_ resource.ResourceWithConfigure        = (*accessRuleResource)(nil)
	_ resource.ResourceWithConfigValidators = (*accessRuleResource)(nil)
	_ resource.ResourceWithImportState      = (*accessRuleResource)(nil)
)
