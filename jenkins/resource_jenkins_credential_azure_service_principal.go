package jenkins

import (
	"context"
	"encoding/xml"

	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// AzureServicePrincipalCredentials struct representing credential for storing Azure service credentials
type AzureServicePrincipalCredentials struct {
	XMLName     xml.Name                             `xml:"com.microsoft.azure.util.AzureCredentials"`
	ID          string                               `xml:"id"`
	Scope       string                               `xml:"scope"`
	Description string                               `xml:"description"`
	Data        AzureServicePrincipalCredentialsData `xml:"data"`
}

type AzureServicePrincipalCredentialsData struct {
	SubscriptionId          string `xml:"subscriptionId"`
	ClientId                string `xml:"clientId"`
	ClientSecret            string `xml:"clientSecret"`
	CertificateId           string `xml:"certificateId"`
	Tenant                  string `xml:"tenant"`
	AzureEnvironmentName    string `xml:"azureEnvironmentName"`
	ServiceManagementURL    string `xml:"serviceManagementURL"`
	AuthenticationEndpoint  string `xml:"authenticationEndpoint"`
	ResourceManagerEndpoint string `xml:"resourceManagerEndpoint"`
	GraphEndpoint           string `xml:"graphEndpoint"`
}

type credentialAzureServicePrincipalResourceModel struct {
	ID                      types.String `tfsdk:"id"`
	Name                    types.String `tfsdk:"name"`
	Folder                  types.String `tfsdk:"folder"`
	Description             types.String `tfsdk:"description"`
	Domain                  types.String `tfsdk:"domain"`
	Scope                   types.String `tfsdk:"scope"`
	SubscriptionId          types.String `tfsdk:"subscription_id"`
	ClientId                types.String `tfsdk:"client_id"`
	ClientSecret            types.String `tfsdk:"client_secret"`
	ClientSecretWo          types.String `tfsdk:"client_secret_wo"`
	ClientSecretWoVersion   types.String `tfsdk:"client_secret_wo_version"`
	CertificateId           types.String `tfsdk:"certificate_id"`
	Tenant                  types.String `tfsdk:"tenant"`
	AzureEnvironmentName    types.String `tfsdk:"azure_environment_name"`
	ServiceManagementURL    types.String `tfsdk:"service_management_url"`
	AuthenticationEndpoint  types.String `tfsdk:"authentication_endpoint"`
	ResourceManagerEndpoint types.String `tfsdk:"resource_manager_endpoint"`
	GraphEndpoint           types.String `tfsdk:"graph_endpoint"`
}

type credentialAzureServicePrincipalResource struct {
	*resourceHelper
}

// Ensure the implementation satisfies the desired interfaces.
var _ resource.ResourceWithConfigure = &credentialAzureServicePrincipalResource{}
var _ resource.ResourceWithImportState = &credentialAzureServicePrincipalResource{}
var _ resource.ResourceWithConfigValidators = &credentialAzureServicePrincipalResource{}

func newCredentialAzureServicePrincipalResource() resource.Resource {
	return &credentialAzureServicePrincipalResource{
		resourceHelper: newResourceHelper(),
	}
}

// Metadata should return the full name of the resource.
func (r *credentialAzureServicePrincipalResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credential_azure_service_principal"
}

// Schema should return the schema for this resource.
func (r *credentialAzureServicePrincipalResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `
Manages an Azure Service Principal credential within Jenkins. This credential may then be referenced within jobs that are created.

~> The "client_secret" property may leave plain-text secret id in your state file. If using the property to manage the secret id in Terraform, ensure that your state file is properly secured and encrypted at rest.

~> The Jenkins installation that uses this resource is expected to have the [Azure Credentials Plugin](https://plugins.jenkins.io/azure-credentials/) installed in their system.`,
		Attributes: r.schemaCredential(addWriteOnlySecret(map[string]schema.Attribute{
			"subscription_id": schema.StringAttribute{
				MarkdownDescription: "The Azure subscription id mapped to the Azure Service Principal.",
				Required:            true,
			},
			"client_id": schema.StringAttribute{
				MarkdownDescription: "The client id (application id) of the Azure Service Principal.",
				Required:            true,
			},
			"client_secret": schema.StringAttribute{
				MarkdownDescription: "The client secret of the Azure Service Principal. Cannot be used with `certificate_id` or `client_secret_wo`. Has to be specified, if neither `certificate_id` nor `client_secret_wo` is specified.",
				Sensitive:           true,
				Optional:            true,
			},
			"certificate_id": schema.StringAttribute{
				MarkdownDescription: "The certificate reference of the Azure Service Principal, pointing to a Jenkins certificate credential. Cannot be used with `client_secret` or `client_secret_wo`. Has to be specified, if neither `client_secret` nor `client_secret_wo` is specified.",
				Sensitive:           true,
				Optional:            true,
			},
			"tenant": schema.StringAttribute{
				MarkdownDescription: "The Azure Tenant ID of the Azure Service Principal.",
				Required:            true,
			},
			"azure_environment_name": schema.StringAttribute{
				MarkdownDescription: `The Azure Cloud enviroment name. Allowed values are "Azure", "Azure China", "Azure Germany", "Azure US Government".`,
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("Azure"),
				Validators: []validator.String{
					stringvalidator.OneOf("Azure", "Azure China", "Azure Germany", "Azure US Government"),
				},
			},
			"service_management_url": schema.StringAttribute{
				MarkdownDescription: "Override the Azure management endpoint URL for the selected Azure environment.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
			},
			"authentication_endpoint": schema.StringAttribute{
				MarkdownDescription: "Override the Azure Active Directory endpoint for the selected Azure environment.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
			},
			"resource_manager_endpoint": schema.StringAttribute{
				MarkdownDescription: "Override the Azure resource manager endpoint URL for the selected Azure environment.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
			},
			"graph_endpoint": schema.StringAttribute{
				MarkdownDescription: "Override the Azure graph endpoint URL for the selected Azure environment.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
			},
		}, "client_secret", "The client secret of the Azure Service Principal.")),
	}
}

