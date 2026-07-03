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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

const (
	multibranchProjectClass = "org.jenkinsci.plugins.workflow.multibranch.WorkflowMultiBranchProject"
	branchSourceListClass   = "jenkins.branch.MultiBranchProject$BranchSourceList"
	gitSCMSourceClass       = "jenkins.plugins.git.GitSCMSource"
	branchStrategyClass     = "jenkins.branch.DefaultBranchPropertyStrategy"
	branchFactoryClass      = "org.jenkinsci.plugins.workflow.multibranch.WorkflowBranchProjectFactory"
)

// The structs below model the subset of a WorkflowMultiBranchProject config.xml
// this resource manages (a single Git branch source with branch discovery).
// XStream owner back-references are deliberately omitted: Jenkins re-establishes
// them when it loads the persisted config. encoding/xml ignores the extra
// elements Jenkins adds (folderViews, icon, orphanedItemStrategy, triggers) on
// read.
type mbBranchDiscoveryTrait struct {
	XMLName xml.Name `xml:"jenkins.plugins.git.traits.BranchDiscoveryTrait"`
}

type mbTraits struct {
	BranchDiscovery mbBranchDiscoveryTrait
}

type mbGitSource struct {
	XMLName       xml.Name `xml:"source"`
	Class         string   `xml:"class,attr"`
	Plugin        string   `xml:"plugin,attr,omitempty"`
	ID            string   `xml:"id"`
	Remote        string   `xml:"remote"`
	CredentialsID string   `xml:"credentialsId"`
	Traits        mbTraits `xml:"traits"`
}

type mbEmptyList struct {
	Class string `xml:"class,attr"`
}

type mbStrategy struct {
	XMLName    xml.Name    `xml:"strategy"`
	Class      string      `xml:"class,attr"`
	Properties mbEmptyList `xml:"properties"`
}

type mbBranchSource struct {
	XMLName  xml.Name    `xml:"jenkins.branch.BranchSource"`
	Source   mbGitSource `xml:"source"`
	Strategy mbStrategy  `xml:"strategy"`
}

type mbSourceData struct {
	BranchSource mbBranchSource `xml:"jenkins.branch.BranchSource"`
}

type mbSources struct {
	XMLName xml.Name     `xml:"sources"`
	Class   string       `xml:"class,attr"`
	Plugin  string       `xml:"plugin,attr,omitempty"`
	Data    mbSourceData `xml:"data"`
}

type mbFactory struct {
	XMLName    xml.Name `xml:"factory"`
	Class      string   `xml:"class,attr"`
	ScriptPath string   `xml:"scriptPath"`
}

type multibranchProject struct {
	XMLName     xml.Name  `xml:"org.jenkinsci.plugins.workflow.multibranch.WorkflowMultiBranchProject"`
	Plugin      string    `xml:"plugin,attr,omitempty"`
	Description string    `xml:"description"`
	Sources     mbSources `xml:"sources"`
	Factory     mbFactory `xml:"factory"`
}

type multibranchPipelineResourceModel struct {
	ID            types.String `tfsdk:"id"`
	Name          types.String `tfsdk:"name"`
	Folder        types.String `tfsdk:"folder"`
	Description   types.String `tfsdk:"description"`
	Remote        types.String `tfsdk:"remote"`
	CredentialsID types.String `tfsdk:"credentials_id"`
	ScriptPath    types.String `tfsdk:"script_path"`
}

func (m multibranchPipelineResourceModel) configXML() (string, error) {
	proj := multibranchProject{
		Plugin:      "workflow-multibranch",
		Description: m.Description.ValueString(),
		Sources: mbSources{
			Class: branchSourceListClass,
			Data: mbSourceData{
				BranchSource: mbBranchSource{
					Source: mbGitSource{
						Class:         gitSCMSourceClass,
						Plugin:        "git",
						ID:            m.Name.ValueString(),
						Remote:        m.Remote.ValueString(),
						CredentialsID: m.CredentialsID.ValueString(),
					},
					Strategy: mbStrategy{
						Class:      branchStrategyClass,
						Properties: mbEmptyList{Class: "empty-list"},
					},
				},
			},
		},
		Factory: mbFactory{
			Class:      branchFactoryClass,
			ScriptPath: m.ScriptPath.ValueString(),
		},
	}
	out, err := xml.MarshalIndent(proj, "", "  ")
	if err != nil {
		return "", err
	}
	return xml.Header + string(out), nil
}

type multibranchPipelineResource struct {
	*resourceHelper
}

var _ resource.ResourceWithConfigure = &multibranchPipelineResource{}
var _ resource.ResourceWithImportState = &multibranchPipelineResource{}

func newMultibranchPipelineResource() resource.Resource {
	return &multibranchPipelineResource{
		resourceHelper: newResourceHelper(),
	}
}

func (r *multibranchPipelineResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_multibranch_pipeline"
}

