package jenkins

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

type pluginResourceModel struct {
	ID                 types.String `tfsdk:"id"`
	Name               types.String `tfsdk:"name"`
	Version            types.String `tfsdk:"version"`
	UninstallOnDestroy types.Bool   `tfsdk:"uninstall_on_destroy"`
	Active             types.Bool   `tfsdk:"active"`
	PendingRestart     types.Bool   `tfsdk:"pending_restart"`
}

type pluginResource struct {
	*resourceHelper
}

var _ resource.ResourceWithConfigure = &pluginResource{}
var _ resource.ResourceWithImportState = &pluginResource{}

func newPluginResource() resource.Resource {
	return &pluginResource{
		resourceHelper: newResourceHelper(),
	}
}

func (r *pluginResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_plugin"
}

func (r *pluginResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `
Manages a Jenkins plugin through the update center, so plugin prerequisites can be
declared as code instead of installed by hand.

Installation is idempotent: if the plugin is already present at the requested version
(or at any version, when ` + "`version`" + ` is unset) no install is attempted. Some plugins
require a Jenkins restart before they become active; when that happens ` + "`pending_restart`" + `
is set and a warning is emitted. Dependency plugins that Jenkins installs automatically
are left unmanaged.`,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The short name of the plugin, which is also its unique identifier.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The short name of the plugin to manage (e.g. `git`). Changing this forces a new resource to be created.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"version": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "The plugin version to install (e.g. `5.2.0`). When omitted, the latest available version is installed and this attribute reflects the installed version. Leaving it unset is drift-prone: Jenkins may install or already have a newer version than any previously recorded.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"uninstall_on_destroy": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Whether destroying the resource uninstalls the plugin from Jenkins. Defaults to `false`, which only drops the plugin from Terraform state and leaves it installed.",
			},
			"active": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether the plugin is currently active (loaded) in Jenkins.",
			},
			"pending_restart": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether Jenkins must be restarted before the plugin becomes active. When `true`, a warning diagnostic is emitted.",
			},
		},
	}
}

func (r *pluginResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	tflog.Debug(ctx, "pluginResource.Create")
	var data pluginResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := data.Name.ValueString()
	wantVersion := data.Version.ValueString() // "" when the version is unset

	existing, err := r.client.HasPlugin(ctx, name)
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to Create Resource",
			fmt.Sprintf("An unexpected error occurred while checking for plugin %q.\n\nError: %s", name, err.Error()),
		)
		return
	}

	// Install only when absent, or present at a different pinned version.
	if existing == nil || (wantVersion != "" && existing.Version != wantVersion) {
		if err := r.client.InstallPlugin(ctx, name, wantVersion); err != nil {
			resp.Diagnostics.AddError(
				"Unable to Create Resource",
				fmt.Sprintf("An unexpected error occurred while installing plugin %q.\n\nError: %s", name, err.Error()),
			)
			return
		}
	}

	r.observe(ctx, &data, false, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *pluginResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	tflog.Debug(ctx, "pluginResource.Read")
	var data pluginResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	plugin, err := r.client.HasPlugin(ctx, data.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to Refresh Resource",
			fmt.Sprintf("An unexpected error occurred while reading plugin %q.\n\nError: %s", data.Name.ValueString(), err.Error()),
		)
		return
	}
	if plugin == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	data.ID = data.Name
	// Always reflect the observed version so that drift from a pinned version
	// (for example an out-of-band upgrade) is surfaced as a plan.
	data.Version = types.StringValue(plugin.Version)
	data.Active = types.BoolValue(plugin.Active)
	data.PendingRestart = types.BoolValue(false)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *pluginResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	tflog.Debug(ctx, "pluginResource.Update")
	var plan, state pluginResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// name is RequiresReplace, so only a version change needs an API call;
	// uninstall_on_destroy is state-only.
	if !plan.Version.Equal(state.Version) && plan.Version.ValueString() != "" {
		if err := r.client.InstallPlugin(ctx, plan.Name.ValueString(), plan.Version.ValueString()); err != nil {
			resp.Diagnostics.AddError(
				"Unable to Update Resource",
				fmt.Sprintf("An unexpected error occurred while updating plugin %q.\n\nError: %s", plan.Name.ValueString(), err.Error()),
			)
			return
		}
	}

	r.observe(ctx, &plan, true, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *pluginResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	tflog.Debug(ctx, "pluginResource.Delete")
	var data pluginResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// The default (false) intentionally leaves the plugin installed; only the
	// state entry is removed by the framework.
	if data.UninstallOnDestroy.ValueBool() {
		if err := r.client.UninstallPlugin(ctx, data.Name.ValueString()); err != nil {
			resp.Diagnostics.AddError(
				"Unable to Delete Resource",
				fmt.Sprintf("An unexpected error occurred while uninstalling plugin %q.\n\nError: %s", data.Name.ValueString(), err.Error()),
			)
		}
	}
}

func (r *pluginResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

// observe refreshes the computed attributes from a fresh plugin lookup after a
// create or update. pinned indicates the caller supplied a concrete version that
// must be preserved (the observed version may lag until a restart); when the
// version is unset it is filled from the installed plugin instead.
func (r *pluginResource) observe(ctx context.Context, data *pluginResourceModel, pinned bool, diags *diag.Diagnostics) {
	name := data.Name.ValueString()
	data.ID = types.StringValue(name)

	plugin, err := r.client.HasPlugin(ctx, name)
	if err != nil {
		diags.AddError(
			"Unable to Read Plugin State",
			fmt.Sprintf("An unexpected error occurred while reading plugin %q after installation.\n\nError: %s", name, err.Error()),
		)
		return
	}

	if plugin == nil {
		// The plugin was accepted but is not yet loaded, which typically means a
		// restart is required for it to activate.
		data.Active = types.BoolValue(false)
		data.PendingRestart = types.BoolValue(true)
		if data.Version.IsUnknown() || data.Version.IsNull() {
			data.Version = types.StringValue("")
		}
		diags.AddWarning(
			"Plugin pending restart",
			fmt.Sprintf("Plugin %q was requested but is not yet loaded; Jenkins likely requires a restart before it becomes active.", name),
		)
		return
	}

	data.Active = types.BoolValue(plugin.Active)
	data.PendingRestart = types.BoolValue(false)
	if !pinned || data.Version.IsUnknown() || data.Version.IsNull() {
		data.Version = types.StringValue(plugin.Version)
	}
}
