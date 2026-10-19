package jenkins

import (
	"context"
	"regexp"

	jenkins "github.com/bndr/gojenkins"
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

// jobTemplateFolderCredentialsKey is the private-state key under which Create and
// Update record that the configured template itself contains a folder
// credentials store (FolderCredentialsProperty). Read only keeps that element
// in state when the key is set; see jobTemplateFromServer.
const jobTemplateFolderCredentialsKey = "template_has_folder_credentials"

type jobResourceModel struct {
	ID       types.String `tfsdk:"id"`
	Name     types.String `tfsdk:"name"`
	Folder   types.String `tfsdk:"folder"`
	Template types.String `tfsdk:"template"`
	Disabled types.Bool   `tfsdk:"disabled"`
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
		MarkdownDescription: "Manages a job (or folder-scoped job) within Jenkins from a raw config.xml template.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The full canonical job path, e.g. `/job/job-name`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The unique name of the JenkinsCI job.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.RegexMatches(
						regexp.MustCompile(`^[^/]*$`),
						"must not include path characters. Please use the 'folder' property if specifying a job within a subfolder",
					),
				},
			},
			"folder": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "The folder namespace to create this job in. Nested folders are separated with `/`, e.g. `parent/child`. Cannot be changed after creation, and every parent folder must already exist.",
				PlanModifiers: []planmodifier.String{
					folderPlanModifier{},
				},
				Validators: []validator.String{
					folderNameValidator{},
				},
			},
			"template": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The Jenkins-compatible XML template describing the job. An existing job's XML can be retrieved by appending `/config.xml` to its URL. Rendered as a Go template with the other resource arguments available as variables; do not include the XML prolog. Well-formedness is validated at plan time, and semantically-equivalent XML (differing only in attribute order, empty-element syntax, whitespace, plugin versions, or the XML declaration) does not produce a diff. When the job is a folder, its credentials store (the `com.cloudbees.hudson.plugins.folder.properties.FolderCredentialsProvider_-FolderCredentialsProperty` element) is left out of the refreshed value unless this template itself contains that element, so encrypted folder credentials are not copied into state. Like any other element missing from the template, a credentials store the template does not contain is still removed from the folder whenever the job is updated; manage folders that hold credentials with `jenkins_folder` instead.",
				Validators: []validator.String{
					jobXMLValidator{},
				},
				PlanModifiers: []planmodifier.String{
					templatePlanModifier{},
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

	folderName := data.Folder.ValueString()
	if err := folderExists(ctx, r.client, folderName); err != nil {
		resp.Diagnostics.AddError(
			"Invalid Folder",
			"Could not find folder '"+folderName+"'.\n\nError: "+err.Error(),
		)
		return
	}

	job, err := r.client.CreateJobInFolder(ctx, data.Template.ValueString(), data.Name.ValueString(), extractFolders(folderName)...)
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to Create Resource",
			"An unexpected error occurred while creating the job "+data.Name.ValueString()+".\n\nError: "+err.Error(),
		)
		return
	}

	// Enforce the explicit disabled state after the template is applied, so it
	// wins over any <disabled> element the template itself carries.
	if !data.Disabled.IsNull() && !data.Disabled.IsUnknown() {
		if err := applyDisabledState(ctx, job, data.Disabled.ValueBool()); err != nil {
			resp.Diagnostics.AddError(
				"Unable to Set Job State",
				"An unexpected error occurred while setting the disabled state on job "+data.Name.ValueString()+".\n\nError: "+err.Error(),
			)
			return
		}
	}

	// Keep the configured template in state (Terraform requires the applied
	// value to match the plan); Read refreshes it to Jenkins' re-serialized
	// config, which the plan modifier reconciles.
	if !r.refreshMeta(ctx, job, &data, &resp.Diagnostics) {
		return
	}

	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, jobTemplateFolderCredentialsKey, folderCredentialsFlag(data.Template.ValueString()))...)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *jobResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	tflog.Debug(ctx, "jobResource.Read")
	var data jobResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	job, err := r.client.GetJob(ctx, data.Name.ValueString(), extractFolders(data.Folder.ValueString())...)
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Unable to Refresh Resource", "Could not read job "+data.Name.ValueString()+".\n\nError: "+err.Error())
		return
	}

	config, err := job.GetConfig(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Refresh Resource", "Could not read job configuration.\n\nError: "+err.Error())
		return
	}
	keep, d := req.Private.GetKey(ctx, jobTemplateFolderCredentialsKey)
	resp.Diagnostics.Append(d...)
	data.Template = types.StringValue(jobTemplateFromServer(config, string(keep) == "true"))

	if !r.refreshMeta(ctx, job, &data, &resp.Diagnostics) {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *jobResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	tflog.Debug(ctx, "jobResource.Update")
	var data jobResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	job, err := r.client.GetJob(ctx, data.Name.ValueString(), extractFolders(data.Folder.ValueString())...)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Update Resource", "Could not find job "+data.Name.ValueString()+".\n\nError: "+err.Error())
		return
	}

	if err := job.UpdateConfig(ctx, data.Template.ValueString()); err != nil {
		resp.Diagnostics.AddError("Unable to Update Resource", "An unexpected error occurred while updating the job configuration.\n\nError: "+err.Error())
		return
	}

	// Apply the explicit disabled state after the template update, so it wins
	// over any <disabled> element the new template carries.
	if !data.Disabled.IsNull() && !data.Disabled.IsUnknown() {
		if err := applyDisabledState(ctx, job, data.Disabled.ValueBool()); err != nil {
			resp.Diagnostics.AddError("Unable to Set Job State", "An unexpected error occurred while setting the disabled state.\n\nError: "+err.Error())
			return
		}
	}

	if !r.refreshMeta(ctx, job, &data, &resp.Diagnostics) {
		return
	}

	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, jobTemplateFolderCredentialsKey, folderCredentialsFlag(data.Template.ValueString()))...)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *jobResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	tflog.Debug(ctx, "jobResource.Delete")
	var data jobResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if _, err := r.client.DeleteJobInFolder(ctx, data.Name.ValueString(), extractFolders(data.Folder.ValueString())...); err != nil {
		resp.Diagnostics.AddError("Unable to Delete Resource", err.Error())
	}
}

