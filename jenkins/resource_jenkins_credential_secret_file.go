package jenkins

import (
	"context"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

type credentialSecretFileResourceModel struct {
	ID                   types.String `tfsdk:"id"`
	Name                 types.String `tfsdk:"name"`
	Folder               types.String `tfsdk:"folder"`
	Description          types.String `tfsdk:"description"`
	Domain               types.String `tfsdk:"domain"`
	Scope                types.String `tfsdk:"scope"`
	Filename             types.String `tfsdk:"filename"`
	SecretBytes          types.String `tfsdk:"secretbytes"`
	SecretBytesWo        types.String `tfsdk:"secretbytes_wo"`
	SecretBytesWoVersion types.String `tfsdk:"secretbytes_wo_version"`
}

type credentialSecretFileResource struct {
	*resourceHelper
}

// Ensure the implementation satisfies the desired interfaces.
var _ resource.ResourceWithConfigure = &credentialSecretFileResource{}
var _ resource.ResourceWithImportState = &credentialSecretFileResource{}
var _ resource.ResourceWithConfigValidators = &credentialSecretFileResource{}

func newCredentialSecretFileResource() resource.Resource {
	return &credentialSecretFileResource{
		resourceHelper: newResourceHelper(),
	}
}

// Metadata should return the full name of the resource.
func (r *credentialSecretFileResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credential_secret_file"
}

// Schema should return the schema for this resource.
func (r *credentialSecretFileResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `
Manages a secret file credential within Jenkins. This secret file may then be referenced within jobs that are created.`,
		Attributes: r.schemaCredential(addWriteOnlySecret(map[string]schema.Attribute{
			"filename": schema.StringAttribute{
				MarkdownDescription: "The secret file filename on jenkins server side.",
				Required:            true,
			},
			"secretbytes": schema.StringAttribute{
				MarkdownDescription: "The secret file, base64 encoded content. It can be sourced directly from local file with filebase64(path) TF function or given directly.",
				Optional:            true,
				Sensitive:           true,
			},
		}, "secretbytes", "The secret file, base64 encoded content.")),
	}
}

// ConfigValidators enforces the plain/write-only secret constraints.
func (r *credentialSecretFileResource) ConfigValidators(_ context.Context) []resource.ConfigValidator {
	return writeOnlySecretConfigValidators("secretbytes")
}

// Create is called when the provider must create a new resource. Config
// and planned state values should be read from the
// CreateRequest and new state values set on the CreateResponse.
func (r *credentialSecretFileResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	tflog.Debug(ctx, "credentialSecretFileResource.Create")
	var data credentialSecretFileResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cm := r.credentialManagerForFolder(ctx, data.Folder.ValueString(), &resp.Diagnostics)
	if cm == nil {
		return
	}

	secretBytes := data.SecretBytes.ValueString()
	if secretBytesWo := r.readWriteOnly(ctx, req.Config, "secretbytes_wo", &resp.Diagnostics); !secretBytesWo.IsNull() {
		secretBytes = secretBytesWo.ValueString()
	}
	if resp.Diagnostics.HasError() {
		return
	}

	cred := jenkins.FileCredentials{
		ID:          data.Name.ValueString(),
		Scope:       data.Scope.ValueString(),
		Description: data.Description.ValueString(),
		Filename:    data.Filename.ValueString(),
		SecretBytes: secretBytes,
	}

	err := cm.Add(ctx, data.Domain.ValueString(), cred)
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to Create Resource",
			"An unexpected error occurred while creating the resource. "+
				"Please report this issue to the provider developers.\n\n"+
				"Error: "+err.Error(),
		)

		return
	}

	// Convert from the API data model to the Terraform data model
	// and set any unknown attribute values.
	data.ID = types.StringValue(generateCredentialID(data.Folder.ValueString(), cred.ID))

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// Read is called when the provider must read resource values in order
// to update state. Planned state values should be read from the
// ReadRequest and new state values set on the ReadResponse.
func (r *credentialSecretFileResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	tflog.Debug(ctx, "credentialSecretFileResource.Read")
	var data credentialSecretFileResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cm := r.credentialManager(data.Folder.ValueString())

	cred := jenkins.FileCredentials{}
	err := cm.GetSingle(ctx, data.Domain.ValueString(), data.Name.ValueString(), &cred)
	if err != nil {
		if isNotFound(err) {
			// Job does not exist
			resp.State.RemoveResource(ctx)
			return
		}

		resp.Diagnostics.AddError(
			"Unable to Refresh Resource",
			"An unexpected error occurred while parsing the resource read response. "+
				"Please report this issue to the provider developers.\n\n"+
				"Error: "+err.Error(),
		)

		return
	}

	data.ID = types.StringValue(generateCredentialID(data.Folder.ValueString(), cred.ID))
	data.Scope = types.StringValue(cred.Scope)
	data.Description = types.StringValue(cred.Description)
	data.Filename = types.StringValue(cred.Filename)

	// NOTE: We are NOT setting the secret here, as the secret returned by GetSingle is garbage
	// Secret only applies to Create/Update operations if the "secretbytes" property is non-empty

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// Update is called to update the state of the resource. Config, planned
// state, and prior state values should be read from the
// UpdateRequest and new state values set on the UpdateResponse.
func (r *credentialSecretFileResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	tflog.Debug(ctx, "credentialSecretFileResource.Update")
	var data credentialSecretFileResourceModel
	var state credentialSecretFileResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cm := r.credentialManager(data.Folder.ValueString())

	cred := jenkins.FileCredentials{
		ID:          data.Name.ValueString(),
		Scope:       data.Scope.ValueString(),
		Description: data.Description.ValueString(),
		Filename:    data.Filename.ValueString(),
	}

	// Send the secret bytes only when they should change; omitting them leaves the
	// Jenkins-stored value untouched (also correct for lifecycle.ignore_changes).
	// Write-only: re-send when the version trigger changed. Plain: re-send when
	// the value changed.
	if secretBytesWo := r.readWriteOnly(ctx, req.Config, "secretbytes_wo", &resp.Diagnostics); !secretBytesWo.IsNull() {
		if !data.SecretBytesWoVersion.Equal(state.SecretBytesWoVersion) {
			cred.SecretBytes = secretBytesWo.ValueString()
		}
	} else if !data.SecretBytes.Equal(state.SecretBytes) {
		cred.SecretBytes = data.SecretBytes.ValueString()
	}
	if resp.Diagnostics.HasError() {
		return
	}

	err := cm.Update(ctx, data.Domain.ValueString(), data.Name.ValueString(), &cred)
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to Update Resource",
			"An unexpected error occurred while attempting to update the resource. "+
				"Please retry the operation or report this issue to the provider developers.\n\n"+
				"Error: "+err.Error(),
		)

		return
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// Delete is called when the provider must delete the resource. Config
// values may be read from the DeleteRequest.
//
// If execution completes without error, the framework will automatically
// call DeleteResponse.State.RemoveResource(), so it can be omitted
// from provider logic.
func (r *credentialSecretFileResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data credentialSecretFileResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.deleteCredential(ctx, data.Folder.ValueString(), data.Domain.ValueString(), data.Name.ValueString(), &resp.Diagnostics)
}
