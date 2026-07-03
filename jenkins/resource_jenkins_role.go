package jenkins

import (
	"context"
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

// roleStrategyRoleResponse models the JSON returned by the role-strategy
// getRole endpoint. A missing role yields an empty object (nil PermissionIDs).
type roleStrategyRoleResponse struct {
	PermissionIDs map[string]bool `json:"permissionIds"`
	Pattern       string          `json:"pattern"`
	Sids          []struct {
		Type string `json:"type"`
		Sid  string `json:"sid"`
	} `json:"sids"`
}

type roleResourceModel struct {
	ID          types.String `tfsdk:"id"`
	Type        types.String `tfsdk:"type"`
	Name        types.String `tfsdk:"name"`
	Pattern     types.String `tfsdk:"pattern"`
	Permissions types.Set    `tfsdk:"permissions"`
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

Requires the Role-Based Authorization Strategy to be the active authorization strategy.

Assignments are **authoritative**: the provider makes the role's set of assigned users/groups exactly match ` + "`assignments`" + `, removing any assignment it does not manage.`,
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
				MarkdownDescription: "The unique name of the role.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"pattern": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "A regular expression matching the item/agent names the role applies to (e.g. `team-a/.*`). Required for `item` and `agent` roles (defaults to `.*`); must be omitted for `global` roles.",
			},
			"permissions": schema.SetAttribute{
				Required:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "The permission IDs granted by the role, e.g. `hudson.model.Item.Build`.",
				Validators: []validator.Set{
					setvalidator.SizeAtLeast(1),
				},
			},
			"assignments": schema.SetAttribute{
				Optional:            true,
				Computed:            true,
				ElementType:         types.StringType,
				Default:             setdefault.StaticValue(types.SetValueMust(types.StringType, []attr.Value{})),
				MarkdownDescription: "The user or group SIDs the role is assigned to. Authoritative: unmanaged assignments are removed. Omit or set to `[]` for a role with no assignments.",
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
	assignments := setStrings(ctx, data.Assignments, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.AddRole(ctx, apiType, data.Name.ValueString(), permissions, pattern, false); err != nil {
		resp.Diagnostics.AddError("Unable to Create Resource", "An unexpected error occurred while creating the role.\n\nError: "+err.Error())
		return
	}

	if !r.assign(ctx, apiType, data.Name.ValueString(), assignments, &resp.Diagnostics) {
		return
	}

	if found := r.populate(ctx, &data, &resp.Diagnostics); !found && !resp.Diagnostics.HasError() {
		resp.Diagnostics.AddError("Unable to Read Created Resource", "the role was created but could not be read back")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
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
	assignments := setStrings(ctx, data.Assignments, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	// Overwriting the role replaces its permissions/pattern and drops all of its
	// assignments; re-assigning the desired SIDs afterwards makes the assignment
	// set authoritative.
	if err := r.client.AddRole(ctx, apiType, data.Name.ValueString(), permissions, pattern, true); err != nil {
		resp.Diagnostics.AddError("Unable to Update Resource", "An unexpected error occurred while updating the role.\n\nError: "+err.Error())
		return
	}

	if !r.assign(ctx, apiType, data.Name.ValueString(), assignments, &resp.Diagnostics) {
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

// assign assigns the role to each SID, returning false (with a diagnostic) on error.
func (r *roleResource) assign(ctx context.Context, apiType, name string, sids []string, diags *diag.Diagnostics) bool {
	for _, sid := range sids {
		if err := r.client.AssignRole(ctx, apiType, name, sid); err != nil {
			diags.AddError("Unable to Assign Role", "Could not assign role to "+sid+".\n\nError: "+err.Error())
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
	if len(role.PermissionIDs) == 0 {
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

	sids := make([]string, 0, len(role.Sids))
	for _, s := range role.Sids {
		sids = append(sids, s.Sid)
	}
	sidSet, d := types.SetValueFrom(ctx, types.StringType, sids)
	diags.Append(d...)
	data.Assignments = sidSet

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
