package jenkins

import (
	"context"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
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

type credentialDomainResourceModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Folder      types.String `tfsdk:"folder"`
	Description types.String `tfsdk:"description"`
}

type credentialDomainResource struct {
	*resourceHelper
}

var _ resource.ResourceWithConfigure = &credentialDomainResource{}
var _ resource.ResourceWithImportState = &credentialDomainResource{}

func newCredentialDomainResource() resource.Resource {
	return &credentialDomainResource{
		resourceHelper: newResourceHelper(),
	}
}

func (r *credentialDomainResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credential_domain"
}

func (r *credentialDomainResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `
Manages a credentials domain within a Jenkins credentials store. Domains group credentials so that resources can place them into a named store via their ` + "`domain`" + ` argument.`,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The canonical identifier of the domain, `[<folder>/]<name>`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The name of the domain. Changing this forces a new domain to be created.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.RegexMatches(
						regexp.MustCompile(`^[^/]+$`),
						"must not be empty or include path characters; use the 'folder' property to scope the domain to a folder",
					),
				},
			},
			"folder": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "The folder whose credentials store holds the domain. If not set, the global store is used. Changing this forces a new domain to be created.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"description": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
				MarkdownDescription: "A human readable description of the domain.",
			},
		},
	}
}

func (r *credentialDomainResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	tflog.Debug(ctx, "credentialDomainResource.Create")
	var data credentialDomainResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := folderExists(ctx, r.client, data.Folder.ValueString()); err != nil {
		resp.Diagnostics.AddError("Invalid Folder", "Could not find folder "+data.Folder.ValueString()+".\n\nError: "+err.Error())
		return
	}

	if err := r.client.CreateCredentialDomain(ctx, data.Folder.ValueString(), data.Name.ValueString(), data.Description.ValueString()); err != nil {
		resp.Diagnostics.AddError(
			"Unable to Create Resource",
			"An unexpected error occurred while creating the credential domain.\n\nError: "+err.Error(),
		)
		return
	}

	data.ID = types.StringValue(generateCredentialID(data.Folder.ValueString(), data.Name.ValueString()))

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *credentialDomainResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	tflog.Debug(ctx, "credentialDomainResource.Read")
	var data credentialDomainResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var dom credentialDomainXML
	if err := r.client.GetCredentialDomain(ctx, data.Folder.ValueString(), data.Name.ValueString(), &dom); err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"Unable to Refresh Resource",
			"An unexpected error occurred while reading the credential domain.\n\nError: "+err.Error(),
		)
		return
	}

	data.ID = types.StringValue(generateCredentialID(data.Folder.ValueString(), data.Name.ValueString()))
	data.Description = types.StringValue(dom.Description)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *credentialDomainResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	tflog.Debug(ctx, "credentialDomainResource.Update")
	var data credentialDomainResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.UpdateCredentialDomain(ctx, data.Folder.ValueString(), data.Name.ValueString(), data.Description.ValueString()); err != nil {
		resp.Diagnostics.AddError(
			"Unable to Update Resource",
			"An unexpected error occurred while updating the credential domain.\n\nError: "+err.Error(),
		)
		return
	}

	data.ID = types.StringValue(generateCredentialID(data.Folder.ValueString(), data.Name.ValueString()))

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *credentialDomainResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	tflog.Debug(ctx, "credentialDomainResource.Delete")
	var data credentialDomainResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.DeleteCredentialDomain(ctx, data.Folder.ValueString(), data.Name.ValueString()); err != nil {
		resp.Diagnostics.AddError("Unable to Delete Resource", err.Error())
	}
}

func (r *credentialDomainResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// ID format: "[<folder>/]<name>", with folder using plain "/" separators.
	name := req.ID
	folder := ""
	if idx := strings.LastIndex(req.ID, "/"); idx != -1 {
		folder = req.ID[:idx]
		name = req.ID[idx+1:]
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
	if folder != "" {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("folder"), folder)...)
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
