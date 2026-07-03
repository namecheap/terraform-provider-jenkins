package jenkins

import (
	"context"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

type credentialUsernameResourceModel struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	Folder            types.String `tfsdk:"folder"`
	Description       types.String `tfsdk:"description"`
	Domain            types.String `tfsdk:"domain"`
	Scope             types.String `tfsdk:"scope"`
	Username          types.String `tfsdk:"username"`
	Password          types.String `tfsdk:"password"`
	PasswordWo        types.String `tfsdk:"password_wo"`
	PasswordWoVersion types.String `tfsdk:"password_wo_version"`
}

type credentialUsernameResource struct {
	*resourceHelper
}

// Ensure the implementation satisfies the desired interfaces.
var _ resource.ResourceWithConfigure = &credentialUsernameResource{}
var _ resource.ResourceWithImportState = &credentialUsernameResource{}
var _ resource.ResourceWithConfigValidators = &credentialUsernameResource{}

func newCredentialUsernameResource() resource.Resource {
	return &credentialUsernameResource{
		resourceHelper: newResourceHelper(),
	}
}

// Metadata should return the full name of the resource.
func (r *credentialUsernameResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credential_username"
}

// Schema should return the schema for this resource.
func (r *credentialUsernameResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `
Manages a username credential within Jenkins. This username may then be referenced within jobs that are created.

~> The "password" property may leave plain-text passwords in your state file. If using the property to manage the password in Terraform, ensure that your state file is properly secured and encrypted at rest.`,
		Attributes: r.schemaCredential(addWriteOnlySecret(map[string]schema.Attribute{
			"username": schema.StringAttribute{
				MarkdownDescription: "The username to be associated with the credentials.",
				Required:            true,
			},
			"password": schema.StringAttribute{
				MarkdownDescription: "The password to be associated with the credentials. If empty then the password property will become unmanaged and expected to be set manually within Jenkins. If set then the password will be updated only upon changes -- if the password is set manually within Jenkins then it will not reconcile this drift until the next time the password property is changed.",
				Optional:            true,
				Sensitive:           true,
			},
		}, "password", "The password to be associated with the credentials.")),
	}
}

// ConfigValidators enforces the plain/write-only secret constraints.
func (r *credentialUsernameResource) ConfigValidators(_ context.Context) []resource.ConfigValidator {
	return optionalWriteOnlySecretConfigValidators("password")
}

// Create is called when the provider must create a new resource. Config
// and planned state values should be read from the
// CreateRequest and new state values set on the CreateResponse.
func (r *credentialUsernameResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	tflog.Debug(ctx, "credentialUsernameResource.Create")
	var data credentialUsernameResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cm := r.credentialManagerForFolder(ctx, data.Folder.ValueString(), &resp.Diagnostics)
	if cm == nil {
		return
	}

	password := data.Password.ValueString()
	if passwordWo := r.readWriteOnly(ctx, req.Config, "password_wo", &resp.Diagnostics); !passwordWo.IsNull() {
		password = passwordWo.ValueString()
	}
	if resp.Diagnostics.HasError() {
		return
	}

	cred := jenkins.UsernameCredentials{
		ID:          data.Name.ValueString(),
		Scope:       data.Scope.ValueString(),
		Description: data.Description.ValueString(),
		Username:    data.Username.ValueString(),
		Password:    password,
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
func (r *credentialUsernameResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	tflog.Debug(ctx, "credentialUsernameResource.Read")
	var data credentialUsernameResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cm := r.credentialManager(data.Folder.ValueString())

	cred := jenkins.UsernameCredentials{}
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
	data.Username = types.StringValue(cred.Username)
	// NOTE: We are NOT setting the password here, as the password returned by GetSingle is garbage
	// Password only applies to Create/Update operations if the "password" property is non-empty

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// Update is called to update the state of the resource. Config, planned
// state, and prior state values should be read from the
// UpdateRequest and new state values set on the UpdateResponse.
func (r *credentialUsernameResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	tflog.Debug(ctx, "credentialUsernameResource.Update")
	var data credentialUsernameResourceModel
	var state credentialUsernameResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cm := r.credentialManager(data.Folder.ValueString())

	cred := jenkins.UsernameCredentials{
		ID:          data.Name.ValueString(),
		Scope:       data.Scope.ValueString(),
		Description: data.Description.ValueString(),
		Username:    data.Username.ValueString(),
	}

	// Send the password only when it should change; omitting it leaves the
	// Jenkins-stored value untouched (also correct for lifecycle.ignore_changes).
	// Write-only: re-send when the version trigger changed. Plain: re-send when
	// the value changed.
	if passwordWo := r.readWriteOnly(ctx, req.Config, "password_wo", &resp.Diagnostics); !passwordWo.IsNull() {
		if !data.PasswordWoVersion.Equal(state.PasswordWoVersion) {
			cred.Password = passwordWo.ValueString()
		}
	} else if !data.Password.Equal(state.Password) {
		cred.Password = data.Password.ValueString()
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
func (r *credentialUsernameResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data credentialUsernameResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.deleteCredential(ctx, data.Folder.ValueString(), data.Domain.ValueString(), data.Name.ValueString(), &resp.Diagnostics)
}
