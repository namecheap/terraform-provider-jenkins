package jenkins

import (
	"context"
	"fmt"
	"regexp"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var (
	reJobName    = regexp.MustCompile(`^[^/]*$`)
	reFolderName = regexp.MustCompile(`^[^\\]*$`)
)

type jobResourceModel struct {
	ID       types.String     `tfsdk:"id"`
	Name     types.String     `tfsdk:"name"`
	Folder   types.String     `tfsdk:"folder"`
	Template jobTemplateValue `tfsdk:"template"`
	Disabled types.Bool       `tfsdk:"disabled"`
}

type jobResource struct {
	*resourceHelper
}

var _ resource.ResourceWithConfigure = &jobResource{}
var _ resource.ResourceWithImportState = &jobResource{}

func newJobResource() resource.Resource {
	return &jobResource{
		resourceHelper: newResourceHelper(),
	}
}

func (r *jobResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_job"
}

func (r *jobResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a job within Jenkins.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The canonical name of the job, used as its unique identifier.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The unique name of the JenkinsCI job. Changing this forces a new resource to be created.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.RegexMatches(reJobName, "provided name includes path characters. Please use the 'folder' property if specifying a job within a subfolder"),
				},
			},
			"folder": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "The folder namespace that the job will be added to. Changing this forces a new resource to be created.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.RegexMatches(reFolderName, "folder path must not contain backslashes; use '/' as the path separator"),
				},
			},
			"template": schema.StringAttribute{
				Required:            true,
				CustomType:          jobTemplateType{},
				MarkdownDescription: "The configuration file template, used to communicate with Jenkins. Semantically-equal XML (differing only in attribute order, empty-element syntax, whitespace, plugin versions, or the XML declaration) does not produce a diff, and the template is validated for well-formedness at plan time.",
				Validators: []validator.String{
					jobXMLValidatorAttr(),
				},
				PlanModifiers: []planmodifier.String{
					jobDisabledTemplatePlanModifier(),
				},
			},
			"disabled": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Whether the job is disabled. When set, the provider enforces this state through the Jenkins enable/disable API and it takes precedence over any `<disabled>` element in the template; an out-of-band toggle is detected as drift. When omitted, the provider does not manage the job's enabled state and the template's own `<disabled>` value, if any, applies.",
			},
		},
	}
}

func (r *jobResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	tflog.Debug(ctx, "jobResource.Create")
	var data jobResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := data.Name.ValueString()
	folderName := data.Folder.ValueString()

	if err := folderExists(ctx, r.client, folderName); err != nil {
		resp.Diagnostics.AddError(
			"Invalid Folder",
			fmt.Sprintf("Could not find folder %q.\n\nError: %s", folderName, err.Error()),
		)
		return
	}

	folders := extractFolders(folderName)
	job, err := r.client.CreateJobInFolder(ctx, data.Template.ValueString(), name, folders...)
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to Create Resource",
			fmt.Sprintf("An unexpected error occurred while creating job %q in folder %q.\n\nError: %s", name, folderName, err.Error()),
		)
		return
	}

	// Enforce the explicit disabled state after the template is applied, so it
	// wins over any <disabled> element the template itself carries.
	if !data.Disabled.IsNull() && !data.Disabled.IsUnknown() {
		if err := applyDisabledState(ctx, job, data.Disabled.ValueBool()); err != nil {
			resp.Diagnostics.AddError(
				"Unable to Create Resource",
				fmt.Sprintf("An unexpected error occurred while setting disabled=%t on job %q.\n\nError: %s", data.Disabled.ValueBool(), name, err.Error()),
			)
			return
		}
	}

	// Keep the template as planned so the applied value matches the plan exactly;
	// semantic equality reconciles it against Jenkins on the next refresh.
	data.ID = types.StringValue(job.Base)
	data.Folder = types.StringValue(formatFolderID(folders))

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *jobResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	tflog.Debug(ctx, "jobResource.Read")
	var data jobResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name, folders := parseCanonicalJobID(data.ID.ValueString())
	job, err := r.client.GetJob(ctx, name, folders...)
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"Unable to Refresh Resource",
			fmt.Sprintf("An unexpected error occurred while reading job %q.\n\nError: %s", name, err.Error()),
		)
		return
	}

	config, err := job.GetConfig(ctx)
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to Refresh Resource",
			fmt.Sprintf("Job %q could not extract configuration.\n\nError: %s", name, err.Error()),
		)
		return
	}

	data.ID = types.StringValue(job.Base)
	data.Name = types.StringValue(name)
	data.Folder = types.StringValue(formatFolderID(folders))
	// Semantic equality keeps the prior value when this is equivalent XML, so a
	// Jenkins reformat does not rewrite state.
	data.Template = newJobTemplateValue(config)

	// Only reflect the enabled state when the user manages it; otherwise leave
	// "disabled" unset so an out-of-band value never registers as drift.
	if !data.Disabled.IsNull() && !data.Disabled.IsUnknown() {
		enabled, err := job.IsEnabled(ctx)
		if err != nil {
			resp.Diagnostics.AddError(
				"Unable to Refresh Resource",
				fmt.Sprintf("Job %q could not read enabled state.\n\nError: %s", name, err.Error()),
			)
			return
		}
		data.Disabled = types.BoolValue(!enabled)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *jobResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	tflog.Debug(ctx, "jobResource.Update")
	var plan, state jobResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name, folders := parseCanonicalJobID(state.ID.ValueString())
	job, err := r.client.GetJob(ctx, name, folders...)
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to Update Resource",
			fmt.Sprintf("Could not find job %q.\n\nError: %s", name, err.Error()),
		)
		return
	}

	if err := job.UpdateConfig(ctx, plan.Template.ValueString()); err != nil {
		resp.Diagnostics.AddError(
			"Unable to Update Resource",
			fmt.Sprintf("An unexpected error occurred while updating job %q configuration.\n\nError: %s", name, err.Error()),
		)
		return
	}

	// Apply the explicit disabled state after the template update, so it wins
	// over any <disabled> element the new template carries.
	if !plan.Disabled.IsNull() && !plan.Disabled.IsUnknown() {
		if err := applyDisabledState(ctx, job, plan.Disabled.ValueBool()); err != nil {
			resp.Diagnostics.AddError(
				"Unable to Update Resource",
				fmt.Sprintf("An unexpected error occurred while setting disabled=%t on job %q.\n\nError: %s", plan.Disabled.ValueBool(), name, err.Error()),
			)
			return
		}
	}

	plan.ID = state.ID
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *jobResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	tflog.Debug(ctx, "jobResource.Delete")
	var data jobResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name, folders := parseCanonicalJobID(data.ID.ValueString())
	if _, err := r.client.DeleteJobInFolder(ctx, name, folders...); err != nil {
		resp.Diagnostics.AddError(
			"Unable to Delete Resource",
			fmt.Sprintf("An unexpected error occurred while deleting job %q.\n\nError: %s", name, err.Error()),
		)
	}
}

func (r *jobResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// applyDisabledState forces job to the desired enabled/disabled state. Enable and
// Disable are idempotent (Jenkins returns 200 even when the job is already in the
// requested state), so the current state need not be read first.
func applyDisabledState(ctx context.Context, job *jenkins.Job, disabled bool) error {
	var err error
	if disabled {
		_, err = job.Disable(ctx)
	} else {
		_, err = job.Enable(ctx)
	}
	return err
}
