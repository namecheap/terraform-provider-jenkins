package jenkins

import (
	"context"
	"encoding/xml"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// VaultAppRoleCredentials struct representing credential for storing Vault AppRole role id and secret id
type VaultAppRoleCredentials struct {
	XMLName     xml.Name `xml:"com.datapipe.jenkins.vault.credentials.VaultAppRoleCredential"`
	ID          string   `xml:"id"`
	Scope       string   `xml:"scope"`
	Description string   `xml:"description"`
	Namespace   string   `xml:"namespace"`
	Path        string   `xml:"path"`
	RoleID      string   `xml:"roleId"`
	SecretID    string   `xml:"secretId"`
}

type credentialVaultAppRoleResourceModel struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	Folder            types.String `tfsdk:"folder"`
	Description       types.String `tfsdk:"description"`
	Domain            types.String `tfsdk:"domain"`
	Scope             types.String `tfsdk:"scope"`
	Namespace         types.String `tfsdk:"namespace"`
	Path              types.String `tfsdk:"path"`
	RoleID            types.String `tfsdk:"role_id"`
	SecretID          types.String `tfsdk:"secret_id"`
	SecretIDWo        types.String `tfsdk:"secret_id_wo"`
	SecretIDWoVersion types.String `tfsdk:"secret_id_wo_version"`
}

type credentialVaultAppRoleResource struct {
	*resourceHelper
}

// Ensure the implementation satisfies the desired interfaces.
var _ resource.ResourceWithConfigure = &credentialVaultAppRoleResource{}
var _ resource.ResourceWithImportState = &credentialVaultAppRoleResource{}
var _ resource.ResourceWithConfigValidators = &credentialVaultAppRoleResource{}

func newCredentialVaultAppRoleResource() resource.Resource {
	return &credentialVaultAppRoleResource{
		resourceHelper: newResourceHelper(),
	}
}

// Metadata should return the full name of the resource.
func (r *credentialVaultAppRoleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credential_vault_approle"
}

// Schema should return the schema for this resource.
func (r *credentialVaultAppRoleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `
Manages a Vault AppRole credential within Jenkins. This credential may then be referenced within jobs that are created.

~> The "secret_id" property may leave plain-text secret id in your state file. If using the property to manage the secret id in Terraform, ensure that your state file is properly secured and encrypted at rest.

~> The Jenkins installation that uses this resource is expected to have the [Hashicorp Vault Plugin](https://plugins.jenkins.io/hashicorp-vault-plugin/) installed in their system.`,
		Attributes: r.schemaCredential(addWriteOnlySecret(map[string]schema.Attribute{
			"namespace": schema.StringAttribute{
				MarkdownDescription: "The Vault namespace of the approle credential.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
			},
			"path": schema.StringAttribute{
				MarkdownDescription: "The unique name of the approle auth backend. Defaults to `approle`.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("approle"),
			},
			"role_id": schema.StringAttribute{
				MarkdownDescription: "The role_id to be associated with the credentials.",
				Required:            true,
			},
			"secret_id": schema.StringAttribute{
				MarkdownDescription: "The secret_id to be associated with the credentials. If empty then the secret_id property will become unmanaged and expected to be set manually within Jenkins. If set then the secret_id will be updated only upon changes -- if the secret_id is set manually within Jenkins then it will not reconcile this drift until the next time the secret_id property is changed.",
				Optional:            true,
				Sensitive:           true,
			},
		}, "secret_id", "The secret_id to be associated with the credentials.")),
	}
}

// ConfigValidators enforces the plain/write-only secret constraints.
func (r *credentialVaultAppRoleResource) ConfigValidators(_ context.Context) []resource.ConfigValidator {
	return optionalWriteOnlySecretConfigValidators("secret_id")
}

// Create is called when the provider must create a new resource. Config
// and planned state values should be read from the
// CreateRequest and new state values set on the CreateResponse.
func (r *credentialVaultAppRoleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	tflog.Debug(ctx, "credentialVaultAppRoleResource.Create")
	var data credentialVaultAppRoleResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cm := r.credentialManagerForFolder(ctx, data.Folder.ValueString(), &resp.Diagnostics)
	if cm == nil {
		return
	}

	secretID := data.SecretID.ValueString()
	if secretIDWo := r.readWriteOnly(ctx, req.Config, "secret_id_wo", &resp.Diagnostics); !secretIDWo.IsNull() {
		secretID = secretIDWo.ValueString()
	}
	if resp.Diagnostics.HasError() {
		return
	}

	cred := VaultAppRoleCredentials{
		ID:          data.Name.ValueString(),
		Scope:       data.Scope.ValueString(),
		Description: data.Description.ValueString(),
		Namespace:   data.Namespace.ValueString(),
		Path:        data.Path.ValueString(),
		RoleID:      data.RoleID.ValueString(),
		SecretID:    secretID,
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
func (r *credentialVaultAppRoleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	tflog.Debug(ctx, "credentialVaultAppRoleResource.Read")
	var data credentialVaultAppRoleResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cm := r.credentialManager(data.Folder.ValueString())

	cred := VaultAppRoleCredentials{}
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
	data.Namespace = types.StringValue(cred.Namespace)
	data.Path = types.StringValue(cred.Path)
	data.RoleID = types.StringValue(cred.RoleID)
	// NOTE: We are NOT setting the secret here, as the secret returned by GetSingle is garbage
	// Secret only applies to Create/Update operations if the "secret_id" property is non-empty

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// Update is called to update the state of the resource. Config, planned
// state, and prior state values should be read from the
// UpdateRequest and new state values set on the UpdateResponse.
func (r *credentialVaultAppRoleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	tflog.Debug(ctx, "credentialVaultAppRoleResource.Update")
	var data credentialVaultAppRoleResourceModel
	var state credentialVaultAppRoleResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cm := r.credentialManager(data.Folder.ValueString())

	cred := VaultAppRoleCredentials{
		ID:          data.Name.ValueString(),
		Scope:       data.Scope.ValueString(),
		Description: data.Description.ValueString(),
		Namespace:   data.Namespace.ValueString(),
		Path:        data.Path.ValueString(),
		RoleID:      data.RoleID.ValueString(),
	}

	// Send the secret_id only when it should change; omitting it leaves the
	// Jenkins-stored value untouched (also correct for lifecycle.ignore_changes).
	// Write-only: re-send when the version trigger changed. Plain: re-send when
	// the value changed.
	if secretIDWo := r.readWriteOnly(ctx, req.Config, "secret_id_wo", &resp.Diagnostics); !secretIDWo.IsNull() {
		if !data.SecretIDWoVersion.Equal(state.SecretIDWoVersion) {
			cred.SecretID = secretIDWo.ValueString()
		}
	} else if !data.SecretID.Equal(state.SecretID) {
		cred.SecretID = data.SecretID.ValueString()
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
func (r *credentialVaultAppRoleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data credentialVaultAppRoleResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.deleteCredential(ctx, data.Folder.ValueString(), data.Domain.ValueString(), data.Name.ValueString(), &resp.Diagnostics)
}