// ConfigValidators enforces that exactly one of client_secret, client_secret_wo,
// or certificate_id is set, and that the write-only secret is paired with its
// version trigger.
func (r *credentialAzureServicePrincipalResource) ConfigValidators(_ context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		resourcevalidator.ExactlyOneOf(
			path.MatchRoot("client_secret"),
			path.MatchRoot("client_secret_wo"),
			path.MatchRoot("certificate_id"),
		),
		resourcevalidator.RequiredTogether(
			path.MatchRoot("client_secret_wo"),
			path.MatchRoot("client_secret_wo_version"),
		),
	}
}

// Create is called when the provider must create a new resource. Config
// and planned state values should be read from the
// CreateRequest and new state values set on the CreateResponse.
func (r *credentialAzureServicePrincipalResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	tflog.Debug(ctx, "credentialAzureServicePrincipalResource.Create")
	var data credentialAzureServicePrincipalResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cm := r.credentialManagerForFolder(ctx, data.Folder.ValueString(), &resp.Diagnostics)
	if cm == nil {
		return
	}

	clientSecret := data.ClientSecret.ValueString()
	if clientSecretWo := r.readWriteOnly(ctx, req.Config, "client_secret_wo", &resp.Diagnostics); !clientSecretWo.IsNull() {
		clientSecret = clientSecretWo.ValueString()
	}
	if resp.Diagnostics.HasError() {
		return
	}

	credData := AzureServicePrincipalCredentialsData{
		SubscriptionId:          data.SubscriptionId.ValueString(),
		ClientId:                data.ClientId.ValueString(),
		ClientSecret:            clientSecret,
		CertificateId:           data.CertificateId.ValueString(),
		Tenant:                  data.Tenant.ValueString(),
		AzureEnvironmentName:    data.AzureEnvironmentName.ValueString(),
		ServiceManagementURL:    data.ServiceManagementURL.ValueString(),
		AuthenticationEndpoint:  data.AuthenticationEndpoint.ValueString(),
		ResourceManagerEndpoint: data.ResourceManagerEndpoint.ValueString(),
		GraphEndpoint:           data.GraphEndpoint.ValueString(),
	}

	cred := AzureServicePrincipalCredentials{
		ID:          data.Name.ValueString(),
		Scope:       data.Scope.ValueString(),
		Description: data.Description.ValueString(),
		Data:        credData,
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
func (r *credentialAzureServicePrincipalResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	tflog.Debug(ctx, "credentialAzureServicePrincipalResource.Read")
	var data credentialAzureServicePrincipalResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cm := r.credentialManager(data.Folder.ValueString())

	cred := AzureServicePrincipalCredentials{}
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

	// NOTE: We are NOT setting the password here, as the password returned by GetSingle is garbage
	// Password only applies to Create/Update operations if the "password" property is non-empty

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// Update is called to update the state of the resource. Config, planned
// state, and prior state values should be read from the
// UpdateRequest and new state values set on the UpdateResponse.
func (r *credentialAzureServicePrincipalResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	tflog.Debug(ctx, "credentialAzureServicePrincipalResource.Update")
	var data credentialAzureServicePrincipalResourceModel
	var state credentialAzureServicePrincipalResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cm := r.credentialManager(data.Folder.ValueString())

	cred := buildAzureServicePrincipalUpdate(data, state)

	// When the client secret is supplied write-only it is absent from plan/state
	// (so the builder's plain-value comparison never sends it); re-send it from
	// config only when its version trigger changed.
	if clientSecretWo := r.readWriteOnly(ctx, req.Config, "client_secret_wo", &resp.Diagnostics); !clientSecretWo.IsNull() {
		if !data.ClientSecretWoVersion.Equal(state.ClientSecretWoVersion) {
			cred.Data.ClientSecret = clientSecretWo.ValueString()
		}
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
func (r *credentialAzureServicePrincipalResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data credentialAzureServicePrincipalResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.deleteCredential(ctx, data.Folder.ValueString(), data.Domain.ValueString(), data.Name.ValueString(), &resp.Diagnostics)
}

// buildAzureServicePrincipalUpdate maps the planned model (data) and the prior
// state into the credential payload sent to Jenkins on update.
//
// client_secret is written only when it changed, so that
// lifecycle.ignore_changes = [client_secret] leaves the Jenkins-stored secret
// untouched. certificate_id is written to the CertificateId field (the previous
// code wrote it to ClientId, clobbering the real client id and wiping
// certificate_id on every update — including description-only edits) and is
// re-sent whenever the credential is certificate-based, because omitting it on
// an unchanged update would send an empty value and delete the reference.
func buildAzureServicePrincipalUpdate(data, state credentialAzureServicePrincipalResourceModel) AzureServicePrincipalCredentials {
	cred := AzureServicePrincipalCredentials{
		ID:          data.Name.ValueString(),
		Scope:       data.Scope.ValueString(),
		Description: data.Description.ValueString(),
		Data: AzureServicePrincipalCredentialsData{
			SubscriptionId:          data.SubscriptionId.ValueString(),
			ClientId:                data.ClientId.ValueString(),
			Tenant:                  data.Tenant.ValueString(),
			AzureEnvironmentName:    data.AzureEnvironmentName.ValueString(),
			ServiceManagementURL:    data.ServiceManagementURL.ValueString(),
			AuthenticationEndpoint:  data.AuthenticationEndpoint.ValueString(),
			ResourceManagerEndpoint: data.ResourceManagerEndpoint.ValueString(),
			GraphEndpoint:           data.GraphEndpoint.ValueString(),
		},
	}

	if !data.ClientSecret.Equal(state.ClientSecret) {
		cred.Data.ClientSecret = data.ClientSecret.ValueString()
	}

	if data.CertificateId.ValueString() != "" {
		cred.Data.CertificateId = data.CertificateId.ValueString()
	}

	return cred
}
