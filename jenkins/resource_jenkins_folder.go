package jenkins

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

const defaultFolderInheritanceStrategy = "org.jenkinsci.plugins.matrixauth.inheritance.InheritParentStrategy"

// folderSecurityObjectType is the element type of the "security" set block. It
// must match the block's nested attributes so the set can be (de)serialized.
var folderSecurityObjectType = types.ObjectType{
	AttrTypes: map[string]attr.Type{
		"inheritance_strategy": types.StringType,
		"permissions":          types.SetType{ElemType: types.StringType},
	},
}

type folderResourceModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Folder      types.String `tfsdk:"folder"`
	DisplayName types.String `tfsdk:"display_name"`
	Description types.String `tfsdk:"description"`
	Security    types.Set    `tfsdk:"security"`
	Template    types.String `tfsdk:"template"`
}

type folderSecurityBlockModel struct {
	InheritanceStrategy types.String `tfsdk:"inheritance_strategy"`
	Permissions         types.Set    `tfsdk:"permissions"`
}

type folderResource struct {
	*resourceHelper
}

var _ resource.ResourceWithConfigure = &folderResource{}
var _ resource.ResourceWithImportState = &folderResource{}

func newFolderResource() resource.Resource {
	return &folderResource{
		resourceHelper: newResourceHelper(),
	}
}

func (r *folderResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_folder"
}

func (r *folderResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a folder within Jenkins, optionally nested inside another folder, with project-based security.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The full canonical folder path, e.g. `/job/folder-name`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The unique name of the JenkinsCI folder.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.RegexMatches(
						regexp.MustCompile(`^[^/]*$`),
						"must not include path characters. Please use the 'folder' property if specifying a folder within a subfolder",
					),
				},
			},
			"folder": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "The folder namespace that the folder will be added to as a subfolder.",
				PlanModifiers: []planmodifier.String{
					folderPlanModifier{},
				},
				Validators: []validator.String{
					folderNameValidator{},
				},
			},
			"display_name": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
				MarkdownDescription: "The name of the folder to display in the UI.",
			},
			"description": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
				MarkdownDescription: "The description of this folder's purpose.",
			},
			"template": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The configuration file template, used to communicate with Jenkins.",
			},
		},
		Blocks: map[string]schema.Block{
			"security": schema.SetNestedBlock{
				MarkdownDescription: "The Jenkins project-based security configuration.",
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"inheritance_strategy": schema.StringAttribute{
							Optional:            true,
							Computed:            true,
							Default:             stringdefault.StaticString(defaultFolderInheritanceStrategy),
							MarkdownDescription: "The strategy for applying these permissions sets to existing inherited permissions.",
						},
						"permissions": schema.SetAttribute{
							Required:            true,
							ElementType:         types.StringType,
							MarkdownDescription: "The Jenkins permissions sets that provide access to this folder.",
						},
					},
				},
			},
		},
	}
}

