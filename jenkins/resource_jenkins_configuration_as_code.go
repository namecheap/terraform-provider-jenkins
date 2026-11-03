package jenkins

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
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
	ID             types.String `tfsdk:"id"`
	Section        types.String `tfsdk:"section"`
	YAML           types.String `tfsdk:"yaml"`
	UnorderedPaths types.Set    `tfsdk:"unordered_paths"`
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
- **Sequences.** Lists compare in order and by length, since JCasC list configurators replace the list. ` + "`unordered_paths`" + ` opts a list into an order-insensitive comparison, for lists the controller exports in an order of its own.
- **Drift.** When a declared key drifts, state keeps only the declared keys: ` + "`${VAR}`" + ` placeholders stay as written and drifted plain values show the controller's value. A value that could carry a resolved secret or unmanaged keys is shown as ` + "`" + cascDriftMarker + "`" + ` instead.
- **Import.** Import records only ` + "`section`" + `; ` + "`yaml`" + ` is not read from the controller, so the first apply after import re-applies the configured document.
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
			"unordered_paths": schema.SetAttribute{
				ElementType: types.StringType,
				Optional:    true,
				MarkdownDescription: "Paths, within `section`, of the lists compared without regard to order: same length, and each declared element matching a distinct controller element. " +
					"A path is mapping keys joined by dots, where `*` stands for any key or any list element, e.g. `authorizationStrategy.roleBased.roles.*.*.permissions`. " +
					"A path must address a list exactly; paths matching no list are silently ignored. Use one `*` for each list element traversed. " +
					"Only use it for lists whose order the controller does not keep: JCasC still applies the list as declared.",
				Validators: []validator.Set{
					setvalidator.ValueStringsAre(stringvalidator.RegexMatches(reCASCPath, "must be keys or '*' joined by '.', without whitespace, '*' standing for a whole segment")),
				},
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

	// A null yaml comes from ImportState, which has no declared document to
	// compare against; the next plan applies the configured one.
	if !data.YAML.IsNull() {
		section := data.Section.ValueString()
		unordered, diags := unorderedPaths(ctx, data.UnorderedPaths)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		inSync, err := cascInSync(data.YAML.ValueString(), exported, section, unordered)
		if err != nil {
			resp.Diagnostics.AddError("Unable to Compare Configuration", err.Error())
			return
		}

		if !inSync {
			// The controller no longer matches the declared subtree. Store a
			// view of the drifted declared keys so a diff (and a reconciling
			// apply) is produced without copying resolved ${...} values or
			// unmanaged keys from the export into state. If the section is gone
			// entirely, drop the resource so it is recreated.
			view, found, err := cascDriftView(data.YAML.ValueString(), exported, section, unordered)
			if err != nil {
				resp.Diagnostics.AddError("Unable to Read Section", err.Error())
				return
			}
			if !found {
				resp.State.RemoveResource(ctx)
				return
			}
			data.YAML = types.StringValue(view)
		}
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

	// Changing comparison policy alone must not reapply the section (and the
	// controller's configured CASC files). Check the live export with the new
	// paths first; real drift and YAML changes still require an apply.
	var prior configurationAsCodeResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if data.Section.Equal(prior.Section) && data.YAML.Equal(prior.YAML) && !data.UnorderedPaths.Equal(prior.UnorderedPaths) {
		unordered, diags := unorderedPaths(ctx, data.UnorderedPaths)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		exported, err := r.client.ExportCASC(ctx)
		if err != nil {
			resp.Diagnostics.AddError("Unable to Refresh Resource", "Could not export JCasC configuration.\n\nError: "+err.Error())
			return
		}
		inSync, err := cascInSync(data.YAML.ValueString(), exported, data.Section.ValueString(), unordered)
		if err != nil {
			resp.Diagnostics.AddError("Unable to Compare Configuration", err.Error())
			return
		}
		if inSync {
			data.ID = data.Section
			resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
			return
		}
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

// ImportState imports by section name after checking the controller has that
// section. yaml is left null: the export holds the values JCasC resolved for
// ${...} placeholders and every unmanaged key, and without a declared document
// there is nothing to restrict it to. The first plan after import therefore
// shows the configured yaml as an in-place update, which re-applies it.
func (r *configurationAsCodeResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	section := req.ID
	exported, err := r.client.ExportCASC(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Import Resource", "Could not export JCasC configuration.\n\nError: "+err.Error())
		return
	}
	_, found, err := exportedSection(exported, section)
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
}

// unorderedPaths converts the unordered_paths attribute into patterns.
func unorderedPaths(ctx context.Context, set types.Set) (cascPaths, diag.Diagnostics) {
	if set.IsNull() || set.IsUnknown() {
		return nil, nil
	}
	var patterns []string
	diags := set.ElementsAs(ctx, &patterns, false)
	return newCASCPaths(patterns), diags
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