func (r *multibranchPipelineResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `
Manages a multibranch pipeline project within Jenkins, backed by a Git branch source. Jenkins discovers branches in the repository and runs the ` + "`script_path`" + ` (a Jenkinsfile) found in each.`,
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
				MarkdownDescription: "The name of the multibranch pipeline. Changing this forces a new project to be created.",
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
				MarkdownDescription: "The folder namespace to store the project in. If not set, defaults to the global Jenkins root. Changing this forces a new project to be created.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"description": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
				MarkdownDescription: "A human readable description of the project.",
			},
			"remote": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The Git remote URL to discover branches from (e.g. `https://github.com/org/repo.git`).",
			},
			"credentials_id": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
				MarkdownDescription: "The ID of a Jenkins credential used to access the Git remote. Leave empty for anonymous access.",
			},
			"script_path": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("Jenkinsfile"),
				MarkdownDescription: "The path to the pipeline definition within each branch. Defaults to `Jenkinsfile`.",
			},
		},
	}
}

func (r *multibranchPipelineResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	tflog.Debug(ctx, "multibranchPipelineResource.Create")
	var data multibranchPipelineResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := folderExists(ctx, r.client, data.Folder.ValueString()); err != nil {
		resp.Diagnostics.AddError("Invalid Folder", "Could not find folder "+data.Folder.ValueString()+".\n\nError: "+err.Error())
		return
	}

	xmlStr, err := data.configXML()
	if err != nil {
		resp.Diagnostics.AddError("Unable to Render Project Configuration", err.Error())
		return
	}

	if _, err := r.client.CreateJobInFolder(ctx, xmlStr, data.Name.ValueString(), extractFolders(data.Folder.ValueString())...); err != nil {
		resp.Diagnostics.AddError(
			"Unable to Create Resource",
			"An unexpected error occurred while creating the multibranch pipeline.\n\nError: "+err.Error(),
		)
		return
	}

	if found := r.populate(ctx, &data, &resp.Diagnostics); !found && !resp.Diagnostics.HasError() {
		resp.Diagnostics.AddError("Unable to Read Created Resource", "the multibranch pipeline was created but could not be read back")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *multibranchPipelineResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	tflog.Debug(ctx, "multibranchPipelineResource.Read")
	var data multibranchPipelineResourceModel

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

func (r *multibranchPipelineResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	tflog.Debug(ctx, "multibranchPipelineResource.Update")
	var data multibranchPipelineResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	job, err := r.client.GetJob(ctx, data.Name.ValueString(), extractFolders(data.Folder.ValueString())...)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Update Resource", "Could not find multibranch pipeline "+data.Name.ValueString()+".\n\nError: "+err.Error())
		return
	}

	xmlStr, err := data.configXML()
	if err != nil {
		resp.Diagnostics.AddError("Unable to Render Project Configuration", err.Error())
		return
	}

	if err := job.UpdateConfig(ctx, xmlStr); err != nil {
		resp.Diagnostics.AddError("Unable to Update Resource", "An unexpected error occurred while updating the multibranch pipeline.\n\nError: "+err.Error())
		return
	}

	if found := r.populate(ctx, &data, &resp.Diagnostics); !found && !resp.Diagnostics.HasError() {
		resp.Diagnostics.AddError("Unable to Read Updated Resource", "the multibranch pipeline was updated but could not be read back")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *multibranchPipelineResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	tflog.Debug(ctx, "multibranchPipelineResource.Delete")
	var data multibranchPipelineResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if _, err := r.client.DeleteJobInFolder(ctx, data.Name.ValueString(), extractFolders(data.Folder.ValueString())...); err != nil {
		resp.Diagnostics.AddError("Unable to Delete Resource", err.Error())
	}
}

func (r *multibranchPipelineResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	name, folders := parseCanonicalJobID(req.ID)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
	if folder := formatFolderID(folders); folder != "" {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("folder"), folder)...)
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

// populate fetches the project config.xml and fills data from it. Returns false
// (without an error) when the project no longer exists, so Read can drop it.
func (r *multibranchPipelineResource) populate(ctx context.Context, data *multibranchPipelineResourceModel, diags *diag.Diagnostics) bool {
	job, err := r.client.GetJob(ctx, data.Name.ValueString(), extractFolders(data.Folder.ValueString())...)
	if err != nil {
		if isNotFound(err) {
			return false
		}
		diags.AddError("Unable to Refresh Resource", "Could not read multibranch pipeline "+data.Name.ValueString()+".\n\nError: "+err.Error())
		return false
	}

	config, err := job.GetConfig(ctx)
	if err != nil {
		diags.AddError("Unable to Refresh Resource", "Could not read multibranch pipeline configuration.\n\nError: "+err.Error())
		return false
	}

	var proj multibranchProject
	if err := xml.Unmarshal(stripXMLDeclaration([]byte(config)), &proj); err != nil {
		diags.AddError("Unable to Parse Project Configuration", "Could not parse multibranch pipeline configuration XML.\n\nError: "+err.Error())
		return false
	}

	data.ID = types.StringValue(job.Base)
	data.Description = types.StringValue(proj.Description)
	data.Remote = types.StringValue(proj.Sources.Data.BranchSource.Source.Remote)
	data.CredentialsID = types.StringValue(proj.Sources.Data.BranchSource.Source.CredentialsID)
	data.ScriptPath = types.StringValue(proj.Factory.ScriptPath)
	return true
}