func (r *jobResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	name, folders := parseCanonicalJobID(req.ID)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
	// Only set folder when the job is nested; a top-level job leaves folder null
	// (matching a config that omits it) so ImportStateVerify sees no difference.
	if folder := formatFolderID(folders); folder != "" {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("folder"), folder)...)
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

// refreshMeta fills the resource ID and, when the disabled state is managed, the
// live enabled/disabled value. It does not touch the template. It returns false
// (with a diagnostic) on an API error.
func (r *jobResource) refreshMeta(ctx context.Context, job *jenkins.Job, data *jobResourceModel, diags *diag.Diagnostics) bool {
	data.ID = types.StringValue(job.Base)

	// Only reflect the enabled state when the user manages it; otherwise leave
	// "disabled" unset so an out-of-band value never registers as drift.
	if !data.Disabled.IsNull() && !data.Disabled.IsUnknown() {
		enabled, err := job.IsEnabled(ctx)
		if err != nil {
			diags.AddError("Unable to Refresh Resource", "Could not read enabled state for job "+data.Name.ValueString()+".\n\nError: "+err.Error())
			return false
		}
		data.Disabled = types.BoolValue(!enabled)
	}

	return true
}

// jobTemplateFromServer returns the template Read stores for config, the job's
// config.xml as served by Jenkins (#255).
//
// The computed templates of jenkins_folder and the jenkins_folder/jenkins_job
// data sources are fully redacted (redactJenkinsConfig). This one cannot be:
// it is the practitioner's own configuration, and templatePlanModifier keeps
// state only while it stays semantically equal to the configured template. So
// only the folder credentials store is removed, and only when the configured
// template does not contain it (keepFolderCredentials false). Removing an
// element the configuration does not have can only bring state closer to it,
// never open a diff. What Update posts is unchanged: always the configured
// template or a state value semantically equal to it, so a store the template
// lacks is dropped by the next update exactly as before; only its showing up
// as drift at plan time goes away. Ciphertext anywhere else is left alone: a
// configured template that carries an encrypted value (say a password
// parameter default pasted from config.xml) would never again compare equal to
// a redacted state, and every plan would re-apply it.
//
// Whether the configured template contains the element is only known when it
// is applied, so Create and Update record it in private state. An imported
// job, or state written by an earlier provider version, has no record and is
// stripped; if its configuration does contain the element, the next plan shows
// it once and that apply records it.
func jobTemplateFromServer(config string, keepFolderCredentials bool) string {
	if keepFolderCredentials {
		return config
	}
	return stripFolderCredentials(config)
}

// folderCredentialsFlag is the private-state value Create and Update store
// under jobTemplateFolderCredentialsKey for the applied template: JSON true when
// it contains a folder credentials store, otherwise nil, which removes the key.
func folderCredentialsFlag(template string) []byte {
	if hasFolderCredentials(template) {
		return []byte("true")
	}
	return nil
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
