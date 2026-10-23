package jenkins

import (
	"context"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// roleTypeAPI maps the user-facing role type to the role-strategy REST type.
var roleTypeAPI = map[string]string{
	"global": "globalRoles",
	"item":   "projectRoles",
	"agent":  "slaveRoles",
}

// Role entry types, as role-strategy names its AuthorizationType values.
// EITHER is the legacy ambiguous entry, matching a user or a group of that
// name.
const (
	roleEntryUser   = "USER"
	roleEntryGroup  = "GROUP"
	roleEntryEither = "EITHER"
)

// roleEntry is one assignment of a role: a SID and its entry type.
type roleEntry struct {
	Type string `json:"type"`
	Sid  string `json:"sid"`
}

// roleStrategyRoleResponse models the JSON returned by the role-strategy
// getRole endpoint. A missing role yields an empty object (nil PermissionIDs);
// a role with no permissions yields an empty, non-nil map.
type roleStrategyRoleResponse struct {
	PermissionIDs map[string]bool `json:"permissionIds"`
	Pattern       string          `json:"pattern"`
	Sids          []roleEntry     `json:"sids"`
}

type roleResourceModel struct {
	ID          types.String `tfsdk:"id"`
	Type        types.String `tfsdk:"type"`
	Name        types.String `tfsdk:"name"`
	Pattern     types.String `tfsdk:"pattern"`
	Permissions types.Set    `tfsdk:"permissions"`
	Users       types.Set    `tfsdk:"users"`
	Groups      types.Set    `tfsdk:"groups"`
	Assignments types.Set    `tfsdk:"assignments"`
}

type roleResource struct {
	*resourceHelper
}

var _ resource.ResourceWithConfigure = &roleResource{}
var _ resource.ResourceWithImportState = &roleResource{}

func newRoleResource() resource.Resource {
	return &roleResource{
		resourceHelper: newResourceHelper(),
	}
}

func (r *roleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_role"
}

func (r *roleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Manages a role in the Jenkins Role-Based Authorization Strategy (the ` + "`role-strategy`" + ` plugin).

Assignments are **authoritative**: the provider makes the role's entries exactly match ` + "`users`" + `, ` + "`groups`" + ` and ` + "`assignments`" + `, removing any entry it does not manage. Creating a role that already exists fails; import it instead.

**Limitations**

- **The Role-Based Authorization Strategy must already be active.** This resource does not switch it on, and every operation fails with a 404 from the role-strategy API until it is. Switch it on once, from *Manage Jenkins > Security* or with Configuration as Code; it then stays active across restarts.
- **Configuration as Code deletes these roles.** An apply that contains ` + "`jenkins.authorizationStrategy.roleBased`" + ` replaces every role with the ones it lists, including the roles managed here. Do not declare ` + "`authorizationStrategy`" + ` in a ` + "`jenkins_configuration_as_code`" + ` resource used alongside this one.
- **The controller's own Configuration as Code files count too.** Every ` + "`jenkins_configuration_as_code`" + ` apply also re-applies the controller's own files: ` + "`CASC_JENKINS_CONFIG`" + `, the path set in *Manage Jenkins > Configuration as Code*, or ` + "`$JENKINS_HOME/jenkins.yaml`" + `. If one of them declares ` + "`authorizationStrategy`" + `, applying any section, even an unrelated one, deletes these roles.`,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The canonical role identifier, `<type>/<name>`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"type": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The role type: `global`, `item`, or `agent`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.OneOf("global", "item", "agent"),
				},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The unique name of the role. Must not contain a comma.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					// role-strategy's removeRoles splits role names on ",", so
					// deleting "team,admin" would delete the role "admin".
					stringvalidator.RegexMatches(regexp.MustCompile(`^[^,]*$`), "must not contain a comma "+
						"(role-strategy cannot delete such a role without deleting other roles; to stop managing one, "+
						"replace its resource block with a removed block that sets lifecycle { destroy = false }, "+
						"or run terraform state rm, then delete the role in Jenkins by hand)"),
				},
			},
			"pattern": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "A regular expression matching the item/agent names the role applies to (e.g. `team-a/.*`). Required for `item` and `agent` roles (defaults to `.*`); must be omitted for `global` roles. Must not be empty. When omitted, an `item` or `agent` role is planned with `.*` even if the role on the controller has another pattern, so set it explicitly on an imported role to keep a narrower one.",
				PlanModifiers: []planmodifier.String{
					rolePatternPlanModifier{},
				},
				Validators: []validator.String{
					// role-strategy stores an empty pattern as ".*" and a global
					// role as no pattern, so "" never reads back as configured.
					stringvalidator.LengthAtLeast(1),
				},
			},
			"permissions": schema.SetAttribute{
				Required:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "The permission IDs granted by the role, e.g. `hudson.model.Item.Build`.",
				Validators: []validator.Set{
					// addRole receives the IDs joined with ",".
					setvalidator.ValueStringsAre(stringvalidator.RegexMatches(regexp.MustCompile(`^[^,]*$`), "must not contain a comma")),
					setvalidator.SizeAtLeast(1),
				},
			},
			"users": schema.SetAttribute{
				Optional:            true,
				Computed:            true,
				ElementType:         types.StringType,
				Default:             setdefault.StaticValue(types.SetValueMust(types.StringType, []attr.Value{})),
				MarkdownDescription: "The user IDs the role is assigned to, as User entries. Authoritative: unmanaged User entries are removed.",
			},
			"groups": schema.SetAttribute{
				Optional:            true,
				Computed:            true,
				ElementType:         types.StringType,
				Default:             setdefault.StaticValue(types.SetValueMust(types.StringType, []attr.Value{})),
				MarkdownDescription: "The group names the role is assigned to, as Group entries (e.g. `authenticated`, or a group of the security realm). Authoritative: unmanaged Group entries are removed.",
			},
			"assignments": schema.SetAttribute{
				Optional:            true,
				Computed:            true,
				ElementType:         types.StringType,
				Default:             setdefault.StaticValue(types.SetValueMust(types.StringType, []attr.Value{})),
				DeprecationMessage:  "Use users and groups. assignments creates ambiguous User/Group entries, which role-strategy flags for migration.",
				MarkdownDescription: "The SIDs the role is assigned to as ambiguous User/Group entries, which match a user or a group of that name. Authoritative: unmanaged ambiguous entries are removed. Prefer `users` and `groups`.",
			},
		},
	}
}

func (r *roleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	tflog.Debug(ctx, "roleResource.Create")
	var data roleResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	apiType, pattern, ok := r.validate(&data, &resp.Diagnostics)
	if !ok {
		return
	}

	permissions := setStrings(ctx, data.Permissions, &resp.Diagnostics)
	entries := roleEntries(ctx, &data, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	// addRole without overwrite is a silent no-op on an existing role, and
	// reconcile would then strip that role's entries under a plan that only
	// showed a create: refuse instead, the role has to be imported.
	var existing roleStrategyRoleResponse
	if err := r.client.GetRole(ctx, apiType, data.Name.ValueString(), &existing); err != nil {
		resp.Diagnostics.AddError("Unable to Create Resource", "Could not check whether role "+data.Name.ValueString()+" exists.\n\nError: "+err.Error())
		return
	}
	if existing.PermissionIDs != nil {
		resp.Diagnostics.AddError("Role Already Exists",
			"The "+data.Type.ValueString()+" role "+data.Name.ValueString()+" already exists. Import it with the identifier \""+data.Type.ValueString()+"/"+data.Name.ValueString()+"\" to manage it.")
		return
	}

	if err := r.client.AddRole(ctx, apiType, data.Name.ValueString(), permissions, pattern, false); err != nil {
		resp.Diagnostics.AddError("Unable to Create Resource", "An unexpected error occurred while creating the role.\n\nError: "+err.Error())
		return
	}

	if !r.reconcile(ctx, apiType, data.Name.ValueString(), entries, &resp.Diagnostics) {
		r.rollback(ctx, apiType, data.Name.ValueString(), &resp.Diagnostics)
		return
	}

	planned := data
	found := r.populate(ctx, &data, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		// The role was created and its entries reconciled; only reading it
		// back failed, possibly for a passing reason. Deleting it would drop
		// the access just granted, so it is saved as planned instead, and
		// Terraform taints it: the next plan shows the replacement, which
		// terraform untaint cancels.
		planned.ID = types.StringValue(planned.Type.ValueString() + "/" + planned.Name.ValueString())
		resp.Diagnostics.Append(resp.State.Set(ctx, &planned)...)
		return
	}
	if !found {
		resp.Diagnostics.AddError("Unable to Read Created Resource", "the role was created but could not be read back")
		r.rollback(ctx, apiType, data.Name.ValueString(), &resp.Diagnostics)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// rollback deletes the role Create just made after a later step failed, so
// the role is not left on the controller outside the state, where a retry
// would stop at the existence check. The failure is already in diags; a
// second diagnostic is added only when the delete fails too.
func (r *roleResource) rollback(ctx context.Context, apiType, name string, diags *diag.Diagnostics) {
	if err := r.client.RemoveRole(ctx, apiType, name); err != nil {
		diags.AddError("Role Left Orphaned After Failed Create",
			"Creating role "+name+" failed, and the automatic rollback that deletes it also failed, so the role still exists in Jenkins but is not tracked in Terraform state.\n\n"+
				"Delete or import the role before re-applying; otherwise the next apply fails because the role already exists.\n\n"+
				"Rollback error: "+err.Error())
	}
}

func (r *roleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	tflog.Debug(ctx, "roleResource.Read")
	var data roleResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	found := r.populate(ctx, &data, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if !found {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *roleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	tflog.Debug(ctx, "roleResource.Update")
	var data, prior roleResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}

	apiType, pattern, ok := r.validate(&data, &resp.Diagnostics)
	if !ok {
		return
	}

	permissions := setStrings(ctx, data.Permissions, &resp.Diagnostics)
	entries := roleEntries(ctx, &data, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	// An overwrite drops every entry of the role on role-strategy releases
	// before 918, so a change to the entries alone leaves the role in place.
	if !data.Permissions.Equal(prior.Permissions) || pattern != prior.Pattern.ValueString() {
		if err := r.client.AddRole(ctx, apiType, data.Name.ValueString(), permissions, pattern, true); err != nil {
			resp.Diagnostics.AddError("Unable to Update Resource", "An unexpected error occurred while updating the role.\n\nError: "+err.Error())
			return
		}
	}

	if !r.reconcile(ctx, apiType, data.Name.ValueString(), entries, &resp.Diagnostics) {
		return
	}

	if found := r.populate(ctx, &data, &resp.Diagnostics); !found && !resp.Diagnostics.HasError() {
		resp.Diagnostics.AddError("Unable to Read Updated Resource", "the role was updated but could not be read back")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *roleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	tflog.Debug(ctx, "roleResource.Delete")
	var data roleResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	apiType, ok := roleTypeAPI[data.Type.ValueString()]
	if !ok {
		resp.Diagnostics.AddError("Invalid Role Type", "Unknown role type "+data.Type.ValueString())
		return
	}

	if err := r.client.RemoveRole(ctx, apiType, data.Name.ValueString()); err != nil {
		resp.Diagnostics.AddError("Unable to Delete Resource", err.Error())
	}
}

func (r *roleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	idx := strings.Index(req.ID, "/")
	if idx < 1 || idx == len(req.ID)-1 {
		resp.Diagnostics.AddError(
			"Unexpected Import Identifier",
			`Expected import identifier with format: "<type>/<name>" (e.g. "item/team-a-developer"). Got: `+req.ID,
		)
		return
	}
	if err := checkRoleName(req.ID[idx+1:]); err != nil {
		resp.Diagnostics.AddError("Unable to Import Role",
			err.Error()+", so this provider could not delete the role without deleting other roles and does not import it.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("type"), req.ID[:idx])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), req.ID[idx+1:])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

// validate resolves the role-strategy API type and effective pattern, enforcing
// that a pattern is set for item/agent roles and absent for global roles. It
// returns ok=false (with a diagnostic) on a violation.
func (r *roleResource) validate(data *roleResourceModel, diags *diag.Diagnostics) (apiType, pattern string, ok bool) {
	apiType, exists := roleTypeAPI[data.Type.ValueString()]
	if !exists {
		diags.AddError("Invalid Role Type", "Unknown role type "+data.Type.ValueString())
		return "", "", false
	}

	patternSet := !data.Pattern.IsNull() && !data.Pattern.IsUnknown() && data.Pattern.ValueString() != ""
	if data.Type.ValueString() == "global" {
		if patternSet {
			diags.AddAttributeError(path.Root("pattern"), "Invalid Attribute", "pattern must not be set for global roles")
			return "", "", false
		}
		return apiType, "", true
	}

	if patternSet {
		pattern = data.Pattern.ValueString()
	}
	return apiType, pattern, true
}

// roleEntrySet pairs a role entry type with the attribute listing its SIDs.
type roleEntrySet struct {
	typ string
	set *types.Set
}

func (m *roleResourceModel) entrySets() []roleEntrySet {
	return []roleEntrySet{
		{roleEntryUser, &m.Users},
		{roleEntryGroup, &m.Groups},
		{roleEntryEither, &m.Assignments},
	}
}

// roleEntries collects the entries data asks for, typed by the attribute that
// lists them.
func roleEntries(ctx context.Context, data *roleResourceModel, diags *diag.Diagnostics) []roleEntry {
	var entries []roleEntry
	for _, src := range data.entrySets() {
		for _, sid := range setStrings(ctx, *src.set, diags) {
			entries = append(entries, roleEntry{Type: src.typ, Sid: sid})
		}
	}
	return entries
}

// reconcile makes the role's entries exactly match want: it reads the entries
// the role holds, assigns the missing ones, then unassigns those not wanted.
// Assigning first means swapping an entry's type (an ambiguous entry for a
// User entry of the same SID) never leaves the SID without the role. It reads
// rather than assuming an empty role because addRole with overwrite keeps the
// existing entries on role-strategy 918.v91e5468d8db_2 and later, and drops
// them on older releases. It returns false (with a diagnostic) on error.
func (r *roleResource) reconcile(ctx context.Context, apiType, name string, want []roleEntry, diags *diag.Diagnostics) bool {
	var role roleStrategyRoleResponse
	if err := r.client.GetRole(ctx, apiType, name, &role); err != nil {
		diags.AddError("Unable to Read Role Assignments", "Could not read role "+name+".\n\nError: "+err.Error())
		return false
	}

	held := make(map[roleEntry]bool, len(role.Sids))
	for _, e := range role.Sids {
		held[e] = true
	}
	wanted := make(map[roleEntry]bool, len(want))
	for _, e := range want {
		wanted[e] = true
		if held[e] {
			continue
		}
		if err := r.client.AssignRole(ctx, apiType, name, e); err != nil {
			diags.AddError("Unable to Assign Role", "Could not assign "+e.Type+" entry "+e.Sid+".\n\nError: "+err.Error())
			return false
		}
	}
	for _, e := range role.Sids {
		if wanted[e] {
			continue
		}
		if err := r.client.UnassignRole(ctx, apiType, name, e); err != nil {
			diags.AddError("Unable to Unassign Role", "Could not unassign "+e.Type+" entry "+e.Sid+".\n\nError: "+err.Error())
			return false
		}
	}
	return true
}

// populate reads the role back from Jenkins and fills data. It returns false
// (without an error diagnostic) when the role no longer exists.
func (r *roleResource) populate(ctx context.Context, data *roleResourceModel, diags *diag.Diagnostics) bool {
	apiType, ok := roleTypeAPI[data.Type.ValueString()]
	if !ok {
		diags.AddError("Invalid Role Type", "Unknown role type "+data.Type.ValueString())
		return false
	}

	var role roleStrategyRoleResponse
	if err := r.client.GetRole(ctx, apiType, data.Name.ValueString(), &role); err != nil {
		diags.AddError("Unable to Refresh Resource", "Could not read role "+data.Name.ValueString()+".\n\nError: "+err.Error())
		return false
	}
	if role.PermissionIDs == nil {
		return false
	}

	data.ID = types.StringValue(data.Type.ValueString() + "/" + data.Name.ValueString())

	permissions := make([]string, 0, len(role.PermissionIDs))
	for id := range role.PermissionIDs {
		permissions = append(permissions, id)
	}
	permSet, d := types.SetValueFrom(ctx, types.StringType, permissions)
	diags.Append(d...)
	data.Permissions = permSet

	if data.Type.ValueString() == "global" {
		data.Pattern = types.StringNull()
	} else {
		data.Pattern = types.StringValue(role.Pattern)
	}

	byType := map[string][]string{}
	for _, e := range role.Sids {
		byType[e.Type] = append(byType[e.Type], e.Sid)
	}
	for _, dst := range data.entrySets() {
		// Copy into a non-nil slice: a nil one would read back as a null set.
		set, d := types.SetValueFrom(ctx, types.StringType, append([]string{}, byType[dst.typ]...))
		diags.Append(d...)
		*dst.set = set
	}

	return !diags.HasError()
}

// setStrings converts a string set into a slice, treating null/unknown as empty.
func setStrings(ctx context.Context, s types.Set, diags *diag.Diagnostics) []string {
	if s.IsNull() || s.IsUnknown() {
		return nil
	}
	var out []string
	diags.Append(s.ElementsAs(ctx, &out, false)...)
	return out
}

// rolePatternPlanModifier plans the pattern role-strategy stores when none is
// configured: ".*" for an item or agent role, none for a global role. A known
// plan keeps Update from overwriting the role for a pattern that did not
// change, and shows the real value instead of "known after apply".
type rolePatternPlanModifier struct{}

func (m rolePatternPlanModifier) Description(context.Context) string {
	return "Plans .* for an item or agent role without a pattern, and no pattern for a global role."
}

func (m rolePatternPlanModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (m rolePatternPlanModifier) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if !req.ConfigValue.IsNull() {
		return
	}
	var roleType types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("type"), &roleType)...)
	if roleType.IsUnknown() || roleType.IsNull() {
		return
	}
	if roleType.ValueString() == "global" {
		resp.PlanValue = types.StringNull()
		return
	}
	resp.PlanValue = types.StringValue(".*")
}
