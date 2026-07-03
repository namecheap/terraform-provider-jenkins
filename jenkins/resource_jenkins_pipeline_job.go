package jenkins

import (
	"context"
	"encoding/xml"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

const (
	cpsFlowDefinitionClass  = "org.jenkinsci.plugins.workflow.cps.CpsFlowDefinition"
	cpsFlowDefinitionPlugin = "workflow-cps"
	flowDefinitionPlugin    = "workflow-job"
)

// cpsFlowDefinition and flowDefinition model the subset of a pipeline job's
// config.xml this resource manages. encoding/xml handles escaping the (arbitrary
// Groovy) script when marshaling, and ignores the extra elements Jenkins adds
// (properties, triggers) when unmarshaling.
type cpsFlowDefinition struct {
	XMLName xml.Name `xml:"definition"`
	Class   string   `xml:"class,attr"`
	Plugin  string   `xml:"plugin,attr,omitempty"`
	Script  string   `xml:"script"`
	Sandbox bool     `xml:"sandbox"`
}

type flowDefinition struct {
	XMLName     xml.Name          `xml:"flow-definition"`
	Plugin      string            `xml:"plugin,attr,omitempty"`
	Description string            `xml:"description"`
	Definition  cpsFlowDefinition `xml:"definition"`
	Disabled    bool              `xml:"disabled"`
}

type pipelineJobResourceModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Folder      types.String `tfsdk:"folder"`
	Description types.String `tfsdk:"description"`
	Script      types.String `tfsdk:"script"`
	Sandbox     types.Bool   `tfsdk:"sandbox"`
	Disabled    types.Bool   `tfsdk:"disabled"`
}

func (m pipelineJobResourceModel) configXML() (string, error) {
	fd := flowDefinition{
		Plugin:      flowDefinitionPlugin,
		Description: m.Description.ValueString(),
		Definition: cpsFlowDefinition{
			Class:   cpsFlowDefinitionClass,
			Plugin:  cpsFlowDefinitionPlugin,
			Script:  m.Script.ValueString(),
			Sandbox: m.Sandbox.ValueBool(),
		},
		Disabled: m.Disabled.ValueBool(),
	}
	out, err := xml.MarshalIndent(fd, "", "  ")
	if err != nil {
		return "", err
	}
	return xml.Header + string(out), nil
}

type pipelineJobResource struct {
	*resourceHelper
}

var _ resource.ResourceWithConfigure = &pipelineJobResource{}
var _ resource.ResourceWithImportState = &pipelineJobResource{}

func newPipelineJobResource() resource.Resource {
	return &pipelineJobResource{
		resourceHelper: newResourceHelper(),
	}
}

func (r *pipelineJobResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_pipeline_job"
}

func (r *pipelineJobResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `
Manages a pipeline job within Jenkins whose definition is an inline Groovy script, so pipelines can be managed without hand-writing job config XML.`,
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
				MarkdownDescription: "The name of the pipeline job. Changing this forces a new job to be created.",
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
				MarkdownDescription: "The folder namespace to store the job in. If not set, defaults to the global Jenkins root. Changing this forces a new job to be created.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"description": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
				MarkdownDescription: "A human readable description of the pipeline job.",
			},
			"script": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The inline Groovy pipeline script (the contents of a Jenkinsfile).",
			},
			"sandbox": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Whether to run the script in the Groovy sandbox. Defaults to `true`.",
			},
			"disabled": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Whether the job is disabled. Defaults to `false`.",
			},
		},
	}
}

func (r *pipelineJobResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	tflog.Debug(ctx, "pipelineJobResource.Create")
	var data pipelineJobResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := folderExists(ctx, r.client, data.Folder.ValueString()); err != nil {
		resp.Diagnostics.AddError(
			"Invalid Folder",
			"Could not find folder "+data.Folder.ValueString()+".\n\nError: "+err.Error(),
		)
		return
	}

	xmlStr, err := data.configXML()
	if err != nil {
		resp.Diagnostics.AddError("Unable to Render Job Configuration", err.Error())
		return
	}

	if _, err := r.client.CreateJobInFolder(ctx, xmlStr, data.Name.ValueString(), extractFolders(data.Folder.ValueString())...); err != nil {
		resp.Diagnostics.AddError(
			"Unable to Create Resource",
			"An unexpected error occurred while creating the pipeline job.\n\nError: "+err.Error(),
		)
		return
	}

	if found := r.populate(ctx, &data, &resp.Diagnostics); !found && !resp.Diagnostics.HasError() {
		resp.Diagnostics.AddError("Unable to Read Created Resource", "the pipeline job was created but could not be read back")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *pipelineJobResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	tflog.Debug(ctx, "pipelineJobResource.Read")
	var data pipelineJobResourceModel

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

func (r *pipelineJobResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	tflog.Debug(ctx, "pipelineJobResource.Update")
	var data pipelineJobResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	job, err := r.client.GetJob(ctx, data.Name.ValueString(), extractFolders(data.Folder.ValueString())...)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Update Resource", "Could not find pipeline job "+data.Name.ValueString()+".\n\nError: "+err.Error())
		return
	}

	xmlStr, err := data.configXML()
	if err != nil {
		resp.Diagnostics.AddError("Unable to Render Job Configuration", err.Error())
		return
	}

	if err := job.UpdateConfig(ctx, xmlStr); err != nil {
		resp.Diagnostics.AddError("Unable to Update Resource", "An unexpected error occurred while updating the pipeline job.\n\nError: "+err.Error())
		return
	}

	if found := r.populate(ctx, &data, &resp.Diagnostics); !found && !resp.Diagnostics.HasError() {
		resp.Diagnostics.AddError("Unable to Read Updated Resource", "the pipeline job was updated but could not be read back")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *pipelineJobResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	tflog.Debug(ctx, "pipelineJobResource.Delete")
	var data pipelineJobResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if _, err := r.client.DeleteJobInFolder(ctx, data.Name.ValueString(), extractFolders(data.Folder.ValueString())...); err != nil {
		resp.Diagnostics.AddError("Unable to Delete Resource", err.Error())
	}
}

func (r *pipelineJobResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	name, folders := parseCanonicalJobID(req.ID)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("folder"), formatFolderID(folders))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

// populate fetches the job and its config.xml and fills data from them. It
// returns false (without an error diagnostic) when the job no longer exists, so
// Read can drop it from state.
func (r *pipelineJobResource) populate(ctx context.Context, data *pipelineJobResourceModel, diags *diag.Diagnostics) bool {
	job, err := r.client.GetJob(ctx, data.Name.ValueString(), extractFolders(data.Folder.ValueString())...)
	if err != nil {
		if isNotFound(err) {
			return false
		}
		diags.AddError("Unable to Refresh Resource", "Could not read pipeline job "+data.Name.ValueString()+".\n\nError: "+err.Error())
		return false
	}

	config, err := job.GetConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Refresh Resource", "Could not read pipeline job configuration.\n\nError: "+err.Error())
		return false
	}

	var fd flowDefinition
	if err := xml.Unmarshal(stripXMLDeclaration([]byte(config)), &fd); err != nil {
		diags.AddError("Unable to Parse Job Configuration", "Could not parse pipeline job configuration XML.\n\nError: "+err.Error())
		return false
	}

	data.ID = types.StringValue(job.Base)
	data.Description = types.StringValue(fd.Description)
	data.Script = types.StringValue(fd.Definition.Script)
	data.Sandbox = types.BoolValue(fd.Definition.Sandbox)
	data.Disabled = types.BoolValue(fd.Disabled)
	return true
}
