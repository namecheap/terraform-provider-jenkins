package jenkins

import (
	"context"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// jobXMLType is a custom string type whose values compare by semantic XML
// equality (via normalizeJobXML). It is the framework equivalent of the SDKv2
// templateDiff DiffSuppressFunc: because Jenkins rewrites the config XML it
// stores (declaration, plugin versions, whitespace, entity encoding), the stored
// value differs textually from the user's config. StringSemanticEquals lets the
// provider persist the Jenkins-canonical XML without a spurious diff and without
// tripping the "inconsistent result after apply" check.
type jobXMLType struct {
	basetypes.StringType
}

var _ basetypes.StringTypable = jobXMLType{}

func (t jobXMLType) Equal(o attr.Type) bool {
	other, ok := o.(jobXMLType)
	if !ok {
		return false
	}
	return t.StringType.Equal(other.StringType)
}

func (t jobXMLType) String() string { return "jobXMLType" }

func (t jobXMLType) ValueFromString(_ context.Context, in basetypes.StringValue) (basetypes.StringValuable, diag.Diagnostics) {
	return jobXMLValue{StringValue: in}, nil
}

func (t jobXMLType) ValueFromTerraform(ctx context.Context, in tftypes.Value) (attr.Value, error) {
	attrValue, err := t.StringType.ValueFromTerraform(ctx, in)
	if err != nil {
		return nil, err
	}
	stringValue, ok := attrValue.(basetypes.StringValue)
	if !ok {
		return nil, fmt.Errorf("unexpected value type %T", attrValue)
	}
	return jobXMLValue{StringValue: stringValue}, nil
}

func (t jobXMLType) ValueType(_ context.Context) attr.Value { return jobXMLValue{} }

type jobXMLValue struct {
	basetypes.StringValue
}

var _ basetypes.StringValuableWithSemanticEquals = jobXMLValue{}

func (v jobXMLValue) Type(_ context.Context) attr.Type { return jobXMLType{} }

func (v jobXMLValue) Equal(o attr.Value) bool {
	other, ok := o.(jobXMLValue)
	if !ok {
		return false
	}
	return v.StringValue.Equal(other.StringValue)
}

func (v jobXMLValue) StringSemanticEquals(_ context.Context, newValuable basetypes.StringValuable) (bool, diag.Diagnostics) {
	newValue, ok := newValuable.(jobXMLValue)
	if !ok {
		return false, nil
	}
	return normalizeJobXML(v.ValueString()) == normalizeJobXML(newValue.ValueString()), nil
}

func newJobXMLValue(s string) jobXMLValue {
	return jobXMLValue{StringValue: basetypes.NewStringValue(s)}
}

type jobResourceModel struct {
	ID       types.String `tfsdk:"id"`
	Name     types.String `tfsdk:"name"`
	Folder   types.String `tfsdk:"folder"`
	Template jobXMLValue  `tfsdk:"template"`
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
		MarkdownDescription: "Manages a job (or pipeline) within Jenkins from its raw config XML.",
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
				MarkdownDescription: "The unique name of the JenkinsCI job. Changing this forces a new job to be created.",
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
				MarkdownDescription: "The folder namespace that the job will be added to. Changing this forces a new job to be created.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
					stringplanmodifier.UseStateForUnknown(),
				},
				Validators: []validator.String{
					stringvalidator.RegexMatches(
						regexp.MustCompile(`^[^\\]*$`),
						"folder path must not contain backslashes; use '/' as the path separator",
					),
				},
			},
			"template": schema.StringAttribute{
				CustomType:          jobXMLType{},
				Required:            true,
				MarkdownDescription: "The configuration file template, used to communicate with Jenkins.",
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

	if err := folderExists(ctx, r.client, data.Folder.ValueString()); err != nil {
		resp.Diagnostics.AddError("Invalid Folder", "Could not find folder "+data.Folder.ValueString()+".\n\nError: "+err.Error())
		return
	}

	folders := extractFolders(data.Folder.ValueString())
	if _, err := r.client.CreateJobInFolder(ctx, data.Template.ValueString(), data.Name.ValueString(), folders...); err != nil {
		resp.Diagnostics.AddError(
			"Unable to Create Resource",
			"An unexpected error occurred while creating the job.\n\nError: "+err.Error(),
		)
		return
	}

	if err := r.refresh(ctx, &data, folders); err != nil {
		resp.Diagnostics.AddError("Unable to Read Created Resource", err.Error())
		return
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

	if err := r.refresh(ctx, &data, extractFolders(data.Folder.ValueString())); err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Unable to Refresh Resource", err.Error())
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

	folders := extractFolders(data.Folder.ValueString())
	job, err := r.client.GetJob(ctx, data.Name.ValueString(), folders...)
	if err != nil {
		resp.Diagnostics.AddError("Unable to Update Resource", "Could not find job "+data.Name.ValueString()+".\n\nError: "+err.Error())
		return
	}

	if err := job.UpdateConfig(ctx, data.Template.ValueString()); err != nil {
		resp.Diagnostics.AddError("Unable to Update Resource", "An unexpected error occurred while updating the job.\n\nError: "+err.Error())
		return
	}

	if err := r.refresh(ctx, &data, folders); err != nil {
		resp.Diagnostics.AddError("Unable to Read Updated Resource", err.Error())
		return
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
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("folder"), formatFolderID(folders))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

// refresh populates data (id, folder, template) from Jenkins. template is set to
// the Jenkins-canonical XML; the jobXMLType semantic equality reconciles it with
// the user's configured value without a diff. Returns the underlying error (use
// isNotFound) so Read can distinguish a deleted job.
func (r *jobResource) refresh(ctx context.Context, data *jobResourceModel, folders []string) error {
	job, err := r.client.GetJob(ctx, data.Name.ValueString(), folders...)
	if err != nil {
		return err
	}

	config, err := job.GetConfig(ctx)
	if err != nil {
		return err
	}

	data.ID = types.StringValue(job.Base)
	data.Folder = types.StringValue(formatFolderID(folders))
	data.Template = newJobXMLValue(config)
	return nil
}