func (r *folderResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	tflog.Debug(ctx, "folderResource.Create")
	var data folderResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	folderName := data.Folder.ValueString()
	if err := folderExists(ctx, r.client, folderName); err != nil {
		resp.Diagnostics.AddError(
			"Invalid Folder",
			"Could not find folder '"+folderName+"'.\n\nError: "+err.Error(),
		)
		return
	}

	f := folder{
		Description: data.Description.ValueString(),
		DisplayName: data.DisplayName.ValueString(),
	}
	f.Properties.Security = securityFromModel(ctx, data.Security, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	xml, err := f.Render()
	if err != nil {
		resp.Diagnostics.AddError("Unable to Render Folder Configuration", err.Error())
		return
	}

	if _, err := r.client.CreateJobInFolder(ctx, string(xml), data.Name.ValueString(), extractFolders(folderName)...); err != nil {
		resp.Diagnostics.AddError(
			"Unable to Create Resource",
			"An unexpected error occurred while creating the folder "+data.Name.ValueString()+".\n\nError: "+err.Error(),
		)
		return
	}

	if found := r.populate(ctx, &data, f.Properties.Security, &resp.Diagnostics); !found && !resp.Diagnostics.HasError() {
		resp.Diagnostics.AddError("Unable to Read Created Resource", "the folder was created but could not be read back")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *folderResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	tflog.Debug(ctx, "folderResource.Read")
	var data folderResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	wantSecurity := securityFromModel(ctx, data.Security, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	found := r.populate(ctx, &data, wantSecurity, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if !found {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *folderResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	tflog.Debug(ctx, "folderResource.Update")
	var data folderResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	job, err := r.client.GetJob(ctx, data.Name.ValueString(), extractFolders(data.Folder.ValueString())...)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Update Resource", "Could not find folder "+data.Name.ValueString()+".\n\nError: "+err.Error())
		return
	}

	// Read the existing config so unmanaged elements (folderViews, healthMetrics,
	// and any other properties) are preserved across the update.
	config, err := job.GetConfig(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Update Resource", "Could not read folder configuration.\n\nError: "+err.Error())
		return
	}

	f, err := parseFolder(config)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Parse Folder Configuration", err.Error())
		return
	}

	f.Description = data.Description.ValueString()
	f.DisplayName = data.DisplayName.ValueString()
	f.Properties.Security = securityFromModel(ctx, data.Security, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	xml, err := f.Render()
	if err != nil {
		resp.Diagnostics.AddError("Unable to Render Folder Configuration", err.Error())
		return
	}

	if err := job.UpdateConfig(ctx, string(xml)); err != nil {
		resp.Diagnostics.AddError("Unable to Update Resource", "An unexpected error occurred while updating the folder configuration.\n\nError: "+err.Error())
		return
	}

	if found := r.populate(ctx, &data, f.Properties.Security, &resp.Diagnostics); !found && !resp.Diagnostics.HasError() {
		resp.Diagnostics.AddError("Unable to Read Updated Resource", "the folder was updated but could not be read back")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *folderResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	tflog.Debug(ctx, "folderResource.Delete")
	var data folderResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if _, err := r.client.DeleteJobInFolder(ctx, data.Name.ValueString(), extractFolders(data.Folder.ValueString())...); err != nil {
		resp.Diagnostics.AddError("Unable to Delete Resource", err.Error())
	}
}

func (r *folderResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	name, folders := parseCanonicalJobID(req.ID)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
	if folder := formatFolderID(folders); folder != "" {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("folder"), folder)...)
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

// populate fetches the folder and its config.xml and fills data from them. It
// returns false (without an error diagnostic) when the folder no longer exists,
// so Read can drop it from state.
//
// wantSecurity is the security value the caller just configured or rendered
// (nil if none). Jenkins silently omits the AuthorizationMatrixProperty
// element entirely when it would grant zero permissions, rather than
// persisting an empty one. Since "permissions" is a Required attribute, its
// value must round-trip exactly, so when the server reports no security at
// all but wantSecurity asked for a strategy with zero permissions, populate
// reports back wantSecurity instead of losing the block.
func (r *folderResource) populate(ctx context.Context, data *folderResourceModel, wantSecurity *folderSecurity, diags *diag.Diagnostics) bool {
	job, err := r.client.GetJob(ctx, data.Name.ValueString(), extractFolders(data.Folder.ValueString())...)
	if err != nil {
		if isNotFound(err) {
			return false
		}
		diags.AddError("Unable to Refresh Resource", "Could not read folder "+data.Name.ValueString()+".\n\nError: "+err.Error())
		return false
	}

	config, err := job.GetConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Refresh Resource", "Could not read folder configuration.\n\nError: "+err.Error())
		return false
	}

	f, err := parseFolder(config)
	if err != nil {
		diags.AddError("Unable to Parse Folder Configuration", err.Error())
		return false
	}

	actualSecurity := f.Properties.Security
	if actualSecurity == nil && wantSecurity != nil && len(wantSecurity.Permission) == 0 {
		actualSecurity = wantSecurity
	}

	data.ID = types.StringValue(job.Base)
	data.Template = types.StringValue(config)
	data.DisplayName = types.StringValue(f.DisplayName)
	data.Description = types.StringValue(f.Description)
	data.Security = securityToSet(ctx, actualSecurity, diags)
	return !diags.HasError()
}

// securityFromModel converts the "security" set block into the internal
// folderSecurity model used to render config.xml. It returns nil when no
// security block is configured.
func securityFromModel(ctx context.Context, set types.Set, diags *diag.Diagnostics) *folderSecurity {
	if set.IsNull() || set.IsUnknown() || len(set.Elements()) == 0 {
		return nil
	}

	var blocks []folderSecurityBlockModel
	diags.Append(set.ElementsAs(ctx, &blocks, false)...)
	if diags.HasError() || len(blocks) == 0 {
		return nil
	}

	b := blocks[0]
	permissions := []string{}
	diags.Append(b.Permissions.ElementsAs(ctx, &permissions, false)...)
	if diags.HasError() {
		return nil
	}

	// The schema Default normally fills this in, but fall back to the default
	// class so an omitted inheritance_strategy never renders an empty class.
	strategy := b.InheritanceStrategy.ValueString()
	if strategy == "" {
		strategy = defaultFolderInheritanceStrategy
	}

	return &folderSecurity{
		InheritanceStrategy: folderPermissionInheritanceStrategy{Class: strategy},
		Permission:          permissions,
	}
}

// securityToSet converts an internal folderSecurity model into the "security"
// set block. A nil model yields an empty set, matching a folder with no
// project-based security configured.
func securityToSet(ctx context.Context, sec *folderSecurity, diags *diag.Diagnostics) types.Set {
	if sec == nil {
		return types.SetValueMust(folderSecurityObjectType, []attr.Value{})
	}

	// sec.Permission comes straight from Jenkins' config.xml, which the
	// provider does not control: it may be a nil slice (encoding/xml leaves it
	// nil when zero <permission> elements were persisted) or contain repeated
	// entries (e.g. a hand-edited config.xml). Both break SetValueFrom: a nil
	// slice becomes a *null* Set, which Terraform's post-apply consistency
	// check treats as non-correlating with the *empty* Set a "permissions = []"
	// config produces, and duplicate elements fail Set validation with a
	// "Duplicate Set Element" error instead of collapsing. Rebuild the slice
	// deduplicated and non-nil so the round trip always yields a valid,
	// empty-not-null Set.
	perms := make([]string, 0, len(sec.Permission))
	seen := make(map[string]struct{}, len(sec.Permission))
	for _, p := range sec.Permission {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		perms = append(perms, p)
	}
	permissions, d := types.SetValueFrom(ctx, types.StringType, perms)
	diags.Append(d...)

	set, d := types.SetValueFrom(ctx, folderSecurityObjectType, []folderSecurityBlockModel{
		{
			InheritanceStrategy: types.StringValue(sec.InheritanceStrategy.Class),
			Permissions:         permissions,
		},
	})
	diags.Append(d...)
	return set
}
