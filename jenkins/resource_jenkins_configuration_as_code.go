package jenkins

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

type configurationAsCodeResourceModel struct {
	ID      types.String `tfsdk:"id"`
	Section types.String `tfsdk:"section"`
	YAML    types.String `tfsdk:"yaml"`
}

type configurationAsCodeResource struct {
	*resourceHelper
}

var _ resource.ResourceWithConfigure = &configurationAsCodeResource{}
var _ resource.ResourceWithImportState = &configurationAsCodeResource{}

func newConfigurationAsCodeResource() resource.Resource {
	return &configurationAsCodeResource{
		resourceHelper: newResourceHelper(),
	}
}

func (r *configurationAsCodeResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_configuration_as_code"
}

func (r *configurationAsCodeResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Manages one top-level section of the controller configuration through the ` + "`configuration-as-code`" + ` (JCasC) plugin.

Each instance owns a single top-level JCasC section (` + "`jenkins`, `security`, `unclassified`, `tool`, ..." + `) named by ` + "`section`" + `, and applies the ` + "`yaml`" + ` subtree for it. See the [design notes](https://github.com/namecheap/terraform-provider-jenkins/blob/main/docs/design/casc.md) for the full model.

Key behaviours:

- **Merge, not replace.** Applying a section merges the declared keys into the running configuration; keys present on the controller but not in ` + "`yaml`" + ` are left untouched. Drift is detected as a *subset*: only keys you declare are compared.
- **Secrets.** Use JCasC ` + "`${VAR}`" + ` interpolation for secret values; they are resolved by the controller at apply time and never stored in state or compared.
- **Delete is best-effort.** JCasC cannot "unset" configuration, so destroying this resource only stops managing the section — the applied values remain on the controller.

Requires the ` + "`configuration-as-code`" + ` plugin and an account with ` + "`Overall/Administer`" + `.`,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The managed section name (same as `section`).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"section": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The top-level JCasC configuration section this resource manages, e.g. `jenkins`, `security`, `unclassified`, or `tool`. Changing it forces replacement.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
					stringvalidator.RegexMatches(reCASCSection, "must be a bare top-level key (letters, digits, '_' or '-')"),
				},
			},
			"yaml": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The YAML subtree for `section` (the content *under* the section key, not including the key itself). May reference secrets with JCasC `${VAR}` interpolation.",
			},
		},
	}
}

func (r *configurationAsCodeResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	tflog.Debug(ctx, "configurationAsCodeResource.Create")
	var data configurationAsCodeResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !r.apply(ctx, &data, &resp.Diagnostics) {
		return
	}

	data.ID = data.Section
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *configurationAsCodeResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	tflog.Debug(ctx, "configurationAsCodeResource.Read")
	var data configurationAsCodeResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	exported, err := r.client.ExportCASC(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Refresh Resource", "Could not export JCasC configuration.\n\nError: "+err.Error())
		return
	}

	section := data.Section.ValueString()
	inSync, err := cascInSync(data.YAML.ValueString(), exported, section)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Compare Configuration", err.Error())
		return
	}

	if !inSync {
		// The controller no longer matches the declared subtree. Reflect the
		// actual state so a diff (and a reconciling apply) is produced. If the
		// section is gone entirely, drop the resource so it is recreated.
		actual, found, err := extractSectionYAML(exported, section)
		if err != nil {
			resp.Diagnostics.AddError("Unable to Read Section", err.Error())
			return
		}
		if !found {
			resp.State.RemoveResource(ctx)
			return
		}
		data.YAML = types.StringValue(actual)
	}

	data.ID = data.Section
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *configurationAsCodeResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	tflog.Debug(ctx, "configurationAsCodeResource.Update")
	var data configurationAsCodeResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !r.apply(ctx, &data, &resp.Diagnostics) {
		return
	}

	data.ID = data.Section
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// Delete only stops managing the section: JCasC has no operation to remove
// previously-applied configuration, so the values remain on the controller.
func (r *configurationAsCodeResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	tflog.Debug(ctx, "configurationAsCodeResource.Delete (no-op)")
	var data configurationAsCodeResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.AddWarning(
		"Configuration Not Removed",
		"JCasC cannot un-apply configuration, so the '"+data.Section.ValueString()+"' section previously applied by this "+
			"resource remains on the controller. Remove or override it manually if it is no longer wanted.",
	)
}

// ImportState imports by section name, populating yaml from the controller's
// current export of that section so the imported state is self-consistent.
func (r *configurationAsCodeResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	section := req.ID
	exported, err := r.client.ExportCASC(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Import Resource", "Could not export JCasC configuration.\n\nError: "+err.Error())
		return
	}
	actual, found, err := extractSectionYAML(exported, section)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Import Resource", err.Error())
		return
	}
	if !found {
		resp.Diagnostics.AddError("Section Not Found", "The controller has no JCasC section named "+section+".")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("section"), section)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), section)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("yaml"), actual)...)
}

// apply wraps the declared subtree in its section key and POSTs it to JCasC.
// Returns false (with a diagnostic) on failure.
func (r *configurationAsCodeResource) apply(ctx context.Context, data *configurationAsCodeResourceModel, diags *diag.Diagnostics) bool {
	doc, err := wrapSection(data.Section.ValueString(), data.YAML.ValueString())
	if err != nil {
		diags.AddAttributeError(path.Root("yaml"), "Invalid YAML", err.Error())
		return false
	}
	if err := r.client.ApplyCASC(ctx, doc); err != nil {
		diags.AddError("Unable to Apply Configuration", "JCasC rejected the configuration for section "+data.Section.ValueString()+".\n\nError: "+err.Error())
		return false
	}
	return true
}
